// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	MMUserIDHeader     = "X-Mattermost-UserID"
	EmbeddedServerName = "Mattermost"
	EmbeddedClientKey  = "embedded://mattermost"

	ToolPolicyAsk               = config.MCPToolPolicyAsk
	ToolPolicyAutoRunInDM       = config.MCPToolPolicyAutoRunInDM
	ToolPolicyAutoRunEverywhere = config.MCPToolPolicyAutoRunEverywhere
)

func IsToolPolicyAutoRunInDM(policy string) bool {
	return config.IsToolPolicyAutoRunInDM(policy)
}

func IsToolPolicyAutoRunEverywhere(policy string) bool {
	return config.IsToolPolicyAutoRunEverywhere(policy)
}

// EmbeddedMCPServer interface for dependency injection
type EmbeddedMCPServer interface {
	CreateClientTransport(userID, sessionID string, pluginAPI *pluginapi.Client) (*mcp.InMemoryTransport, error)
}

// EmbeddedServerClient handles connections to the embedded MCP server
type EmbeddedServerClient struct {
	server     EmbeddedMCPServer
	log        pluginapi.LogService
	pluginAPI  *pluginapi.Client
	toolsCache *ToolsCache
}

// Client represents the connection to a single MCP server
type Client struct {
	session        *mcp.ClientSession
	config         ServerConfig
	toolsMu        sync.RWMutex
	tools          map[string]*mcp.Tool
	userID         string
	log            pluginapi.LogService
	oauthManager   *OAuthManager
	httpClient     *http.Client
	toolsCache     *ToolsCache
	embeddedClient *EmbeddedServerClient // for reconnection (nil for remote servers)
	sessionID      string                // session ID for embedded server reconnection
}

// staticOAuthCreds returns static OAuth credentials from a server config, or nil if not configured.
func staticOAuthCreds(s ServerConfig) *StaticOAuthCredentials {
	if s.ClientID == "" {
		return nil
	}
	return &StaticOAuthCredentials{
		ClientID:     s.ClientID,
		ClientSecret: s.ClientSecret,
	}
}

func shouldUseSharedToolsCache(serverConfig ServerConfig) bool {
	return staticOAuthCreds(serverConfig) == nil
}

func invalidateSharedToolsCacheForOAuthDiscovery(toolsCache *ToolsCache, log Logger, userID, serverID string, serverConfig ServerConfig, hasStoredToken bool) {
	if toolsCache == nil || hasStoredToken {
		return
	}

	if err := toolsCache.InvalidateServer(serverID); err != nil {
		log.Warn("Failed to invalidate shared tools cache for OAuth-backed MCP server",
			"serverID", serverID,
			"server", serverConfig.Name,
			"userID", userID,
			"error", err)
	}
}

// maybeInvalidateSharedToolsBeforeOAuthListTools drops any shared-cache tool list for this
// server when the MCP server uses OAuth and the user has not completed OAuth yet. That avoids
// ListTools reusing tools discovered before authentication (shared cache is only for non-OAuth servers).
func maybeInvalidateSharedToolsBeforeOAuthListTools(userID string, serverConfig ServerConfig, log pluginapi.LogService, toolsCache *ToolsCache, oauthManager *OAuthManager) {
	if shouldUseSharedToolsCache(serverConfig) || toolsCache == nil || oauthManager == nil {
		return
	}

	serverID := serverConfig.Name
	hasStoredToken, tokenErr := oauthManager.HasStoredToken(userID, serverID)
	if tokenErr != nil {
		log.Warn("Failed to check stored OAuth token before MCP tool discovery",
			"serverID", serverID,
			"server", serverConfig.Name,
			"userID", userID,
			"error", tokenErr)
		return
	}
	invalidateSharedToolsCacheForOAuthDiscovery(toolsCache, &log, userID, serverID, serverConfig, hasStoredToken)
}

func NewEmbeddedServerClient(server EmbeddedMCPServer, log pluginapi.LogService, pluginAPI *pluginapi.Client) *EmbeddedServerClient {
	return &EmbeddedServerClient{
		server:    server,
		log:       log,
		pluginAPI: pluginAPI,
	}
}

