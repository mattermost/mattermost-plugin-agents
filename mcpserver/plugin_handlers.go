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

	trackAIGenerated := true

	// Create server config for tool provider
	config := BaseConfig{
		MMServerURL:         siteURL,
		MMInternalServerURL: "",
		DevMode:             false,
		TrackAIGenerated:    &trackAIGenerated,
	}

	// Register tools with remote access mode
	toolProvider := tools.NewMattermostToolProvider(
		authProvider,
		logger,
		config,
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

	return &PluginMCPHandlers{
		MCPHandler:           streamableHandler,
		OAuthMetadataHandler: metadataHandler,
		siteURL:              siteURL,
		metadataURL:          metadataURL,
	}, nil
}
