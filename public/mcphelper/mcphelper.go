// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package mcphelper helps Mattermost plugins expose MCP tools to the Agents
// plugin. It handles tool-name namespacing, inter-plugin request checks, and
// async registration retries.
package mcphelper

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PluginMCPServer is the wire-serialized descriptor this plugin sends to the
// Agents plugin when registering. The Agents plugin stores it in its
// ClientManager registry and uses Path to build the inbound MCP URL.
//
// Version is advertised to MCP clients via the underlying go-sdk
// Implementation struct's Version field. If empty, NewServer defaults to
// "0.0.1".
type PluginMCPServer struct {
	PluginID string `json:"plugin_id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Version  string `json:"version,omitempty"`
}

// PluginAPI is the minimal Mattermost plugin API subset mcphelper needs.
type PluginAPI interface {
	PluginHTTP(*http.Request) *http.Response
}

// retryPolicy controls the Register() exponential-backoff loop. Exposed
// unexported so tests in the same package can shrink the delays; production
// uses the defaults set in NewServer.
type retryPolicy struct {
	baseDelay   time.Duration // first sleep
	maxDelay    time.Duration // cap on doubled delays
	maxAttempts int           // including the first immediate try
}

var defaultRetryPolicy = retryPolicy{
	baseDelay:   1 * time.Second,
	maxDelay:    8 * time.Second,
	maxAttempts: 15,
}

// Server is a cross-plugin MCP server owned by a source plugin. It is safe for
// concurrent use after construction.
type Server struct {
	server    *mcp.Server
	config    PluginMCPServer
	pluginAPI PluginAPI

	// Streamable HTTP handler is lazily constructed on first ServeHTTP; see
	// server.go. mu guards lazy init so concurrent first-requests don't race.
	mu             sync.Mutex
	handler        http.Handler
	handlerBuiltOK bool

	// regCtx / regCancel control the goroutine spawned by Register(). Unregister
	// calls regCancel to stop pending retries before firing its own POST.
	regCtx    context.Context
	regCancel context.CancelFunc

	// retry is the policy used by the registration goroutine. Tests in the
	// same package can rewrite this field before calling Register().
	retry retryPolicy
}

// NewServer constructs a cross-plugin MCP server. The config must have
// non-empty PluginID, Name, and Path.
func NewServer(pluginAPI PluginAPI, config PluginMCPServer) *Server {
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
			nil, // ServerOptions: defaults are fine; source plugins that need
			//                    custom logging can wrap externally.
		),
		config:    config,
		pluginAPI: pluginAPI,
		regCtx:    regCtx,
		regCancel: regCancel,
		retry:     defaultRetryPolicy,
	}
}
