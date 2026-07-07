// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
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
	clientsMu    sync.RWMutex
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

type userClientSnapshot struct {
	serverID string
	client   *Client
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

// ConnectToRemoteServers initializes connections to remote MCP servers.
func (c *UserClients) ConnectToRemoteServers(ctx context.Context, servers []ServerConfig, forceRefresh bool) *Errors {
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

		if err := c.connectToServer(ctx, serverConfig.Name, serverConfig, forceRefresh); err != nil {
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
// If a connection already exists, it is reused.
func (c *UserClients) ConnectToEmbeddedServerIfAvailable(ctx context.Context, sessionID string, embeddedClient *EmbeddedServerClient, embeddedConfig EmbeddedServerConfig) error {
	if !embeddedConfig.Enabled || embeddedClient == nil {
		return nil
	}

	if c.hasClient(EmbeddedClientKey) {
		return nil
	}

	if sessionID == "" {
		return nil
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	serverClient, err := embeddedClient.CreateClient(ctxWithTimeout, c.userID, sessionID)
	if err != nil {
		c.log.Error("Failed to connect to embedded MCP server", "userID", c.userID, "error", err)
		return fmt.Errorf("failed to connect to embedded server: %w", err)
	}

	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	if _, exists := c.clients[EmbeddedClientKey]; exists {
		_ = serverClient.Close()
		return nil
	}

	c.clients[EmbeddedClientKey] = serverClient
	c.log.Debug("Successfully connected to embedded MCP server", "userID", c.userID)

	return nil
}

// connectToServer establishes a connection to a single server
func (c *UserClients) connectToServer(ctx context.Context, serverID string, serverConfig ServerConfig, forceRefresh bool) error {
	serverClient, err := NewClient(ctx, c.userID, serverConfig, c.log, c.oauthManager, c.httpClient, c.toolsCache, forceRefresh)
	if err != nil {
		return err
	}
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	c.clients[serverID] = serverClient
	return nil
}

func (c *UserClients) hasClient(serverID string) bool {
	c.clientsMu.RLock()
	defer c.clientsMu.RUnlock()
	_, exists := c.clients[serverID]
	return exists
}

func (c *UserClients) snapshotClients() []userClientSnapshot {
	c.clientsMu.RLock()
	defer c.clientsMu.RUnlock()
	if len(c.clients) == 0 {
		return nil
	}

	serverIDs := make([]string, 0, len(c.clients))
	for serverID := range c.clients {
		serverIDs = append(serverIDs, serverID)
	}
	sort.Strings(serverIDs)

	snapshot := make([]userClientSnapshot, 0, len(serverIDs))
	for _, serverID := range serverIDs {
		snapshot = append(snapshot, userClientSnapshot{
			serverID: serverID,
			client:   c.clients[serverID],
		})
	}
	return snapshot
}

func (c *UserClients) InitialRemoteConnectErrors() *Errors {
	c.clientsMu.RLock()
	defer c.clientsMu.RUnlock()
	return c.initialRemoteConnectErrors
}

func (c *UserClients) setInitialRemoteConnectErrors(mcpErrors *Errors) {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	c.initialRemoteConnectErrors = mcpErrors
}

// Close closes all server connections for a user client
func (c *UserClients) Close() {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

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
func (c *UserClients) GetTools(ctx context.Context) []llm.Tool {
	clientSnapshot := c.snapshotClients()
	if len(clientSnapshot) == 0 {
		return nil
	}

	var tools []llm.Tool
	seenTools := make(map[string]string) // runtime toolName -> serverID for conflict detection
	usedSlugs := make(map[string]string) // slug -> server origin for collision suffixing

	// Iterate over a snapshot so callers do not hold clientsMu during network work.
	for _, entry := range clientSnapshot {
		serverID := entry.serverID
		client := entry.client
		clientTools := client.Tools()
		serverSlug := dedupeMCPServerSlug(mcpServerSlug(serverID, client), client.config.BaseURL, serverID, usedSlugs)
		toolNames := make([]string, 0, len(clientTools))
		for toolName := range clientTools {
			toolNames = append(toolNames, toolName)
		}
		sort.Strings(toolNames)
		for _, toolName := range toolNames {
			tool := clientTools[toolName]
			runtimeToolName := llm.NamespaceMCPToolName(serverSlug, toolName)
			// Namespacing should make cross-server duplicate bare names safe. A
			// final collision means the slug de-dupe or upstream catalog is broken.
			if existingServerID, exists := seenTools[runtimeToolName]; exists {
				c.log.Warn("Namespaced MCP tool name conflict detected",
					"userID", c.userID,
					"tool", runtimeToolName,
					"server1", existingServerID,
					"server2", serverID)
				continue
			}
			seenTools[runtimeToolName] = serverID

			tools = append(tools, llm.Tool{
				Name:         runtimeToolName,
				Description:  tool.Description,
				Schema:       tool.InputSchema,
				Resolver:     c.createToolResolver(client, toolName),
				ServerOrigin: client.config.BaseURL,
			})
		}
	}

	return tools
}

// prepareToolCallMetadata prepares metadata to be sent with MCP tool calls.
// Per-call metadata is sourced from the tool itself (set at scope-time via
// llm.Tool.WithCallMetadata) so callers can plumb runtime info — like before-hook
// keys — without leaking it into the LLM-visible schema or onto llm.Context.
// bot_user_id is sourced from llm.Context because it is identity, not per-call config.
func (c *UserClients) prepareToolCallMetadata(client *Client, toolName string, llmContext *llm.Context) map[string]any {
	if llmContext == nil {
		return nil
	}

	// Only inject metadata for the embedded server.
	if client.config.Name != EmbeddedClientKey {
		return nil
	}

	var metadata map[string]any
	if llmContext.Tools != nil {
		if tool := llmContext.Tools.GetTool(toolName); tool != nil && len(tool.CallMetadata) > 0 {
			metadata = make(map[string]any, len(tool.CallMetadata)+1)
			for k, v := range tool.CallMetadata {
				metadata[k] = v
			}
		}
	}

	if llmContext.BotUserID != "" {
		if metadata == nil {
			metadata = make(map[string]any, 1)
		}
		metadata["bot_user_id"] = llmContext.BotUserID
	}

	return metadata
}

func (c *UserClients) clearOAuthNeededForServer(client *Client) {
	if c.oauthManager == nil || client == nil || client.config.Name == "" {
		return
	}
	if err := c.oauthManager.DeleteAuthNeededState(c.userID, client.config.Name); err != nil {
		c.log.Debug("Failed to clear MCP OAuth-needed state after successful tool call",
			"userID", c.userID,
			"serverID", client.config.Name,
			"error", err)
	}
}

func (c *UserClients) rememberOAuthNeededForToolCall(client *Client, err error) {
	if c.oauthManager == nil || client == nil || client.config.Name == "" || err == nil {
		return
	}

	oauthErr := client.oauthNeededError(err)
	if oauthErr == nil {
		return
	}

	var needed *OAuthNeededError
	if !errors.As(oauthErr, &needed) {
		return
	}

	authURL := needed.AuthURL()
	if authURL == "" {
		authURL = c.oauthManager.StartURL(client.config.Name)
	}
	if authURL == "" {
		return
	}

	if storeErr := c.oauthManager.StoreAuthNeededState(c.userID, client.config.Name, authURL); storeErr != nil {
		c.log.Warn("Failed to persist MCP OAuth-needed state after tool call",
			"userID", c.userID,
			"serverID", client.config.Name,
			"error", storeErr)
	}
}

// createToolResolver creates a resolver function for the given tool
func (c *UserClients) createToolResolver(client *Client, toolName string) llm.ToolResolver {
	return func(ctx context.Context, llmContext *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
		var args map[string]any
		if err := argsGetter(&args); err != nil {
			return "", fmt.Errorf("failed to get arguments for tool %s: %w", toolName, err)
		}

		metadata := c.prepareToolCallMetadata(client, toolName, llmContext)

		result, err := client.CallToolWithMetadata(ctx, toolName, args, metadata)
		if err != nil {
			c.rememberOAuthNeededForToolCall(client, err)
			return result, err
		}

		c.clearOAuthNeededForServer(client)
		return result, nil
	}
}

func mcpServerSlug(serverID string, client *Client) string {
	if client != nil && (client.config.BaseURL == EmbeddedClientKey || client.config.Name == EmbeddedClientKey || serverID == EmbeddedClientKey) {
		return "mattermost"
	}

	candidates := []string{}
	if client != nil {
		candidates = append(candidates, client.config.Name)
	}
	candidates = append(candidates, serverID)
	if client != nil && client.config.BaseURL != "" {
		if parsed, err := url.Parse(client.config.BaseURL); err == nil {
			baseURLName := strings.Trim(strings.Trim(parsed.Host+parsed.Path, "/"), "_")
			candidates = append(candidates, baseURLName)
		}
	}
	candidates = append(candidates, "mcp")

	for _, candidate := range candidates {
		if slug := sanitizeMCPServerSlug(candidate); slug != "" {
			return slug
		}
	}
	return "mcp"
}

func dedupeMCPServerSlug(slug, serverOrigin, serverID string, usedSlugs map[string]string) string {
	if slug == "" {
		slug = "mcp"
	}
	if existingOrigin, exists := usedSlugs[slug]; !exists || existingOrigin == serverOrigin {
		usedSlugs[slug] = serverOrigin
		return slug
	}

	hashInput := serverOrigin
	if hashInput == "" {
		hashInput = serverID
	}
	if hashInput == "" {
		hashInput = slug
	}
	dedupedSlug := slug + "_" + shortSlugHash(hashInput)
	usedSlugs[dedupedSlug] = serverOrigin
	return dedupedSlug
}

func sanitizeMCPServerSlug(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastWasSeparator := false
	for _, r := range value {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAllowed {
			b.WriteRune(r)
			lastWasSeparator = false
			continue
		}
		if b.Len() > 0 && !lastWasSeparator {
			b.WriteByte('_')
			lastWasSeparator = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func shortSlugHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
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
	originKey := pluginServerOriginKey(cfg.PluginID)
	if c.hasClient(originKey) {
		return nil
	}

	client, err := NewPluginClient(ctx, c.userID, cfg, sourcePluginAPI, c.log)
	if err != nil {
		return err
	}

	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	if _, exists := c.clients[originKey]; exists {
		_ = client.Close()
		return nil
	}

	c.clients[originKey] = client
	c.log.Debug("Connected to plugin MCP server", "userID", c.userID, "pluginID", cfg.PluginID, "toolCount", len(client.Tools()))
	return nil
}
