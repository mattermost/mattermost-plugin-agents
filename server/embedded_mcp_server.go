// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"fmt"
	"strings"

	localmcp "github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/tools"
	"github.com/mattermost/mattermost/server/public/model"
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
// searchService and fileContentService are optional and can be nil when the
// corresponding capability is unavailable
func NewEmbeddedMCPServer(pluginAPI *pluginapi.Client, logger pluginapi.LogService, searchService tools.SemanticSearchService, fileContentService tools.FileContentService) (*EmbeddedMCPServer, error) {
	// Get site URL from plugin configuration
	siteURL := ""
	if config := pluginAPI.Configuration.GetConfig(); config != nil && config.ServiceSettings.SiteURL != nil {
		siteURL = *config.ServiceSettings.SiteURL
	}

	if siteURL == "" {
		return nil, errors.New("site URL not configured, cannot initialize embedded MCP server")
	}

	// Determine the internal server URL for API communication
	internalServerURL := deriveInternalServerURL(pluginAPI, siteURL)

	logger.Debug("Embedded MCP server configuration",
		"siteURL", siteURL,
		"internalServerURL", internalServerURL)

	// Create configuration for in-memory transport
	config := mcpserver.InMemoryConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: siteURL,
			// Use the internal server URL for API communication within the container
			MMInternalServerURL: internalServerURL,
			DevMode:             false,
		},
	}

	// Create a logger adapter that routes MCP server logs through the plugin's logging system
	// This is now a simple pass-through since both use the same interface
	mcpLogger := NewPluginAPILoggerAdapter(logger)

	// Create the in-memory MCP server
	server, err := mcpserver.NewInMemoryServer(config, mcpLogger, searchService, fileContentService)
	if err != nil {
		return nil, err
	}

	embeddedServer := &EmbeddedMCPServer{
		server: server,
		logger: logger,
	}

	return embeddedServer, nil
}

// CreateClientTransport creates a new in-memory transport for a client connection.
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
	hookStore := localmcp.NewBeforeHookStore(&pluginAPI.KV)
	beforeHookResolver := func(userID, toolName, hookKey string) (string, error) {
		entry, err := hookStore.Resolve(userID, toolName, hookKey)
		if err != nil {
			return "", err
		}
		return entry.CallbackURL, nil
	}

	// Create the connection through the server with resolver
	clientTransport, err := e.server.CreateConnectionForUser(userID, sessionID, tokenResolver, beforeHookResolver)
	if err != nil {
		return nil, err
	}

	e.logger.Debug("Created client transport for embedded MCP server",
		"user_id", userID,
		"session_id", sessionID)

	return clientTransport, nil
}

// deriveInternalServerURL determines the internal server URL for API
// communication. We prefer the listen address (localhost) so the call stays
// in-process and survives external port remapping (e.g. Docker), but when
// Mattermost terminates TLS itself we fall back to SiteURL — hitting localhost
// on the HTTPS listener fails cert verification because the cert is issued for
// the public hostname (MM-69180).
func deriveInternalServerURL(pluginAPI *pluginapi.Client, siteURL string) string {
	return deriveInternalServerURLFromConfig(pluginAPI.Configuration.GetConfig(), siteURL)
}

func deriveInternalServerURLFromConfig(config *model.Config, siteURL string) string {
	const defaultURL = "http://localhost:8065"
	if config == nil {
		return defaultURL
	}

	tlsTerminated := config.ServiceSettings.ConnectionSecurity != nil &&
		*config.ServiceSettings.ConnectionSecurity == model.ConnSecurityTLS

	if tlsTerminated && siteURL != "" {
		return siteURL
	}

	scheme := "http://"
	if tlsTerminated {
		scheme = "https://"
	}

	if config.ServiceSettings.ListenAddress == nil || *config.ServiceSettings.ListenAddress == "" {
		return defaultURL
	}
	listenAddr := *config.ServiceSettings.ListenAddress
	switch {
	case listenAddr[0] == ':':
		return scheme + "localhost" + listenAddr
	case strings.HasPrefix(listenAddr, "0.0.0.0"):
		return scheme + "localhost" + listenAddr[len("0.0.0.0"):]
	case strings.HasPrefix(listenAddr, "[::]:"):
		return scheme + "localhost:" + listenAddr[len("[::]:"):]
	default:
		return scheme + listenAddr
	}
}
