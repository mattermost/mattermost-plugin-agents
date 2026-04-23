// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// ToolInfo represents a tool's metadata for discovery purposes
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// UserClients represents a per-user MCP client with multiple server connections
type UserClients struct {
	clients               map[string]*Client // serverID -> client (both remote and embedded)
	userID                string
	log                   pluginapi.LogService
	oauthManager          *OAuthManager
	httpClient            *http.Client
	toolsCache            *ToolsCache
	lastEmbeddedChannelID string // channel id the embedded client was created for ("" = nil channel)
	// initialRemoteConnectErrors holds OAuth / connect failures from the first
	// ConnectToRemoteServers. It must be re-returned on every lookup while this
	// user client is cached; otherwise callers only see those errors once (first
	// GetToolsForUser) and lose stable auth-required state on subsequent requests.
	initialRemoteConnectErrors *Errors
}

func NewUserClients(userID string, log pluginapi.LogService, oauthManager *OAuthManager, httpClient *http.Client, toolsCache *ToolsCache) *UserClients {
	return &UserClients{
		log:          log,
		clients:      make(map[string]*Client),
		userID:       userID,
		oauthManager: oauthManager,
		httpClient:   httpClient,
		toolsCache:   toolsCache,
	}
}

// ConnectToRemoteServers initializes connections to remote MCP servers
func (c *UserClients) ConnectToRemoteServers(servers []ServerConfig) *Errors {
	if len(servers) == 0 {
		c.log.Debug("No remote MCP servers provided for user", "userID", c.userID)
		return nil
	}

	var mcpErrors *Errors

	// Connect to remote servers
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
					ServerName:   serverConfig.Name,
					ServerOrigin: serverConfig.BaseURL,
					AuthURL:      oauthErr.AuthURL(),
					Error:        err,
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

// ConnectToEmbeddedServerIfAvailable connects to the embedded server if session ID is provided.
// When the channel changes from the one the embedded client was created for, the existing
// connection is closed and a new one is created so the server middleware sees the new channel.
func (c *UserClients) ConnectToEmbeddedServerIfAvailable(sessionID string, embeddedClient *EmbeddedServerClient, embeddedConfig EmbeddedServerConfig, channel *model.Channel) error {
	if !embeddedConfig.Enabled || embeddedClient == nil {
		return nil
	}

	channelID := ""
	if channel != nil {
		channelID = channel.Id
	}

	if existing, exists := c.clients[EmbeddedClientKey]; exists {
		if c.lastEmbeddedChannelID == channelID {
			return nil
		}
		// Channel changed — tear down old connection so we get a fresh tool list
		if err := existing.Close(); err != nil {
			c.log.Debug("Failed to close embedded MCP client for channel change", "userID", c.userID, "error", err)
		}
		delete(c.clients, EmbeddedClientKey)
	}

	if sessionID == "" {
		return nil
	}

	ctxWithTimeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.connectToEmbeddedServerWithClient(ctxWithTimeout, c.userID, sessionID, embeddedClient, channel); err != nil {
		c.log.Error("Failed to connect to embedded MCP server", "userID", c.userID, "error", err)
		return fmt.Errorf("failed to connect to embedded server: %w", err)
	}
	c.lastEmbeddedChannelID = channelID
	c.log.Debug("Successfully connected to embedded MCP server", "userID", c.userID, "channelID", channelID)

	return nil
}

// connectToServer establishes a connection to a single server
func (c *UserClients) connectToServer(ctx context.Context, serverID string, serverConfig ServerConfig) error {
	serverClient, err := NewClient(ctx, c.userID, serverConfig, c.log, c.oauthManager, c.httpClient, c.toolsCache)
	if err != nil {
		return err
	}
	c.clients[serverID] = serverClient
	return nil
}

// connectToEmbeddedServerWithClient establishes a connection to the embedded server using the embedded client helper
func (c *UserClients) connectToEmbeddedServerWithClient(ctx context.Context, userID, sessionID string, embeddedClient *EmbeddedServerClient, channel *model.Channel) error {
	serverClient, err := embeddedClient.CreateClient(ctx, userID, sessionID, channel)
	if err != nil {
		return err
	}
	c.clients[EmbeddedClientKey] = serverClient
	return nil
}

// Close closes all server connections for a user client
func (c *UserClients) Close() {
	// Close all MCP server clients (both remote and embedded)
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
				Name:         toolName,
				Description:  tool.Description,
				Schema:       tool.InputSchema,
				Resolver:     c.createToolResolver(client, toolName),
				ServerOrigin: client.config.BaseURL,
			})
		}
	}

	return tools
}

// prepareToolCallMetadata prepares metadata to be sent with MCP tool calls
// This is where we inject context-specific information that tools need but shouldn't be in arguments
func (c *UserClients) prepareToolCallMetadata(client *Client, llmContext *llm.Context) map[string]any {
	// Only add metadata if we have a valid context
	if llmContext == nil {
		return nil
	}

	var metadata map[string]any

	// For embedded server, inject Bot UserID for AI-generated content tracking
	if client.config.Name == EmbeddedClientKey && llmContext.BotUserID != "" {
		metadata = make(map[string]any)
		metadata["bot_user_id"] = llmContext.BotUserID
	}

	return metadata
}

// createToolResolver creates a resolver function for the given tool
func (c *UserClients) createToolResolver(client *Client, toolName string) func(llmContext *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
	return func(llmContext *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
		var args map[string]any
		if err := argsGetter(&args); err != nil {
			return "", fmt.Errorf("failed to get arguments for tool %s: %w", toolName, err)
		}

		// Prepare metadata for the tool call
		metadata := c.prepareToolCallMetadata(client, llmContext)

		return client.CallToolWithMetadata(context.Background(), toolName, args, metadata)
	}
}
