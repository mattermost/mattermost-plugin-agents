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

	listToolsMethod = "tools/list"

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
	serviceAccount bool                  // auth via static ServiceAccountHeaders; remotes only, oauthManager nil
}

// clientParams bundles the dependencies for a remote MCP client connection.
type clientParams struct {
	log            pluginapi.LogService
	oauthManager   *OAuthManager // nil in service-account mode
	httpClient     *http.Client
	toolsCache     *ToolsCache
	forceRefresh   bool
	serviceAccount bool
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

// sharedToolsCacheAllowedForServer reports whether user-mode connections may use the
// shared tools cache; static OAuth credentials make the catalog user-specific.
func sharedToolsCacheAllowedForServer(serverConfig ServerConfig) bool {
	return staticOAuthCreds(serverConfig) == nil
}

// serviceAccountToolsCacheID namespaces service-account tool lists away from the
// user-mode cache entry (keyed by the bare server name).
func serviceAccountToolsCacheID(serverName string) string {
	return "sa:" + serverName
}

func (c *Client) toolsCacheServerID() string {
	if c.serviceAccount {
		return serviceAccountToolsCacheID(c.config.Name)
	}
	return c.config.Name
}

// useSharedToolsCache reports whether this client may read/write the shared tools
// cache. Service-account credentials are identical for every connection, so SA mode always may.
func (c *Client) useSharedToolsCache() bool {
	if c.serviceAccount {
		return true
	}
	return sharedToolsCacheAllowedForServer(c.config)
}

// remoteConnectionHeaders builds the static headers for a remote MCP connection.
// Later layers win on key conflicts: X-Mattermost-UserID < admin Headers < ServiceAccountHeaders.
func remoteConnectionHeaders(userID string, serverConfig ServerConfig, serviceAccount bool) map[string]string {
	headers := make(map[string]string)
	headers[MMUserIDHeader] = userID
	maps.Copy(headers, serverConfig.Headers)
	if serviceAccount {
		maps.Copy(headers, serverConfig.EffectiveServiceAccountHeaders())
	}
	return headers
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
	if sharedToolsCacheAllowedForServer(serverConfig) || toolsCache == nil || oauthManager == nil {
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

// NewSDKClient builds a go-sdk MCP client hardened against hostile tools/list
// responses. Use it instead of calling mcp.NewClient directly.
func NewSDKClient(impl *mcp.Implementation, opts *mcp.ClientOptions) *mcp.Client {
	client := mcp.NewClient(impl, opts)
	client.AddSendingMiddleware(dropNilTools)
	return client
}

// dropNilTools removes null entries from tools/list responses. go-sdk v1.7.0's
// ListTools panics (nil dereference in filterValidTools) when a server's
// tools/list result contains a JSON null tool entry, so a misbehaving remote
// MCP server could crash the whole plugin process before any of our own nil
// checks run. Stripping the entries in sending middleware runs before the
// SDK's validation pass. Upstream report:
// https://github.com/modelcontextprotocol/go-sdk/issues/1119
func dropNilTools(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, req)
		if err != nil || method != listToolsMethod {
			return result, err
		}
		listResult, ok := result.(*mcp.ListToolsResult)
		if !ok || listResult == nil {
			return result, nil
		}
		kept := listResult.Tools[:0]
		for _, tool := range listResult.Tools {
			if tool != nil {
				kept = append(kept, tool)
			}
		}
		listResult.Tools = kept
		return listResult, nil
	}
}

// ListSessionTools lists every tool available on session in wire order,
// following pagination and skipping nil entries.
func ListSessionTools(ctx context.Context, session *mcp.ClientSession) (tools []*mcp.Tool, err error) {
	// The known nil-tool panic is prevented by the dropNilTools middleware on
	// clients built via NewSDKClient; keep the recover as defense-in-depth so
	// no future SDK panic can crash the whole plugin process.
	defer func() {
		if r := recover(); r != nil {
			tools = nil
			err = fmt.Errorf("panic while listing tools from MCP server: %v", r)
		}
	}()

	for tool, iterErr := range session.Tools(ctx, &mcp.ListToolsParams{}) {
		if iterErr != nil {
			return nil, iterErr
		}
		if tool == nil {
			continue
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func listAllTools(ctx context.Context, session *mcp.ClientSession) (map[string]*mcp.Tool, error) {
	toolList, err := ListSessionTools(ctx, session)
	if err != nil {
		return nil, err
	}
	tools := make(map[string]*mcp.Tool, len(toolList))
	for _, tool := range toolList {
		tools[tool.Name] = tool
	}
	return tools, nil
}

// adoptSession lists the tools available on session and, when at least one is
// found, installs the session and tools on c. On failure the session is
// closed and c is left unmodified.
func (c *Client) adoptSession(ctx context.Context, session *mcp.ClientSession, serverLabel string) error {
	discoveredTools, err := listAllTools(ctx, session)
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to list tools: %w", err)
	}
	if len(discoveredTools) == 0 {
		session.Close()
		return fmt.Errorf("no tools found on MCP server %s for user %s", serverLabel, c.userID)
	}

	c.toolsMu.Lock()
	c.session = session
	c.tools = discoveredTools
	c.toolsMu.Unlock()

	for _, tool := range discoveredTools {
		c.log.Debug("Registered MCP tool",
			"userID", c.userID,
			"name", tool.Name,
			"description", tool.Description,
			"server", serverLabel)
	}
	return nil
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
	mcpClient := NewSDKClient(
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
		config:         ServerConfig{Name: EmbeddedClientKey, BaseURL: EmbeddedClientKey, Enabled: true},
		tools:          make(map[string]*mcp.Tool),
		userID:         userID,
		log:            c.log,
		oauthManager:   nil, // Embedded servers don't use OAuth
		toolsCache:     c.toolsCache,
		embeddedClient: c,         // Store client helper for reconnection
		sessionID:      sessionID, // Store session ID for reconnection
	}
	if err := client.adoptSession(ctx, mcpSession, EmbeddedClientKey); err != nil {
		return nil, err
	}

	c.log.Debug("Successfully connected to embedded MCP server",
		"userID", userID,
		"server", EmbeddedClientKey)

	return client, nil
}

// NewClient creates a user-OAuth-mode MCP client for the given server and user and connects to it.
// forceRefresh bypasses the shared tools cache read. Its sole purpose is to close the race where a concurrent
// lookup repopulates the cache between a manual refresh's invalidation and this reconnect; a plain
// post-invalidation rediscovery would otherwise cache-miss on its own.
func NewClient(ctx context.Context, userID string, serverConfig ServerConfig, log pluginapi.LogService, oauthManager *OAuthManager, httpClient *http.Client, toolsCache *ToolsCache, forceRefresh bool) (*Client, error) {
	return newClient(ctx, userID, serverConfig, clientParams{
		log:          log,
		oauthManager: oauthManager,
		httpClient:   httpClient,
		toolsCache:   toolsCache,
		forceRefresh: forceRefresh,
	})
}

// newClient connects to a remote MCP server in either auth mode. In service-account
// mode p.oauthManager must be nil so no OAuth flow can occur.
func newClient(ctx context.Context, userID string, serverConfig ServerConfig, p clientParams) (*Client, error) {
	c := &Client{
		session:        nil,
		config:         serverConfig,
		tools:          make(map[string]*mcp.Tool),
		userID:         userID,
		log:            p.log,
		oauthManager:   p.oauthManager,
		httpClient:     p.httpClient,
		toolsCache:     p.toolsCache,
		serviceAccount: p.serviceAccount,
	}

	session, err := c.createSession(ctx, serverConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP session for server %s: %w", serverConfig.Name, err)
	}

	sharedToolsCache := c.useSharedToolsCache()
	maybeInvalidateSharedToolsBeforeOAuthListTools(userID, serverConfig, p.log, p.toolsCache, p.oauthManager)
	serverID := c.toolsCacheServerID()

	// Try to get tools from global cache first.
	if p.toolsCache != nil && sharedToolsCache && !p.forceRefresh {
		cachedTools := p.toolsCache.GetTools(serverID)
		if len(cachedTools) > 0 {
			// Cache hit - use cached tools
			c.toolsMu.Lock()
			c.tools = cachedTools
			c.toolsMu.Unlock()
			p.log.Debug("Using cached tools for MCP server",
				"userID", userID,
				"server", serverConfig.Name,
				"toolCount", len(cachedTools))
			c.session = session
			return c, nil
		}
	}

	// Cache miss - fetch tools from server
	if err := c.adoptSession(ctx, session, serverConfig.Name); err != nil {
		if oauthErr := c.oauthNeededError(err); oauthErr != nil {
			return nil, oauthErr
		}
		return nil, err
	}

	// Update the global cache with fetched tools.
	if p.toolsCache != nil && sharedToolsCache {
		if err := p.toolsCache.SetTools(serverID, serverConfig.Name, serverConfig.BaseURL, c.Tools(), time.Now()); err != nil {
			p.log.Warn("Failed to update tools cache", "server", serverConfig.Name, "error", err)
		}
	}

	return c, nil
}

// NewPluginClient creates a per-user MCP client for a plugin-registered server.
// Plugin clients list tools at connect time and do not use the shared tools cache.
func NewPluginClient(ctx context.Context, userID string, cfg PluginServerConfig, sourcePluginAPI mmapi.Client, log pluginapi.LogService) (*Client, error) {
	if sourcePluginAPI == nil {
		return nil, fmt.Errorf("sourcePluginAPI is nil; plugin MCP server %s cannot be reached", cfg.PluginID)
	}

	originKey := pluginServerOriginKey(cfg.PluginID)
	httpClient := PluginServerHTTPClient(NewPluginHTTPRoundTripper(cfg.PluginID, cfg.Path, sourcePluginAPI), userID)

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

	session, err := ConnectPluginServer(ctx, "mattermost-agents-plugin-bridge", cfg.Path, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to plugin MCP server %s: %w", cfg.PluginID, err)
	}

	if err := client.adoptSession(ctx, session, originKey); err != nil {
		return nil, fmt.Errorf("plugin MCP server %s: %w", cfg.PluginID, err)
	}

	return client, nil
}

func (c *Client) oauthNeededError(err error) error {
	if err == nil {
		return nil
	}

	// Service-account mode has no per-user OAuth; never classify a failure as OAuth-needed.
	if c.serviceAccount {
		return nil
	}

	if mcpAuthErr, ok := errors.AsType[*mcpUnauthorized](err); ok {
		md := mcpAuthErr.MetadataURL()
		return &OAuthNeededError{
			authURL:     c.oauthNeededRedirectURL(md, mcpAuthErr.Scope()),
			metadataURL: md,
		}
	}

	return nil
}

func (c *Client) createSession(ctx context.Context, serverConfig ServerConfig) (*mcp.ClientSession, error) {
	// Prepare headers for remote servers
	headers := remoteConnectionHeaders(c.userID, serverConfig, c.serviceAccount)

	// TODO: Load and check cached authentication information

	// We have no information about this server, so try to connect various ways.
	client := NewSDKClient(
		&mcp.Implementation{
			Name:    "mattermost-agents",
			Version: "1.0",
		},
		nil,
	)

	httpClient := c.httpClientForMCP(serverConfig.BaseURL, headers)

	// OAuth-capable clients get a per-connection handler; embedded and
	// plugin-bridge clients (nil oauthManager) do not use OAuth.
	var oauthHandler *userOAuthHandler
	if c.oauthManager != nil {
		oauthHandler = newUserOAuthHandler(c.userID, serverConfig, c.oauthManager)
	}

	// Try the modern Streamable HTTP transport first. The SDK auto-negotiates the
	// protocol version (2026-07-28 down to 2025-03-26) via a server/discover request,
	// falling back to a legacy initialize request when the server does not support it.
	streamableTransport := &mcp.StreamableClientTransport{
		Endpoint:             serverConfig.BaseURL,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
	}
	if oauthHandler != nil {
		streamableTransport.OAuthHandler = oauthHandler
	}
	session, errStreamable := client.Connect(ctx, streamableTransport, nil)
	if errStreamable == nil {
		// Successfully connected using Streamable HTTP transport
		return session, nil
	}

	// Check for OAuth error from Streamable HTTP attempt.
	if oauthErr := c.oauthNeededError(errStreamable); oauthErr != nil {
		return nil, oauthErr
	}

	// Fall back to the HTTP+SSE transport for legacy servers that only implement
	// the 2024-11-05 HTTP+SSE transport. SSEClientTransport has no OAuthHandler
	// field, so OAuth is applied through a RoundTripper adapter instead.
	session, errSSE := client.Connect(ctx, &mcp.SSEClientTransport{
		Endpoint:   serverConfig.BaseURL,
		HTTPClient: c.httpClientForLegacySSE(serverConfig.BaseURL, oauthHandler, headers),
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
// path as the failed MCP handshake (RFC 9728) and the challenge's
// authoritative scope so re-authorization requests exactly it (RFC 6750 §3).
func (c *Client) oauthNeededRedirectURL(metadataURL, scope string) string {
	base := c.oauthStartURL()
	if base == "" || (metadataURL == "" && scope == "") {
		return base
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	if metadataURL != "" {
		q.Set("resource_metadata", metadataURL)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
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
				if adoptErr := c.adoptSession(ctx, newSession, c.config.Name); adoptErr != nil {
					return "", fmt.Errorf("failed to reconnect to MCP server %s: %w", c.config.Name, adoptErr)
				}

				if c.toolsCache != nil && c.useSharedToolsCache() {
					if cacheErr := c.toolsCache.SetTools(c.toolsCacheServerID(), c.config.Name, c.config.BaseURL, c.Tools(), time.Now()); cacheErr != nil {
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
