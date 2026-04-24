// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package mcphelper provides a turnkey way for Mattermost plugins to expose
// MCP (Model Context Protocol) tools to the Agents plugin. Source plugins
// construct a Server via NewServer, register tools via AddTool, delegate their
// HTTP handler, and call Register / Unregister in OnActivate / OnDeactivate.
//
// The helper wraps github.com/modelcontextprotocol/go-sdk v1.4.1 with
// Mattermost-specific conventions:
//
//   - Tool names are namespaced as "{pluginID}__{toolName}" so the Agents
//     plugin can attribute calls to their source plugin.
//   - The HTTP endpoint rejects requests that did not arrive via the
//     Mattermost inter-plugin RPC path (security gate on the
//     Mattermost-Plugin-ID header).
//   - Registration with the Agents plugin is asynchronous and retried with
//     exponential backoff, so OnActivate does not block on Agents readiness.
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

// PluginAPI is the minimal subset of the Mattermost plugin API that mcphelper
// needs — specifically the PluginHTTP inter-plugin request primitive. A real
// plugin's *pluginapi.Client.API or plugin.API value satisfies this interface
// automatically; callers can also pass a test double.
//
// This is a separate declaration from bridgeclient.PluginAPI (same shape) to
// avoid forcing mcphelper consumers to transitively import bridgeclient just
// to get a type name.
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

// Server is a cross-plugin MCP server owned by a source plugin. It wraps the
// go-sdk *mcp.Server and a lazily-constructed *mcp.StreamableHTTPHandler, and
// coordinates registration with the Agents plugin.
//
// Server is safe for concurrent use after construction; AddTool may be called
// from any goroutine before the first ServeHTTP. In practice, plugins call
// AddTool from OnActivate and then never again.
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

// NewServer constructs a cross-plugin MCP server. The pluginAPI argument is
// the Mattermost plugin API (typically p.API or pluginapi.NewClient(...).API).
// The config must have non-empty PluginID, Name, and Path (Path typically "/mcp").
//
// The returned Server has no tools registered. Call AddTool for each tool,
// wire ServeHTTP from the plugin's http.Handler for r.URL.Path == config.Path,
// and call Register from OnActivate.
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
