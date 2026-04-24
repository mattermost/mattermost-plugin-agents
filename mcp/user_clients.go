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

// pluginServerOriginKey is the synthetic origin string used for plugin-server
// tools. Must match the key used by filterToolsByConfig when building
// synthetic ServerConfig entries (mcp/client_manager.go).
func pluginServerOriginKey(pluginID string) string {
	return "plugin://" + pluginID
}

// ConnectToPluginServer establishes an MCP client session with a source
// Mattermost plugin's MCP endpoint (registered via the bridge
// /mcp/register endpoint, served by public/mcphelper.Server.ServeHTTP).
// It mirrors the shape of ConnectToRemoteServers at mcp/user_clients.go:51-90
// in the reverse direction:
//
//   - Uses PluginHTTPRoundTripper (mcp/plugin_roundtripper.go) instead of the
//     MCP package's oauth-aware http client.
//   - Layers headerTransport (mcp/http_client.go:9-24) to inject
//     X-Mattermost-UserID on every outbound request.
//   - Keys the resulting cached *Client by pluginServerOriginKey(cfg.PluginID)
//     so tool lookups and filterToolsByConfig line up.
//
// Behavior:
//   - Idempotent: if c.clients already has an entry for the origin key, returns nil.
//   - On zero-tools response: closes the session and returns an error (matches
//     mcp/client.go:244-247).
//   - On connect or list-tools failure: returns a wrapped error; caller
//     (ClientManager.GetToolsForUser) appends to mcpErrors.Errors.
//
// OAuth-error triage is NOT performed — plugin-registered servers authenticate
// via Mattermost's inter-plugin HTTP (X-Mattermost-UserID over PluginHTTP),
// not user OAuth. If a future plugin chooses to wire OAuth behind its
// mcphelper.Server, errors will surface as generic .Errors rather than
// .ToolAuthErrors; Phase 3 can revisit.
func (c *UserClients) ConnectToPluginServer(ctx context.Context, cfg PluginServerConfig, sourcePluginAPI mmapi.Client) error {
	if sourcePluginAPI == nil {
		return fmt.Errorf("sourcePluginAPI is nil; plugin MCP server %s cannot be reached", cfg.PluginID)
	}

	originKey := pluginServerOriginKey(cfg.PluginID)
	// Idempotent: skip reconnect if we already have a live session.
	if _, exists := c.clients[originKey]; exists {
		return nil
	}

	// Build the transport chain: PluginHTTPRoundTripper (URL rewrite) ->
	// headerTransport (X-Mattermost-UserID injection) -> http.Client.
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

	// Connect via go-sdk MCP Streamable HTTP transport. Endpoint URL is a
	// placeholder; PluginHTTPRoundTripper rewrites req.URL.Path before each
	// round trip. The scheme/host must parse to a valid URL for go-sdk.
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

	// Discover tools. Zero-tool responses and list errors close the session
	// and surface as errors — matches mcp/client.go:244-247.
	initResult, err := session.ListTools(ctx, &gosdkmcp.ListToolsParams{})
	if err != nil {
		_ = session.Close()
		return fmt.Errorf("failed to list tools on plugin MCP server %s: %w", cfg.PluginID, err)
	}
	if len(initResult.Tools) == 0 {
		_ = session.Close()
		return fmt.Errorf("no tools found on plugin MCP server %s for user %s", cfg.PluginID, c.userID)
	}

	// Synthetic ServerConfig for the plugin server. BaseURL == originKey is the
	// link to filterToolsByConfig's synthetic entry; GetTools propagates
	// client.config.BaseURL as llm.Tool.ServerOrigin (mcp/user_clients.go:180).
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
		// oauthManager intentionally nil: plugin servers don't use user OAuth.
		// embeddedClient intentionally nil: reconnect-on-ErrConnectionClosed
		// falls through to createSession (mcp/client.go:449-481) which will
		// fail for "plugin://" URLs. Phase 3-3 adds a proper reconnect path;
		// for now, the first reconnect attempt surfaces a clean error to the
		// LLM rather than panicking.
	}
	for _, tool := range initResult.Tools {
		client.tools[tool.Name] = tool
	}

	c.clients[originKey] = client
	c.log.Debug("Connected to plugin MCP server", "userID", c.userID, "pluginID", cfg.PluginID, "toolCount", len(client.tools))
	return nil
}
