// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package pluginmcp helps Mattermost plugins expose MCP tools to the Agents
// plugin. It handles tool-name namespacing, inter-plugin request checks, and
// async registration retries.
package pluginmcp

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const agentsPluginID = "mattermost-ai"

// Config is the wire descriptor sent to the Agents plugin on register.
// Version defaults to "0.0.1" when empty.
type Config struct {
	PluginID string `json:"plugin_id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	// ExposeExternal, when true, allows the server's tools to appear on the
	// Agents plugin's external MCP aggregate (subject to Enabled and admin tool policy).
	ExposeExternal bool   `json:"expose_external"`
	Version        string `json:"version,omitempty"`
}

// PluginAPI is the minimal Mattermost plugin API subset pluginmcp needs.
type PluginAPI interface {
	PluginHTTP(*http.Request) *http.Response
}

// retryPolicy controls the Register() backoff loop. Tests override fields.
type retryPolicy struct {
	baseDelay   time.Duration
	maxDelay    time.Duration
	maxAttempts int
}

var defaultRetryPolicy = retryPolicy{
	baseDelay:   1 * time.Second,
	maxDelay:    8 * time.Second,
	maxAttempts: 15,
}

// Server is a cross-plugin MCP server owned by a source plugin. Safe for
// concurrent use after construction.
type Server struct {
	server    *mcp.Server
	config    Config
	pluginAPI PluginAPI

	// mu guards lazy init of handler against concurrent first requests.
	mu             sync.Mutex
	handler        http.Handler
	handlerBuiltOK bool

	// Unregister calls regCancel to stop pending retries before firing its
	// own POST. regWG tracks the in-flight register goroutine so Unregister
	// can wait for it to drain before posting /unregister, avoiding a race
	// where a late /register lands after /unregister.
	regCtx    context.Context
	regCancel context.CancelFunc
	regWG     sync.WaitGroup

	retry retryPolicy
}

// NewServer constructs a cross-plugin MCP server. The config must have
// non-empty PluginID, Name, and Path.
func NewServer(pluginAPI PluginAPI, config Config) *Server {
	regCtx, regCancel := context.WithCancel(context.Background())
	version := config.Version
	if version == "" {
		version = "0.0.1"
	}
	return &Server{
		server: mcp.NewServer(
			&mcp.Implementation{
				Name:    config.PluginID,
				Version: version,
			},
			nil,
		),
		config:    config,
		pluginAPI: pluginAPI,
		regCtx:    regCtx,
		regCancel: regCancel,
		retry:     defaultRetryPolicy,
	}
}