// NewEmbeddedServerClientWithCache is the same as NewEmbeddedServerClient but
// also wires up a shared tools cache. Pass a non-nil cache when callers want
// per-user tool listings to be cached across requests.
func NewEmbeddedServerClientWithCache(server EmbeddedMCPServer, log pluginapi.LogService, pluginAPI *pluginapi.Client, toolsCache *ToolsCache) *EmbeddedServerClient {
	client := NewEmbeddedServerClient(server, log, pluginAPI)
	client.toolsCache = toolsCache
	return client
}

func listAllTools(ctx context.Context, session *mcp.ClientSession) (map[string]*mcp.Tool, error) {
	tools := make(map[string]*mcp.Tool)
	for tool, err := range session.Tools(ctx, &mcp.ListToolsParams{}) {
		if err != nil {
			return nil, err
		}
		if tool == nil {
			continue
		}
		tools[tool.Name] = tool
	}
	return tools, nil
}

// CreateClient creates an embedded MCP client using session ID for authentication.
// If sessionID is empty, creates an unauthenticated client (used for tool discovery).
func (c *EmbeddedServerClient) CreateClient(ctx context.Context, userID, sessionID string) (*Client, error) {
	// Validate session exists before creating transport (unless empty for tool discovery)
	if sessionID != "" {
		if c.pluginAPI == nil {
			return nil, fmt.Errorf("plugin API is required when sessionID is provided")
		}
		mmSession, err := c.pluginAPI.Session.Get(sessionID)
		if err != nil {
			return nil, fmt.Errorf("failed to get session: %w", err)
		}
		if mmSession == nil {
			return nil, fmt.Errorf("session not found")
		}
		if mmSession.UserId != userID {
			return nil, fmt.Errorf("session user ID does not match: expected %s, got %s", userID, mmSession.UserId)
		}
	}

	// Get the in-memory transport from the embedded server
	transport, err := c.server.CreateClientTransport(userID, sessionID, c.pluginAPI)
	if err != nil {
		return nil, fmt.Errorf("failed to create in-memory transport: %w", err)
	}

	// Create MCP client
	mcpClient := mcp.NewClient(
		&mcp.Implementation{
			Name:    "mattermost-agents-embedded",
			Version: "1.0",
		},
		nil,
	)

	// Connect to the embedded server using in-memory transport
	mcpSession, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to embedded MCP server: %w", err)
	}

	// Create client instance
	client := &Client{
		session:        mcpSession,
		config:         ServerConfig{Name: EmbeddedClientKey, BaseURL: EmbeddedClientKey, Enabled: true},
		tools:          make(map[string]*mcp.Tool),
		userID:         userID,
		log:            c.log,
		oauthManager:   nil, // Embedded servers don't use OAuth
		toolsCache:     c.toolsCache,
		embeddedClient: c,         // Store client helper for reconnection
		sessionID:      sessionID, // Store session ID for reconnection
	}
	// Initialize tools
	discoveredTools, err := listAllTools(ctx, mcpSession)
	if err != nil {
		mcpSession.Close()
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	if len(discoveredTools) == 0 {
		mcpSession.Close()
		return nil, fmt.Errorf("no tools found on MCP server %s for user %s", EmbeddedClientKey, userID)
	}

	// Store the tools for this server
	client.toolsMu.Lock()
	client.tools = discoveredTools
	client.toolsMu.Unlock()
	for _, tool := range discoveredTools {
		c.log.Debug("Registered MCP tool",
			"userID", userID,
			"name", tool.Name,
			"description", tool.Description,
			"server", EmbeddedClientKey)
	}

	c.log.Debug("Successfully connected to embedded MCP server",
		"userID", userID,
		"server", EmbeddedClientKey)

	return client, nil
}

