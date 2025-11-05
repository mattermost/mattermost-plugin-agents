// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/tools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PluginMCPHandlers contains the HTTP handlers for MCP endpoints
// These handlers are designed to be embedded in a plugin's HTTP router
type PluginMCPHandlers struct {
	MCPHandler           http.Handler
	OAuthMetadataHandler http.HandlerFunc
	siteURL              string
	metadataURL          string
}

// NewPluginMCPHandlers creates MCP handlers for use within a Mattermost plugin
// The handlers expect requests to have an Authorization Bearer token injected by the plugin middleware
func NewPluginMCPHandlers(siteURL string, logger loggerlib.Logger) (*PluginMCPHandlers, error) {
	if siteURL == "" {
		return nil, fmt.Errorf("site URL cannot be empty")
	}

	if logger == nil {
		var err error
		logger, err = loggerlib.CreateDefaultLogger()
		if err != nil {
			return nil, fmt.Errorf("failed to create default logger: %w", err)
		}
	}

	logger.Debug("Initializing embedded MCP server handlers testing123")

	// Create Session authentication provider (validates session IDs with token resolver)
	authProvider := auth.NewSessionAuthenticationProvider(
		siteURL, // External server URL
		"",      // Internal server URL (use external)
		logger,
	)

	// Create MCP server
	mcpServer := mcp.NewServer(
		&mcp.Implementation{
			Name:    "mattermost-mcp-server",
			Version: "0.1.0",
		},
		nil, // ServerOptions
	)

	// Register tools with remote access mode
	toolProvider := tools.NewMattermostToolProvider(
		authProvider,
		logger,
		siteURL, // External server URL
		"",      // Internal server URL (use external)
		false,   // devMode
		tools.AccessModeRemote,
	)
	toolProvider.ProvideTools(mcpServer)

	// Create streamable HTTP handler for modern MCP communication
	streamableHandler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		return mcpServer
	}, nil)

	// Create OAuth metadata handler using shared implementation
	resourceURL := fmt.Sprintf("%s/plugins/mattermost-ai/mcp-server", siteURL)
	metadataHandler := CreateOAuthMetadataHandler(resourceURL, siteURL, "Mattermost MCP Server")

	// The metadata URL for WWW-Authenticate headers
	metadataURL := fmt.Sprintf("%s/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource", siteURL)

	handlers := &PluginMCPHandlers{
		MCPHandler:           streamableHandler,
		OAuthMetadataHandler: metadataHandler,
		siteURL:              siteURL,
		metadataURL:          metadataURL,
	}

	// Wrap handler with 401 interceptor to add WWW-Authenticate header
	handlers.MCPHandler = handlers.wrap401Handler(handlers.MCPHandler)

	return handlers, nil
}

// wrap401Handler wraps an HTTP handler to intercept 401 responses and add WWW-Authenticate header
func (h *PluginMCPHandlers) wrap401Handler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create a response recorder to capture the status code and check for WWW-Authenticate
		recorder := &pluginResponseRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
			metadataURL:    h.metadataURL,
		}

		// Call the original handler with our recorder
		handler.ServeHTTP(recorder, r)
	})
}

// pluginResponseRecorder captures the HTTP status code and adds WWW-Authenticate on 401
type pluginResponseRecorder struct {
	http.ResponseWriter
	statusCode  int
	metadataURL string
}

func (r *pluginResponseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode

	// If we got a 401, add the WWW-Authenticate header before writing
	if statusCode == http.StatusUnauthorized && r.Header().Get("WWW-Authenticate") == "" {
		r.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, r.metadataURL))
	}

	r.ResponseWriter.WriteHeader(statusCode)
}
