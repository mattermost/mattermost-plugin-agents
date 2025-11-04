// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// EmbeddedMCPServer manages the lifecycle of an embedded MCP server within the plugin
// This provides in-memory communication between the plugin and MCP server, eliminating
// the need for OAuth flows and network communication
type EmbeddedMCPServer struct {
	server *mcpserver.MattermostInMemoryMCPServer
	logger pluginapi.LogService
}

// NewEmbeddedMCPServer creates a new embedded MCP server instance
func NewEmbeddedMCPServer(pluginAPI *pluginapi.Client, logger pluginapi.LogService) (*EmbeddedMCPServer, error) {
	// Get site URL from plugin configuration
	siteURL := ""
	if config := pluginAPI.Configuration.GetConfig(); config != nil && config.ServiceSettings.SiteURL != nil {
		siteURL = *config.ServiceSettings.SiteURL
	}

	if siteURL == "" {
		return nil, errors.New("site URL not configured, cannot initialize embedded MCP server")
	}

	// Create configuration for in-memory transport
	config := mcpserver.InMemoryConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: siteURL,
			// Just use siteURL for now. Could make this configurable later
			MMInternalServerURL: siteURL,
			DevMode:             false,
		},
	}

	// Create a logger adapter that routes MCP server logs through the plugin's logging system
	// This is now a simple pass-through since both use the same interface
	mcpLogger := NewPluginAPILoggerAdapter(logger)

	// Create the in-memory MCP server
	server, err := mcpserver.NewInMemoryServer(config, mcpLogger)
	if err != nil {
		return nil, err
	}

	embeddedServer := &EmbeddedMCPServer{
		server: server,
		logger: logger,
	}

	return embeddedServer, nil
}

// CreateClientTransport creates a new in-memory transport for a client connection
// Uses sessionID + token resolver pattern for better security than storing raw tokens
func (e *EmbeddedMCPServer) CreateClientTransport(userID, sessionID string, pluginAPI *pluginapi.Client) (*mcp.InMemoryTransport, error) {
	// Create token resolver that has closure over pluginAPI
	// This allows the mcpserver to get fresh tokens without storing raw tokens in context
	tokenResolver := func(sid string) (string, error) {
		session, err := pluginAPI.Session.Get(sid)
		if err != nil {
			e.logger.Debug("Failed to get session for token resolution",
				"user_id", userID,
				"session_id", sid,
				"error", err)
			return "", fmt.Errorf("failed to get session: %w", err)
		}
		if session == nil {
			return "", fmt.Errorf("session not found")
		}
		return session.Token, nil
	}

	// Create the connection through the server with resolver
	clientTransport, err := e.server.CreateConnectionForUser(userID, sessionID, tokenResolver)
	if err != nil {
		return nil, err
	}

	e.logger.Debug("Created client transport for embedded MCP server",
		"user_id", userID,
		"session_id", sessionID)

	return clientTransport, nil
}