// NewClient creates a new MCP client for the given server and user and connects to the specified MCP server.
// forceRefresh bypasses the shared tools cache read. Its sole purpose is to close the race where a concurrent
// lookup repopulates the cache between a manual refresh's invalidation and this reconnect; a plain
// post-invalidation rediscovery would otherwise cache-miss on its own.
func NewClient(ctx context.Context, userID string, serverConfig ServerConfig, log pluginapi.LogService, oauthManager *OAuthManager, httpClient *http.Client, toolsCache *ToolsCache, forceRefresh bool) (*Client, error) {
	c := &Client{
		session:      nil,
		config:       serverConfig,
		tools:        make(map[string]*mcp.Tool),
		userID:       userID,
		log:          log,
		oauthManager: oauthManager,
		httpClient:   httpClient,
		toolsCache:   toolsCache,
	}

	session, err := c.createSession(ctx, serverConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP session for server %s: %w", serverConfig.Name, err)
	}

	useSharedToolsCache := shouldUseSharedToolsCache(serverConfig)
	maybeInvalidateSharedToolsBeforeOAuthListTools(userID, serverConfig, log, toolsCache, oauthManager)
	serverID := serverConfig.Name

	// Try to get tools from global cache first.
	if toolsCache != nil && useSharedToolsCache && !forceRefresh {
		cachedTools := toolsCache.GetTools(serverID)
		if len(cachedTools) > 0 {
			// Cache hit - use cached tools
			c.toolsMu.Lock()
			c.tools = cachedTools
			c.toolsMu.Unlock()
			log.Debug("Using cached tools for MCP server",
				"userID", userID,
				"server", serverConfig.Name,
				"toolCount", len(cachedTools))
			c.session = session
			return c, nil
		}
	}

	// Cache miss - fetch tools from server
	discoveredTools, err := listAllTools(ctx, session)
	if err != nil {
		session.Close()
		if oauthErr := c.oauthNeededError(err); oauthErr != nil {
			return nil, oauthErr
		}
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	if len(discoveredTools) == 0 {
		session.Close()
		return nil, fmt.Errorf("no tools found on MCP server %s for user %s", serverConfig.Name, userID)
	}

	// Store the tools for this server
	c.toolsMu.Lock()
	c.tools = discoveredTools
	c.toolsMu.Unlock()
	for _, tool := range discoveredTools {
		log.Debug("Registered MCP tool",
			"userID", userID,
			"name", tool.Name,
			"description", tool.Description,
			"server", serverConfig.Name)
	}

	// Update the global cache with fetched tools.
	if toolsCache != nil && useSharedToolsCache {
		if err := toolsCache.SetTools(serverID, serverConfig.Name, serverConfig.BaseURL, discoveredTools, time.Now()); err != nil {
			log.Warn("Failed to update tools cache", "server", serverConfig.Name, "error", err)
		}
	}

	c.session = session
	return c, nil
}

// NewPluginClient creates a per-user MCP client for a plugin-registered server.
// Plugin clients list tools at connect time and do not use the shared tools cache.
func NewPluginClient(ctx context.Context, userID string, cfg PluginServerConfig, sourcePluginAPI mmapi.Client, log pluginapi.LogService) (*Client, error) {
	if sourcePluginAPI == nil {
		return nil, fmt.Errorf("sourcePluginAPI is nil; plugin MCP server %s cannot be reached", cfg.PluginID)
	}

	originKey := pluginServerOriginKey(cfg.PluginID)
	roundTripper := NewPluginHTTPRoundTripper(cfg.PluginID, cfg.Path, sourcePluginAPI)
	httpClient := &http.Client{
		Transport: &headerTransport{
			base:    roundTripper,
			headers: map[string]string{MMUserIDHeader: userID},
		},
	}

	pluginCfg := ServerConfig{
		Name:    cfg.Name,
		Enabled: true,
		BaseURL: originKey,
	}

	client := &Client{
		config:     pluginCfg,
		tools:      make(map[string]*mcp.Tool),
		userID:     userID,
		log:        log,
		httpClient: httpClient,
	}

	mcpClient := mcp.NewClient(
		&mcp.Implementation{
			Name:    "mattermost-agents-plugin-bridge",
			Version: "1.0",
		},
		nil,
	)

	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   "http://plugin" + cfg.Path,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to plugin MCP server %s: %w", cfg.PluginID, err)
	}

	discoveredTools, err := listAllTools(ctx, session)
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to list tools on plugin MCP server %s: %w", cfg.PluginID, err)
	}
	if len(discoveredTools) == 0 {
		session.Close()
		return nil, fmt.Errorf("no tools found on plugin MCP server %s for user %s", cfg.PluginID, userID)
	}

	client.session = session
	client.toolsMu.Lock()
	client.tools = discoveredTools
	client.toolsMu.Unlock()

	for _, tool := range discoveredTools {
		log.Debug("Registered MCP tool",
			"userID", userID,
			"name", tool.Name,
			"description", tool.Description,
			"server", originKey)
	}

	return client, nil
}

