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

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// Errors represents a collection of errors from MCP operations.
type Errors struct {
	ToolAuthErrors []llm.ToolAuthError // Authentication errors users need to resolve
	Errors         []error             // Generic errors (connection, config, etc.)
}

// Config contains the configuration for the MCP  servers
type Config struct {
	Enabled            bool           `json:"enabled"`
	Servers            []ServerConfig `json:"servers"`
	IdleTimeoutMinutes int            `json:"idleTimeoutMinutes"`
}

// DiscoverServerTools creates a temporary connection to an MCP server and discovers its tools
func DiscoverServerTools(
	ctx context.Context,
	userID string,
	serverConfig ServerConfig,
	log pluginapi.LogService,
	oauthManger *OAuthManager,
) ([]ToolInfo, error) {
	// Create and connect to the server
	client, err := NewClient(ctx, userID, serverConfig, log, oauthManger)
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
