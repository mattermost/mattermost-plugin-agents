// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MattermostStdioMCPServer wraps MattermostMCPServer for STDIO transport
type MattermostStdioMCPServer struct {
	*MattermostMCPServer
	config StdioConfig
}

// NewStdioServer creates a new STDIO transport MCP server
func NewStdioServer(config StdioConfig, logger loggerlib.Logger) (*MattermostStdioMCPServer, error) {
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

	// Register tools with local access mode
	mattermostServer.registerTools(tools.AccessModeLocal)

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