// extractOAuthMetadataURL attempts to extract the OAuth metadata URL from an error message.
// This is part of a temporary workaround
// Returns the metadata URL and true if found, empty string and false otherwise.
func extractOAuthMetadataURL(err error) (string, bool) {
	if err == nil {
		return "", false
	}

	errMsg := err.Error()
	// Match the pattern from mcpUnauthorized.Error():
	// "OAuth authentication needed for resource at <URL>"
	// "OAuth authentication needed for resource at <URL>: Got error: <err>"
	const prefix = "OAuth authentication needed for resource at "

	idx := strings.Index(errMsg, prefix)
	if idx == -1 {
		return "", false
	}

	// Extract URL starting after the prefix
	urlStart := idx + len(prefix)
	remaining := errMsg[urlStart:]

	// Find the end of the URL. The delimiter is ": Got error:" which separates
	// the URL from the wrapped error. We cannot split on bare ":" because URLs
	// contain colons (e.g. "https://").
	urlEnd := len(remaining)
	const errorSuffix = ": Got error:"
	if suffixIdx := strings.Index(remaining, errorSuffix); suffixIdx != -1 {
		urlEnd = suffixIdx
	}

	metadataURL := strings.TrimSpace(remaining[:urlEnd])
	return metadataURL, metadataURL != ""
}

func (c *Client) oauthNeededError(err error) error {
	if err == nil {
		return nil
	}

	var mcpAuthErr *mcpUnauthorized
	if errors.As(err, &mcpAuthErr) {
		md := mcpAuthErr.MetadataURL()
		return &OAuthNeededError{
			authURL:     c.oauthNeededRedirectURL(md),
			metadataURL: md,
		}
	}

	// Temporary workaround: check for OAuth error by string matching since go-sdk
	// does not preserve error chains with %w.
	if md, ok := extractOAuthMetadataURL(err); ok {
		return &OAuthNeededError{
			authURL:     c.oauthNeededRedirectURL(md),
			metadataURL: md,
		}
	}

	return nil
}

func (c *Client) createSession(ctx context.Context, serverConfig ServerConfig) (*mcp.ClientSession, error) {
	// Prepare headers for remote servers
	headers := make(map[string]string)
	headers[MMUserIDHeader] = c.userID
	maps.Copy(headers, serverConfig.Headers)

	// TODO: Load and check cached authentication information

	// We have no information about this server, so try to connect various ways.
	client := mcp.NewClient(
		&mcp.Implementation{
			Name:    "mattermost-agents",
			Version: "1.0",
		},
		nil,
	)

	httpClient := c.httpClientForMCP(headers)

	// Try new Streamable HTTP transport first (2025-03-26 spec).
	// This will POST InitializeRequest and detect if the server supports the new transport.
	session, errStreamable := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   serverConfig.BaseURL,
		HTTPClient: httpClient,
	}, nil)
	if errStreamable == nil {
		// Successfully connected using Streamable HTTP transport
		return session, nil
	}

	// Check for OAuth error from Streamable HTTP attempt.
	if oauthErr := c.oauthNeededError(errStreamable); oauthErr != nil {
		return nil, oauthErr
	}

	// Fallback to old HTTP+SSE transport for backwards compatibility (2024-11-05 spec)
	session, errSSE := client.Connect(ctx, &mcp.SSEClientTransport{
		Endpoint:   serverConfig.BaseURL,
		HTTPClient: httpClient,
	}, nil)
	if errSSE == nil {
		// Successfully connected using SSE transport
		return session, nil
	}

	// Check for OAuth error from SSE attempt.
	if oauthErr := c.oauthNeededError(errSSE); oauthErr != nil {
		return nil, oauthErr
	}

	// If we reach here, all connection attempts failed
	return nil, fmt.Errorf("failed to connect to MCP server %s, Streamable HTTP: %w, SSE: %w", c.config.Name, errStreamable, errSSE)
}

func (c *Client) oauthStartURL() string {
	if c.oauthManager == nil {
		return ""
	}

	return c.oauthManager.StartURL(c.config.Name)
}

