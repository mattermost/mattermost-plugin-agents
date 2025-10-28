// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/tools"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MattermostInMemoryMCPServer wraps MattermostMCPServer for in-memory transport
// This server runs embedded within the plugin process and uses session-based authentication
type MattermostInMemoryMCPServer struct {
	*MattermostMCPServer
	config InMemoryConfig
}

// NewInMemoryServer creates a new in-memory transport MCP server
// This server is designed to run embedded within the plugin process
func NewInMemoryServer(config InMemoryConfig, logger loggerlib.Logger) (*MattermostInMemoryMCPServer, error) {
	if config.MMServerURL == "" {
		return nil, fmt.Errorf("mattermost server URL cannot be empty for in-memory transport")
	}

	if logger == nil {
		var err error
		logger, err = loggerlib.CreateDefaultLogger()
		if err != nil {
			return nil, fmt.Errorf("failed to create default logger: %w", err)
		}
	}

	mattermostServer := &MattermostInMemoryMCPServer{
		MattermostMCPServer: &MattermostMCPServer{
			logger: logger,
			config: config,
		},
		config: config,
	}

	// Create session authentication provider for in-memory transport
	mattermostServer.authProvider = auth.NewSessionAuthenticationProvider(
		config.GetMMServerURL(),
		config.GetMMInternalServerURL(),
		logger,
	)

	// Create MCP server instance
	mattermostServer.mcpServer = mcp.NewServer(
		&mcp.Implementation{
			Name:    "mattermost-mcp-server-embedded",
			Version: "0.1.0",
		},
		nil, // ServerOptions - keeping nil for now
	)

	// Register tools with remote access mode (embedded clients are treated as remote)
	mattermostServer.registerTools(tools.AccessModeRemote)

	logger.Info("Created in-memory MCP server")

	return mattermostServer, nil
}

// CreateConnectionForUser creates a new in-memory transport connection for a specific user
// Returns the client-side transport that should be used by the MCP client
// Accepts either:
// - sessionID + tokenResolver: Creates authenticated connection
// - empty sessionID + nil tokenResolver: Creates unauthenticated connection (for tool discovery)
func (s *MattermostInMemoryMCPServer) CreateConnectionForUser(userID, sessionID string, tokenResolver auth.TokenResolver) (*mcp.InMemoryTransport, error) {
	if userID == "" {
		return nil, fmt.Errorf("userID cannot be empty")
	}

	// Create context with sessionID and resolver
	ctx := context.Background()
	if sessionID != "" && tokenResolver != nil {
		ctx = context.WithValue(ctx, auth.SessionIDContextKey, sessionID)
		ctx = context.WithValue(ctx, auth.TokenResolverContextKey, tokenResolver)

		// Validate the session at connection time
		_, err := s.validateUserIdentity(ctx, userID)
		if err != nil {
			return nil, err
		}
	}

	// Create new in-memory transport pair
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	// Start the server with the server-side transport in a goroutine
	go func() {
		// Recover from panics to prevent silent failures
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("MCP server panicked",
					"user_id", userID,
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()

		s.logger.Debug("Starting MCP server for in-memory transport",
			"user_id", userID)

		// The server will run until the transport is closed
		if err := s.mcpServer.Run(ctx, serverTransport); err != nil {
			s.logger.Warn("In-memory MCP server stopped",
				"user_id", userID,
				"error", err)
		}
	}()

	s.logger.Debug("Created new in-memory transport for user",
		"user_id", userID)

	// Return the client-side transport
	return clientTransport, nil
}

func (s *MattermostInMemoryMCPServer) validateUserIdentity(ctx context.Context, expectedUserID string) (*model.User, error) {
	identityProvider, ok := s.authProvider.(auth.UserIdentityProvider)
	if !ok {
		return nil, fmt.Errorf("authentication provider does not support identity verification")
	}

	user, err := identityProvider.GetAuthenticatedUser(ctx)
	if err != nil {
		return nil, err
	}

	if user.Id != expectedUserID {
		return nil, fmt.Errorf("session token belongs to a different user: expected %s, got %s", expectedUserID, user.Id)
	}

	return user, nil
}
