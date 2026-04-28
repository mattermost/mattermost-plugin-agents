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
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolInfo represents a tool's metadata for discovery purposes
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// UserClients represents a per-user MCP client with multiple server connections
type UserClients struct {
	clients      map[string]*Client // serverID -> client (both remote and embedded)
	userID       string
	log          pluginapi.LogService
	oauthManager *OAuthManager
	httpClient   *http.Client
	toolsCache   *ToolsCache
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

// ConnectToEmbeddedServerIfAvailable connects to the embedded server if session ID is provided
func (c *UserClients) ConnectToEmbeddedServerIfAvailable(sessionID string, embeddedClient *EmbeddedServerClient, embeddedConfig EmbeddedServerConfig) error {
	if !embeddedConfig.Enabled || embeddedClient == nil {
		return nil
	}

	// Check if we already have an embedded server connection
	if _, exists := c.clients[EmbeddedClientKey]; exists {
		return nil // Already connected
	}

	// Connect if session ID is provided
	if sessionID != "" {
		ctxWithTimeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.connectToEmbeddedServerWithClient(ctxWithTimeout, c.userID, sessionID, embeddedClient); err != nil {
			c.log.Error("Failed to connect to embedded MCP server", "userID", c.userID, "error", err)
			return fmt.Errorf("failed to connect to embedded server: %w", err)
		}
		c.log.Debug("Successfully connected to embedded MCP server", "userID", c.userID)
	}

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
func (c *UserClients) connectToEmbeddedServerWithClient(ctx context.Context, userID, sessionID string, embeddedClient *EmbeddedServerClient) error {
	serverClient, err := embeddedClient.CreateClient(ctx, userID, sessionID)
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

// pluginServerOriginKey returns the synthetic origin string for plugin-server
// tools. Must match the key used by filterToolsByConfig.
func pluginServerOriginKey(pluginID string) string {
	return "plugin://" + pluginID
}

// ConnectToPluginServer establishes a cached MCP session with a source plugin
// over PluginHTTP, injecting X-Mattermost-UserID. Plugin servers use
// inter-plugin auth, not user OAuth.
func (c *UserClients) ConnectToPluginServer(ctx context.Context, cfg PluginServerConfig, sourcePluginAPI mmapi.Client) error {
	if sourcePluginAPI == nil {
		return fmt.Errorf("sourcePluginAPI is nil; plugin MCP server %s cannot be reached", cfg.PluginID)
	}

	originKey := pluginServerOriginKey(cfg.PluginID)
	if _, exists := c.clients[originKey]; exists {
		return nil
	}

	roundTripper := &PluginHTTPRoundTripper{
		pluginID:  cfg.PluginID,
		basePath:  cfg.Path,
		pluginAPI: sourcePluginAPI,
	}
	httpClient := &http.Client{
		Transport: &headerTransport{
			base:    roundTripper,
			headers: map[string]string{MMUserIDHeader: c.userID},
		},
	}

	// Endpoint URL is a placeholder — PluginHTTPRoundTripper rewrites
	// req.URL.Path on each round trip. go-sdk requires a parseable URL.
	mcpClient := gosdkmcp.NewClient(
		&gosdkmcp.Implementation{
			Name:    "mattermost-agents-plugin-bridge",
			Version: "1.0",
		},
		&gosdkmcp.ClientOptions{},
	)
	session, err := mcpClient.Connect(ctx, &gosdkmcp.StreamableClientTransport{
		Endpoint:   "http://plugin" + cfg.Path,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to plugin MCP server %s: %w", cfg.PluginID, err)
	}

	initResult, err := session.ListTools(ctx, &gosdkmcp.ListToolsParams{})
	if err != nil {
		_ = session.Close()
		return fmt.Errorf("failed to list tools on plugin MCP server %s: %w", cfg.PluginID, err)
	}
	if len(initResult.Tools) == 0 {
		_ = session.Close()
		return fmt.Errorf("no tools found on plugin MCP server %s for user %s", cfg.PluginID, c.userID)
	}

	// Synthetic ServerConfig: BaseURL == originKey ties the client into
	// filterToolsByConfig via llm.Tool.ServerOrigin in GetTools.
	pluginCfg := ServerConfig{
		Name:    cfg.Name,
		Enabled: true,
		BaseURL: originKey,
	}

	client := &Client{
		session:    session,
		config:     pluginCfg,
		tools:      make(map[string]*gosdkmcp.Tool, len(initResult.Tools)),
		userID:     c.userID,
		log:        c.log,
		httpClient: httpClient,
		// oauthManager/embeddedClient stay nil; reconnect reuses httpClient.
	}
	for _, tool := range initResult.Tools {
		client.tools[tool.Name] = tool
	}

	c.clients[originKey] = client
	c.log.Debug("Connected to plugin MCP server", "userID", c.userID, "pluginID", cfg.PluginID, "toolCount", len(client.tools))
	return nil
}
