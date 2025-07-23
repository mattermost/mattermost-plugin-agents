// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// ToolInfo represents a tool's metadata for discovery purposes
type ToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// UserClients represents a per-user MCP client with multiple server connections
type UserClients struct {
	clients      map[string]*Client
	userID       string
	log          pluginapi.LogService
	oauthManager *OAuthManager
}

func NewUserClients(userID string, log pluginapi.LogService, oauthManager *OAuthManager) *UserClients {
	return &UserClients{
		log:          log,
		clients:      make(map[string]*Client),
		userID:       userID,
		oauthManager: oauthManager,
	}
}

// ConnectToAllServers initializes connections to all provided servers
func (c *UserClients) ConnectToAllServers(servers []ServerConfig) *Errors {
	if len(servers) == 0 {
		c.log.Debug("No MCP servers provided for user", "userID", c.userID)
		return nil
	}

	var mcpErrors *Errors

	// Initialize clients for each server
	for _, serverConfig := range servers {
		if serverConfig.BaseURL == "" {
			c.log.Warn("Skipping MCP server with empty BaseURL", "serverID", serverConfig.Name)
			continue
		}

		if err := c.connectToServer(context.TODO(), serverConfig.Name, serverConfig); err != nil {
			// Initialize errors struct if needed
			if mcpErrors == nil {
				mcpErrors = &Errors{}
			}

			// Check if this is an OAuth authentication error
			var oauthErr *OAuthNeededError
			if errors.As(err, &oauthErr) {
				mcpErrors.ToolAuthErrors = append(mcpErrors.ToolAuthErrors, llm.ToolAuthError{
					ServerName: serverConfig.Name,
					AuthURL:    oauthErr.AuthURL(),
					Error:      err,
				})
			} else {
				c.log.Error("Failed to connect to MCP server", "userID", c.userID, "serverID", serverConfig.Name, "error", err)
				mcpErrors.Errors = append(mcpErrors.Errors, err)
			}
			continue
		}
	}

	return mcpErrors
}

// connectToServer establishes a connection to a single server
func (c *UserClients) connectToServer(ctx context.Context, serverID string, serverConfig ServerConfig) error {
	serverClient, err := NewClient(ctx, c.userID, serverConfig, c.log, c.oauthManager)
	if err != nil {
		return err
	}
	c.clients[serverID] = serverClient
	return nil
}

// Close closes all server connections for a user client
func (c *UserClients) Close() {
	if len(c.clients) == 0 {
		return
	}

	// Close all MCP server clients
	for serverID, client := range c.clients {
		if err := client.Close(); err != nil {
			c.log.Error("Failed to close MCP client", "userID", c.userID, "serverID", serverID, "error", err)
		}
	}

	// Clear clients
	c.clients = make(map[string]*Client)
}

// GetTools returns the tools available from the clients
func (c *UserClients) GetTools() []llm.Tool {
	if len(c.clients) == 0 {
		return nil
	}

	var tools []llm.Tool
	seenTools := make(map[string]string) // toolName -> serverID for conflict detection

	// Iterate over all clients and collect their tools
	for serverID, client := range c.clients {
		clientTools := client.Tools()
		for toolName, tool := range clientTools {
			// Check for tool name conflicts across servers
			if existingServerID, exists := seenTools[toolName]; exists {
				c.log.Warn("Tool name conflict detected",
					"userID", c.userID,
					"tool", toolName,
					"server1", existingServerID,
					"server2", serverID)
				// Skip duplicate tool (first server wins)
				continue
			}
			seenTools[toolName] = serverID

			tools = append(tools, llm.Tool{
				Name:        toolName,
				Description: tool.Description,
				Schema:      tool.InputSchema,
				Resolver:    c.createToolResolver(client, toolName),
			})
		}
	}

	return tools
}

// createToolResolver creates a resolver function for the given tool
func (c *UserClients) createToolResolver(client *Client, toolName string) func(llmContext *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
	return func(llmContext *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
		var args map[string]any
		if err := argsGetter(&args); err != nil {
			return "", fmt.Errorf("failed to get arguments for tool %s: %w", toolName, err)
		}

		return client.CallTool(context.Background(), toolName, args)
	}
}
