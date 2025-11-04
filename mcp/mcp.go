// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package mcp provides a client for the Model Control Protocol (MCP) that allows
// the AI plugin to access external tools provided by MCP servers.
//
// The UserClients represents a single user's connection to multiple MCP servers.
// The Client represents a connection to a single MCP server.
// The UserClients currently only supports authentication via Mattermost user ID header
// X-Mattermost-UserID. In the future it will support our OAuth implementation.
//
// The ClientManager manages multiple UserClients, allowing for efficient mangement
// of connections. It is responsible for creating and closing UserClients as needed.
//
// The organization reflects the need for each user to have their own connection to
// the MCP server given the design of MCP.
package mcp

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// Errors represents a collection of errors from MCP operations.
type Errors struct {
	ToolAuthErrors []llm.ToolAuthError // Authentication errors users need to resolve
	Errors         []error             // Generic errors (connection, config, etc.)
}

// EmbeddedServerConfig contains configuration for the embedded MCP server
type EmbeddedServerConfig struct {
	Enabled bool `json:"enabled"`
}

// Config contains the configuration for the MCP  servers
type Config struct {
	Enabled            bool                 `json:"enabled"`
	EnablePluginServer bool                 `json:"enablePluginServer"`
	Servers            []ServerConfig       `json:"servers"`
	EmbeddedServer     EmbeddedServerConfig `json:"embeddedServer"`
	IdleTimeoutMinutes int                  `json:"idleTimeoutMinutes"`
}

// DiscoverRemoteServerTools creates a temporary connection to a remote MCP server and discovers its tools
func DiscoverRemoteServerTools(
	ctx context.Context,
	userID string,
	serverConfig ServerConfig,
	log pluginapi.LogService,
	oauthManger *OAuthManager,
	toolsCache *ToolsCache,
) ([]ToolInfo, error) {
	// Create and connect to the remote server
	client, err := NewClient(ctx, userID, serverConfig, log, oauthManger, toolsCache)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	serverTools := client.Tools()
	tools := make([]ToolInfo, 0, len(serverTools))
	for _, tool := range serverTools {
		var inputSchema map[string]interface{}
		if tool.InputSchema.Properties != nil {
			inputSchema = map[string]interface{}{
				"type":       tool.InputSchema.Type,
				"properties": tool.InputSchema.Properties,
				"required":   tool.InputSchema.Required,
			}
		}

		tools = append(tools, ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		})
	}

	return tools, nil
}

// DiscoverEmbeddedServerTools creates a temporary connection to an embedded MCP server and discovers its tools
func DiscoverEmbeddedServerTools(
	ctx context.Context,
	userID string,
	sessionID string,
	embeddedServerConfig EmbeddedServerConfig,
	embeddedServer EmbeddedMCPServer,
	log pluginapi.LogService,
	pluginAPI *pluginapi.Client,
) ([]ToolInfo, error) {
	if !embeddedServerConfig.Enabled {
		return nil, fmt.Errorf("embedded server is not enabled")
	}

	// Create embedded client helper and connect to the embedded server
	embeddedClient := NewEmbeddedServerClient(embeddedServer, log, pluginAPI)

	client, err := embeddedClient.CreateClient(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	serverTools := client.Tools()
	tools := make([]ToolInfo, 0, len(serverTools))
	for _, tool := range serverTools {
		var inputSchema map[string]interface{}
		if tool.InputSchema.Properties != nil {
			inputSchema = map[string]interface{}{
				"type":       tool.InputSchema.Type,
				"properties": tool.InputSchema.Properties,
				"required":   tool.InputSchema.Required,
			}
		}

		tools = append(tools, ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		})
	}

	return tools, nil
}