// oauthNeededRedirectURL returns the plugin MCP OAuth start URL, optionally
// appending resource_metadata so InitiateOAuthFlow can use the same discovery
// path as the failed MCP handshake (RFC 9728).
func (c *Client) oauthNeededRedirectURL(metadataURL string) string {
	base := c.oauthStartURL()
	if metadataURL == "" || base == "" {
		return base
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("resource_metadata", metadataURL)
	u.RawQuery = q.Encode()
	return u.String()
}

// Close closes the connection to the MCP server
func (c *Client) Close() error {
	if c.session == nil {
		return nil
	}
	return c.session.Close()
}

// Tools returns the tools available from this client
func (c *Client) Tools() map[string]*mcp.Tool {
	c.toolsMu.RLock()
	defer c.toolsMu.RUnlock()
	return maps.Clone(c.tools)
}

// CallTool calls a tool on this MCP server
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]any) (string, error) {
	return c.CallToolWithMetadata(ctx, toolName, args, nil)
}

// CallToolWithMetadata calls a tool on this MCP server with optional metadata
func (c *Client) CallToolWithMetadata(ctx context.Context, toolName string, args map[string]any, metadata map[string]any) (string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "mcp call tool",
		trace.WithAttributes(
			telemetry.MCPTool.String(toolName),
			telemetry.MCPServer.String(c.config.Name),
		),
	)
	defer span.End()

	if c.session == nil {
		err := fmt.Errorf("MCP client not connected")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	// Call the tool using new SDK
	params := &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	}

	// Add metadata if provided
	if metadata != nil {
		params.Meta = mcp.Meta(metadata)
	}

	result, err := c.session.CallTool(ctx, params)
	if err != nil {
		if errors.Is(err, mcp.ErrConnectionClosed) {
			if c.embeddedClient != nil {
				// Reconnect to embedded server using stored client helper and session ID
				if c.sessionID == "" {
					return "", fmt.Errorf("embedded server connection lost and cannot be reconnected: missing session ID")
				}

				newClient, reconnectErr := c.embeddedClient.CreateClient(ctx, c.userID, c.sessionID)
				if reconnectErr != nil {
					return "", fmt.Errorf("failed to reconnect to embedded MCP server: %w", reconnectErr)
				}

				c.toolsMu.Lock()
				c.session = newClient.session
				c.tools = newClient.Tools()
				c.toolsMu.Unlock()
				c.log.Debug("Successfully reconnected to embedded MCP server", "userID", c.userID)
			} else {
				// Reconnect to remote server
				newSession, reconnectErr := c.createSession(ctx, c.config)
				if reconnectErr != nil {
					return "", fmt.Errorf("failed to reconnect to MCP server %s: %w", c.config.Name, reconnectErr)
				}
				discoveredTools, listErr := listAllTools(ctx, newSession)
				if listErr != nil {
					newSession.Close()
					return "", fmt.Errorf("failed to list tools after reconnecting to MCP server %s: %w", c.config.Name, listErr)
				}
				if len(discoveredTools) == 0 {
					newSession.Close()
					return "", fmt.Errorf("no tools found after reconnecting to MCP server %s for user %s", c.config.Name, c.userID)
				}

				c.toolsMu.Lock()
				c.session = newSession
				c.tools = discoveredTools
				c.toolsMu.Unlock()

				if c.toolsCache != nil && shouldUseSharedToolsCache(c.config) {
					if cacheErr := c.toolsCache.SetTools(c.config.Name, c.config.Name, c.config.BaseURL, discoveredTools, time.Now()); cacheErr != nil {
						c.log.Warn("Failed to update tools cache after MCP reconnect",
							"server", c.config.Name,
							"userID", c.userID,
							"error", cacheErr)
					}
				}
				c.log.Debug("Successfully reconnected to MCP server", "userID", c.userID, "server", c.config.Name)
			}

			// Retry the tool call after reconnecting
			result, err = c.session.CallTool(ctx, params)
			if err != nil {
				return "", fmt.Errorf("failed to call tool %s on server %s after reconnecting: %w", toolName, c.config.Name, err)
			}
		} else {
			return "", fmt.Errorf("failed to call tool %s on server %s: %w", toolName, c.config.Name, err)
		}
	}
	var textBuilder strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			textBuilder.WriteString(textContent.Text)
			textBuilder.WriteByte('\n')
		}
	}
	text := textBuilder.String()

	// MCP tools can return IsError=true without transport-level errors.
	// Surface this as a resolver error so tool-call status is set correctly.
	if result.IsError {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return "", fmt.Errorf("tool %s on server %s returned an error", toolName, c.config.Name)
		}
		return trimmed, errors.New(trimmed)
	}

	if text != "" {
		return text, nil
	}

	return "", fmt.Errorf("no text content found in response from tool %s on server %s", toolName, c.config.Name)
}
