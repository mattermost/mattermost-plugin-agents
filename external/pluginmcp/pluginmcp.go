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

// ReregisterHeader marks a POST from the Agents plugin to the server's Path
// asking the source plugin to register again, for example after the Agents
// plugin restarted. ServeHTTP answers with the same header set, which tells the
// Agents plugin that this library handled the request.
const ReregisterHeader = "X-Mattermost-Mcp-Reregister"

// Values of ReregisterHeader on the response.
const (
	ReregisterAccepted = "accepted"
	ReregisterStopped  = "stopped"
)

// retryPolicy controls the Register() backoff loop. Retries continue until
// registration succeeds, fails permanently, or Unregister is called; a warning
// is logged once after warnAfterAttempts failures. Tests override fields.
type retryPolicy struct {
	baseDelay         time.Duration
	maxDelay          time.Duration
	warnAfterAttempts int
}

var defaultRetryPolicy = retryPolicy{
	baseDelay:         1 * time.Second,
	maxDelay:          30 * time.Second,
	warnAfterAttempts: 15,
}

// Server is a cross-plugin MCP server owned by a source plugin. Safe for
// concurrent use after construction.
type Server struct {
	server    *mcp.Server
	config    Config
	pluginAPI PluginAPI

	// streamableHandler lazily builds the go-sdk HTTP handler on first request.
	streamableHandler func() http.Handler

	// Unregister calls regCancel to stop pending retries before firing its
	// own POST. regWG tracks the in-flight register goroutine so Unregister
	// can wait for it to drain before posting /unregister, avoiding a race
	// where a late /register lands after /unregister.
	regCtx    context.Context
	regCancel context.CancelFunc
	regWG     sync.WaitGroup

	// regMu guards registering and registerAgain, and orders regCancel
	// against starting a new register goroutine so regWG.Go never races
	// Unregister's regWG.Wait.
	regMu sync.Mutex
	// registering is true while the register goroutine runs.
	registering bool
	// registerAgain asks the running goroutine for one more registration
	// after the current one finishes, so a request that arrives just as a
	// registration completes is not lost.
	registerAgain bool

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
	s := &Server{
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
	s.streamableHandler = sync.OnceValue(s.buildStreamableHandler)
	return s
}
