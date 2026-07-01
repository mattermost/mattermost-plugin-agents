// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MattermostStdioMCPServer wraps MattermostMCPServer for STDIO transport
type MattermostStdioMCPServer struct {
	*MattermostMCPServer
	config StdioConfig
}

// NewStdioServer creates a new STDIO transport MCP server.
// searchService and fileContentService are optional — if nil, default HTTP-based
// services are created that call back to the plugin's /api/v1 endpoints.
func NewStdioServer(config StdioConfig, logger loggerlib.Logger, searchService tools.SemanticSearchService, fileContentService tools.FileContentService) (*MattermostStdioMCPServer, error) {
	if config.MMServerURL == "" {
		return nil, fmt.Errorf("server URL cannot be empty")
	}
	if config.PersonalAccessToken == "" {
		return nil, fmt.Errorf("personal access token cannot be empty")
	}

	if logger == nil {
		var err error
		logger, err = loggerlib.CreateDefaultLogger()
		if err != nil {
			return nil, fmt.Errorf("failed to create default logger: %w", err)
		}
	}

	mattermostServer := &MattermostStdioMCPServer{
		MattermostMCPServer: &MattermostMCPServer{
			logger: logger,
			config: config,
		},
		config: config,
	}

	// Create authentication provider
	mattermostServer.authProvider = auth.NewTokenAuthenticationProvider(config.GetMMServerURL(), config.GetMMInternalServerURL(), config.PersonalAccessToken, logger)

	mattermostServer.mcpServer = mcp.NewServer(
		&mcp.Implementation{
			Name:    "mattermost-mcp-server",
			Version: "0.1.0",
		},
		&mcp.ServerOptions{}, // ServerOptions
	)

	// Validate token at startup for STDIO
	if err := mattermostServer.authProvider.ValidateAuth(context.Background()); err != nil {
		return nil, fmt.Errorf("startup token validation failed: %w", err)
	}

	// Use provided services or create default HTTP callback services
	pluginURL := strings.TrimRight(config.GetMMServerURL(), "/") + "/plugins/mattermost-ai"
	if searchService == nil {
		searchService = tools.NewHTTPSemanticSearchService(pluginURL)
	}
	if fileContentService == nil {
		fileContentService = tools.NewHTTPFileContentService(pluginURL)
	}

	// Register tools with local access mode
	mattermostServer.registerTools(tools.AccessModeLocal, searchService, fileContentService)

	return mattermostServer, nil
}

// Serve starts the STDIO MCP server
func (s *MattermostStdioMCPServer) Serve() error {
	return s.serveStdio()
}

// serveStdio starts the server using stdio transport
func (s *MattermostMCPServer) serveStdio() error {
	// Add context with cancellation for graceful shutdown
	ctx := context.Background()

	// Log startup
	s.logger.Info("Starting MCP server with STDIO transport")

	transport := &mcp.StdioTransport{}

	err := s.mcpServer.Run(ctx, transport)
	if err != nil {
		s.logger.Error("MCP server stopped with error", "error", err)
	} else {
		s.logger.Info("MCP server stopped gracefully")
	}
	return err
}
