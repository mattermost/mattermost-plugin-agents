// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"encoding/json"
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
	SSEHandler           http.Handler
	MessageHandler       http.Handler
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

	// Create OAuth authentication provider (validates Bearer tokens)
	authProvider := auth.NewOAuthAuthenticationProvider(
		siteURL, // External server URL
		"",      // Internal server URL (use external)
		siteURL, // OAuth issuer
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

	// Create SSE handler for backwards compatibility
	sseHandler := mcp.NewSSEHandler(func(req *http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.SSEOptions{})

	// Create streamable HTTP handler for modern MCP communication
	streamableHandler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		return mcpServer
	}, nil)

	// Create OAuth metadata handler
	metadataHandler := func(w http.ResponseWriter, r *http.Request) {
		handleOAuthMetadata(w, r, siteURL)
	}

	// The metadata URL for WWW-Authenticate headers
	metadataURL := fmt.Sprintf("%s/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource", siteURL)

	handlers := &PluginMCPHandlers{
		MCPHandler:           streamableHandler,
		SSEHandler:           sseHandler,
		MessageHandler:       sseHandler, // Message endpoint uses SSE handler
		OAuthMetadataHandler: metadataHandler,
		siteURL:              siteURL,
		metadataURL:          metadataURL,
	}

	// Wrap handlers with 401 interceptor to add WWW-Authenticate header
	handlers.MCPHandler = handlers.wrap401Handler(handlers.MCPHandler)
	handlers.SSEHandler = handlers.wrap401Handler(handlers.SSEHandler)
	handlers.MessageHandler = handlers.wrap401Handler(handlers.MessageHandler)

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

// handleOAuthMetadata serves the OAuth 2.0 Protected Resource Metadata (RFC 9728)
func handleOAuthMetadata(w http.ResponseWriter, r *http.Request, siteURL string) {
	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// The resource URL is the base URL where MCP endpoints are hosted
	// For plugin-embedded server, this is the plugin mcp-server path
	resourceURL := fmt.Sprintf("%s/plugins/mattermost-ai/mcp-server", siteURL)

	// Create protected resource metadata per RFC 9728
	metadata := ProtectedResourceMetadata{
		Resource: resourceURL, // The protected resource's base URL (plugin path)
		AuthorizationServers: []string{
			siteURL, // Mattermost is the authorization server
		},
		ScopesSupported: []string{
			"user",
		},
		ResourceName: "Mattermost MCP Server",
	}

	// Set required headers per RFC 9728
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600") // Cache for 1 hour

	// Marshal and write JSON response
	jsonBytes, err := json.Marshal(metadata)
	if err != nil {
		http.Error(w, "Failed to encode metadata", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(jsonBytes)
}
