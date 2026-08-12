// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// Only PluginID, Name, Path, and ExposeExternal are authoritative here; Enabled and ToolConfigs come from admin config.
const pluginRegistrationsKVKey = "mcp_plugin_registrations_v1"

var ErrOAuthNotConfigured = errors.New("oauth not configured")

// ServerAccessChecker gates per-user visibility of external MCP servers by
// stable ID (satisfied by *accesscontrol.Checker).
type ServerAccessChecker interface {
	CanUseMCPServer(ctx context.Context, userID, serverID string) error
}

func cacheableContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

// ClientManager manages MCP clients for multiple users
type ClientManager struct {
	config         Config
	log            pluginapi.LogService
	pluginAPI      *pluginapi.Client
	clientsMu      sync.RWMutex
	clients        map[string]*UserClients // userID to UserClients
	activity       map[string]time.Time    // userID to last activity time
	cleanupTicker  *time.Ticker
	closeChan      chan struct{}
	clientTimeout  time.Duration
	oauthManager   *OAuthManager
	httpClient     *http.Client
	embeddedClient *EmbeddedServerClient // Helper for embedded server (nil if disabled)
	toolsCache     *ToolsCache

	// pluginServersMu must not be held across PluginHTTP round trips.
	pluginServersMu sync.RWMutex
	pluginServers   map[string]PluginServerConfig // keyed by PluginID
	// pluginRegistered marks entries backed by a source-plugin registration;
	// config-only orphan entries are absent.
	pluginRegistered map[string]bool
	// sourcePluginAPI is the agents-plugin mmapi.Client; used by
	// PluginHTTPRoundTripper to dispatch to source plugins.
	sourcePluginAPI mmapi.Client

	// accessChecker filters external servers per user (nil = no filtering).
	accessChecker ServerAccessChecker
}

// NewClientManager creates a new MCP client manager. embeddedServer may be nil.
// sourcePluginAPI routes PluginHTTP to source plugins; may be nil.
// accessChecker filters external servers per user; nil disables filtering.
func NewClientManager(config Config, log pluginapi.LogService, pluginAPI *pluginapi.Client, oauthManager *OAuthManager, embeddedServer EmbeddedMCPServer, httpClient *http.Client, sourcePluginAPI mmapi.Client, accessChecker ServerAccessChecker) *ClientManager {
	manager := &ClientManager{
		log:              log,
		pluginAPI:        pluginAPI,
		oauthManager:     oauthManager,
		httpClient:       httpClient,
		toolsCache:       NewToolsCache(&pluginAPI.KV, &log),
		pluginServers:    make(map[string]PluginServerConfig),
		pluginRegistered: make(map[string]bool),
		sourcePluginAPI:  sourcePluginAPI,
		accessChecker:    accessChecker,
	}
	manager.hydratePluginRegistrations()
	// PluginMCPHandlers is constructed later and builds the external aggregate
	// from this hydrated registry.
	manager.ReInit(config, embeddedServer)
	return manager
}

// EnsureMCPSessionID ensures there is a valid MCP session for the user
// This is used by both embedded and HTTP MCP servers to get a dedicated session
// created reports whether a new session was minted rather than reused.
func (m *ClientManager) EnsureMCPSessionID(userID string) (sessionID string, created bool, err error) {
	return m.ensureEmbeddedSessionID(userID)
}

// cleanupInactiveClients closes idle clients. closeChan/ticker are captured at
// launch to avoid racing with Close()/ReInit() reassigning the m.* fields.
func (m *ClientManager) cleanupInactiveClients(closeChan <-chan struct{}, ticker *time.Ticker) {
	for {
		select {
		case <-ticker.C:
			m.clientsMu.Lock()
			now := time.Now()
			for userID, client := range m.clients {
				if now.Sub(m.activity[userID]) > m.clientTimeout {
					m.log.Debug("Closing inactive MCP client", "userID", userID)
					client.Close()
					delete(m.clients, userID)
				}
			}
			m.clientsMu.Unlock()
		case <-closeChan:
			ticker.Stop()
			return
		}
	}
}

// ReInit re-initializes the client manager with a new configuration and embedded server
func (m *ClientManager) ReInit(config Config, embeddedServer EmbeddedMCPServer) {
	m.Close()

	if config.IdleTimeoutMinutes <= 0 {
		config.IdleTimeoutMinutes = 30
	}

	// Update embedded server client
	if embeddedServer != nil {
		m.embeddedClient = NewEmbeddedServerClientWithCache(embeddedServer, m.log, m.pluginAPI, m.toolsCache)
	} else {
		m.embeddedClient = nil
	}

	m.config = config
	m.clients = make(map[string]*UserClients)
	m.clientTimeout = time.Duration(config.IdleTimeoutMinutes) * time.Minute
	m.closeChan = make(chan struct{})
	m.activity = make(map[string]time.Time)

	m.cleanupTicker = time.NewTicker(5 * time.Minute)
	go m.cleanupInactiveClients(m.closeChan, m.cleanupTicker)

	// Must happen after m.config = config so the persisted view drives the merge.
	m.syncPluginServersFromConfig(config)
}

// Close closes the client manager and all managed clients
// The client manger should not be used after Close is called
func (m *ClientManager) Close() {
	// If already closed, do nothing
	if m.closeChan == nil {
		return
	}
	// Stop the cleanup goroutine
	close(m.closeChan)
	m.closeChan = nil
	m.cleanupTicker.Stop()

	// Close all client connections
	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	for _, client := range m.clients {
		client.Close()
	}

	// Clear the clients map
	m.clients = make(map[string]*UserClients)
}

// createAndStoreUserClient creates a new UserClients instance and stores it in the manager.
// When forceRefresh is true the remote connect bypasses the shared tools cache and any
// existing cached client is replaced. Servers whose origin is in deniedOrigins
// are never connected to, so a policy-denied server produces no connection
// artifacts (auth errors, connect errors) for this user at all.
func (m *ClientManager) createAndStoreUserClient(ctx context.Context, userID string, forceRefresh bool, deniedOrigins map[string]bool) (*UserClients, *Errors) {
	// Unless forcing a refresh, reuse an already-cached client so we skip a
	// redundant remote connect when another goroutine cached one first.
	if !forceRefresh {
		m.clientsMu.Lock()
		if client, exists := m.clients[userID]; exists {
			m.activity[userID] = time.Now()
			m.clientsMu.Unlock()
			return client, client.InitialRemoteConnectErrors()
		}
		m.clientsMu.Unlock()
	}

	userClients := NewUserClients(userID, m.log, m.oauthManager, m.httpClient, m.toolsCache)

	servers := m.config.Servers
	if len(deniedOrigins) > 0 {
		allowed := make([]ServerConfig, 0, len(servers))
		for _, server := range servers {
			if !deniedOrigins[server.BaseURL] {
				allowed = append(allowed, server)
			}
		}
		servers = allowed
	}

	// Connect outside the manager lock so remote MCP handshakes do not block other users.
	// Cacheable client creation must not inherit request cancellation; a canceled
	// popover/tab close would otherwise poison initialRemoteConnectErrors until TTL.
	mcpErrors := userClients.ConnectToRemoteServers(cacheableContext(ctx), servers, forceRefresh)
	userClients.setInitialRemoteConnectErrors(mcpErrors)

	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	// Check again in case another goroutine created the client while we were connecting.
	// On a forced refresh we intentionally replace (and close) any existing client.
	if client, exists := m.clients[userID]; exists {
		if !forceRefresh {
			userClients.Close()
			m.activity[userID] = time.Now()
			return client, client.InitialRemoteConnectErrors()
		}
		client.Close()
	}

	// Store the client even if some servers failed to connect
	// This allows partial success - user gets tools from working servers
	m.clients[userID] = userClients
	m.activity[userID] = time.Now()

	return userClients, mcpErrors
}

// getClientForUser gets or creates an MCP client for a specific user.
func (m *ClientManager) getClientForUser(ctx context.Context, userID string, deniedOrigins map[string]bool) (*UserClients, *Errors) {
	m.clientsMu.Lock()
	client, exists := m.clients[userID]
	if exists {
		m.activity[userID] = time.Now()
		m.clientsMu.Unlock()
		return client, client.InitialRemoteConnectErrors()
	}
	m.clientsMu.Unlock()

	return m.createAndStoreUserClient(ctx, userID, false, deniedOrigins)
}

// UserToolsAccess is the per-request tools listing plus the ABAC denial and
// plugin-server snapshots used to produce it. Callers that also filter server
// rows (e.g. GET /mcp/tools) must reuse DeniedOrigins and PluginServers
// instead of re-evaluating policies or re-sampling the live registry.
type UserToolsAccess struct {
	Tools         []llm.Tool
	Errors        *Errors
	DeniedOrigins map[string]bool
	// PluginServers is the live-registered plugin snapshot taken once at the
	// start of the request; ABAC, connect, filter, and response rendering all
	// reuse it so mid-request registrations cannot drift into the response.
	PluginServers []PluginServerConfig
}

// GetToolsForUser returns the tools available for a specific user.
func (m *ClientManager) GetToolsForUser(ctx context.Context, userID string) ([]llm.Tool, *Errors) {
	access := m.GetUserToolsAccess(ctx, userID)
	return access.Tools, access.Errors
}

// GetUserToolsAccess returns tools and the denied-origins snapshot from one
// ABAC evaluation pass.
func (m *ClientManager) GetUserToolsAccess(ctx context.Context, userID string) UserToolsAccess {
	return m.buildUserToolsAccess(ctx, userID, false)
}

// buildUserToolsAccess evaluates ABAC once, connects (optionally forcing remote
// rediscovery), and returns tools plus the denial snapshot.
func (m *ClientManager) buildUserToolsAccess(ctx context.Context, userID string, forceRemoteRediscovery bool) UserToolsAccess {
	// One immutable live-registered snapshot for ABAC, connect, filter, and
	// response rendering. Mid-request registrations must not appear here.
	pluginSnap := m.ListPluginServers()

	// Authorized origins are computed once so denied servers are neither
	// connected to nor represented by any artifact (tools, auth errors).
	deniedOrigins := m.deniedExternalOrigins(ctx, userID, pluginSnap)

	var userClient *UserClients
	var initialErrors *Errors
	if forceRemoteRediscovery {
		userClient, initialErrors = m.createAndStoreUserClient(ctx, userID, true, deniedOrigins)
	} else {
		userClient, initialErrors = m.getClientForUser(ctx, userID, deniedOrigins)
	}
	// Cached clients may predate a policy change, so origin-scoped errors are
	// re-filtered on every request, exactly like the tools below.
	mcpErrors := filterErrorsByDeniedOrigins(cloneMCPErrors(initialErrors), deniedOrigins)

	// Embedded and plugin connects intentionally receive the raw cancelable ctx:
	// they run per-request and are not cached, so a canceled request should abort
	// them. Only the remote connect uses cacheableContext(ctx) (in
	// createAndStoreUserClient) because its result is cached across requests.
	if m.embeddedClient != nil && !deniedOrigins[config.MCPEmbeddedServerOrigin] {
		ensuredSessionID, _, ensureErr := m.ensureEmbeddedSessionID(userID)
		if ensureErr != nil {
			m.log.Debug("Failed to ensure embedded session for user - embedded MCP tools will not be available", "userID", userID, "error", ensureErr)
		} else if ensuredSessionID != "" {
			if embeddedErr := userClient.ConnectToEmbeddedServerIfAvailable(ctx, ensuredSessionID, m.embeddedClient, m.config.EmbeddedServer); embeddedErr != nil {
				m.log.Debug("Failed to connect to embedded server for user - embedded MCP tools will not be available", "userID", userID, "sessionID", ensuredSessionID, "error", embeddedErr)
			}
		}
	}

	for _, cfg := range pluginSnap {
		if !cfg.Enabled || deniedOrigins[pluginServerOriginKey(cfg.PluginID)] {
			continue
		}
		if connectErr := userClient.ConnectToPluginServer(ctx, cfg, m.sourcePluginAPI); connectErr != nil {
			m.log.Error("Failed to connect to plugin MCP server", "userID", userID, "pluginID", cfg.PluginID, "error", connectErr)
			mcpErrors = appendMCPError(mcpErrors, connectErr)
		}
	}

	rawTools := userClient.GetTools(ctx)
	filtered := filterToolsByConfig(rawTools, m.config, m.embeddedClient, pluginSnap)
	filtered = dropToolsFromDeniedOrigins(filtered, deniedOrigins)
	return UserToolsAccess{
		Tools:         filtered,
		Errors:        mcpErrors,
		DeniedOrigins: deniedOrigins,
		PluginServers: pluginSnap,
	}
}

// deniedExternalOrigins evaluates the ABAC gate for every MCP server with a
// stable ID and returns the origins the user is denied. One decision call per
// server. Origins without a stable ID are never denied here. Filtering is
// silent by design: Debug log only, no mcpErrors entries, no chat banners.
// pluginServers must be the same request-scoped snapshot used for connect,
// filter, and response rendering.
func (m *ClientManager) deniedExternalOrigins(ctx context.Context, userID string, pluginServers []PluginServerConfig) map[string]bool {
	if m.accessChecker == nil {
		return nil
	}

	var denied map[string]bool
	deny := func(origin, serverID string) {
		if denied == nil {
			denied = make(map[string]bool)
		}
		denied[origin] = true
		m.log.Debug("Omitting MCP server for user by access policy", "userID", userID, "serverID", serverID)
	}

	for _, server := range m.config.Servers {
		if !server.Enabled || server.BaseURL == "" || server.ID == "" {
			continue
		}
		if err := m.accessChecker.CanUseMCPServer(ctx, userID, server.ID); err != nil {
			deny(server.BaseURL, server.ID)
		}
	}

	// Embedded is always-on when a client exists; enablement is not a gate.
	if m.embeddedClient != nil {
		if id := m.config.EmbeddedServer.ID; id != "" {
			if err := m.accessChecker.CanUseMCPServer(ctx, userID, id); err != nil {
				deny(config.MCPEmbeddedServerOrigin, id)
			}
		}
	}

	// Live-registered entries only (orphans stay in config for identity, not runtime).
	for _, ps := range pluginServers {
		if ps.ID == "" {
			continue
		}
		if err := m.accessChecker.CanUseMCPServer(ctx, userID, ps.ID); err != nil {
			deny(config.PluginServerOrigin(ps.PluginID), ps.ID)
		}
	}

	return denied
}

// dropToolsFromDeniedOrigins silently drops tools originating from denied
// servers. Needed on top of the connect-time skip because cached user clients
// may have connected before the policy changed.
func dropToolsFromDeniedOrigins(tools []llm.Tool, deniedOrigins map[string]bool) []llm.Tool {
	if len(deniedOrigins) == 0 || len(tools) == 0 {
		return tools
	}
	filtered := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		if !deniedOrigins[tool.ServerOrigin] {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

// filterErrorsByDeniedOrigins strips origin-scoped auth errors for denied
// servers so a denied OAuth server leaks no auth artifacts to the tool store
// or the UI. Returns nil when nothing remains.
func filterErrorsByDeniedOrigins(mcpErrors *Errors, deniedOrigins map[string]bool) *Errors {
	if mcpErrors == nil || len(deniedOrigins) == 0 {
		return mcpErrors
	}
	kept := make([]llm.ToolAuthError, 0, len(mcpErrors.ToolAuthErrors))
	for _, authErr := range mcpErrors.ToolAuthErrors {
		if !deniedOrigins[authErr.ServerOrigin] {
			kept = append(kept, authErr)
		}
	}
	mcpErrors.ToolAuthErrors = kept
	if len(mcpErrors.ToolAuthErrors) == 0 && len(mcpErrors.Errors) == 0 {
		return nil
	}
	return mcpErrors
}

// RefreshToolsForUser drops cached user clients and shared server tool lists,
// pre-warms a fresh user client, then delegates to GetUserToolsAccess.
func (m *ClientManager) RefreshToolsForUser(ctx context.Context, userID string) ([]llm.Tool, *Errors, error) {
	access, err := m.RefreshUserToolsAccess(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return access.Tools, access.Errors, nil
}

// RefreshUserToolsAccess drops caches, forces remote rediscovery, and returns
// tools with a single ABAC denial snapshot.
func (m *ClientManager) RefreshUserToolsAccess(ctx context.Context, userID string) (UserToolsAccess, error) {
	if userID == "" {
		return UserToolsAccess{}, errors.New("userID is required")
	}

	if refreshErr := m.invalidateSharedToolsCacheForRefresh(); refreshErr != nil {
		m.log.Warn("Failed to invalidate shared MCP tools cache during user refresh; bypassing cache for rediscovery", "userID", userID, "error", refreshErr)
	}
	m.InvalidateUserClients(userID)
	return m.buildUserToolsAccess(ctx, userID, true), nil
}

func (m *ClientManager) invalidateSharedToolsCacheForRefresh() error {
	if m.toolsCache == nil {
		return nil
	}

	var refreshErr error
	for _, serverConfig := range m.config.Servers {
		if !serverConfig.Enabled || serverConfig.BaseURL == "" || !shouldUseSharedToolsCache(serverConfig) {
			continue
		}
		if err := m.toolsCache.InvalidateServer(serverConfig.Name); err != nil {
			refreshErr = errors.Join(refreshErr, fmt.Errorf("failed to invalidate tools cache for server %s: %w", serverConfig.Name, err))
		}
	}
	return refreshErr
}

func cloneMCPErrors(src *Errors) *Errors {
	if src == nil || (len(src.ToolAuthErrors) == 0 && len(src.Errors) == 0) {
		return nil
	}
	return &Errors{
		ToolAuthErrors: append([]llm.ToolAuthError(nil), src.ToolAuthErrors...),
		Errors:         append([]error(nil), src.Errors...),
	}
}

func appendMCPError(mcpErrors *Errors, err error) *Errors {
	if err == nil {
		return mcpErrors
	}
	if mcpErrors == nil {
		mcpErrors = &Errors{}
	}
	mcpErrors.Errors = append(mcpErrors.Errors, err)
	return mcpErrors
}

func (m *ClientManager) GetToolRetrievalOverrides() map[string]ToolRetrievalOverride {
	if m == nil {
		return nil
	}

	var overrides map[string]ToolRetrievalOverride
	addOverride := func(serverOrigin string, toolConfig ToolConfig) {
		summary := strings.TrimSpace(toolConfig.RetrievalDescriptionOverride)
		if summary == "" {
			return
		}
		if overrides == nil {
			overrides = make(map[string]ToolRetrievalOverride)
		}
		overrides[ToolRetrievalOverrideKey(serverOrigin, toolConfig.Name)] = ToolRetrievalOverride{
			Summary: summary,
		}
	}

	for _, server := range m.config.Servers {
		if !server.Enabled {
			continue
		}
		for _, toolConfig := range server.ToolConfigs {
			addOverride(server.BaseURL, toolConfig)
		}
	}

	for _, toolConfig := range m.config.EmbeddedServer.ToolConfigs {
		addOverride(EmbeddedClientKey, toolConfig)
	}

	for _, server := range m.ListPluginServers() {
		if !server.Enabled {
			continue
		}
		for _, toolConfig := range server.ToolConfigs {
			addOverride(pluginServerOriginKey(server.PluginID), toolConfig)
		}
	}

	return overrides
}

// snapshotEnabledPluginServers returns a copy of enabled, live-registered
// plugin configs so callers can iterate (and do HTTP work) without holding
// pluginServersMu. Config-only orphans are excluded.
func (m *ClientManager) snapshotEnabledPluginServers() []PluginServerConfig {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()
	out := make([]PluginServerConfig, 0, len(m.pluginServers))
	for pluginID, cfg := range m.pluginServers {
		if !m.pluginRegistered[pluginID] || !cfg.Enabled {
			continue
		}
		out = append(out, cfg)
	}
	return out
}

// InvalidateUserClients closes and removes cached MCP clients for a user.
func (m *ClientManager) InvalidateUserClients(userID string) {
	if userID == "" {
		return
	}

	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	if uc, ok := m.clients[userID]; ok {
		uc.Close()
		delete(m.clients, userID)
	}
	delete(m.activity, userID)
}

// ProcessOAuthCallback processes the OAuth callback for a user. iss is the
// RFC 9207 issuer identifier from the authorization response, if any.
func (m *ClientManager) ProcessOAuthCallback(ctx context.Context, userID, state, code, iss string) (*OAuthSession, error) {
	if m.oauthManager == nil {
		return nil, ErrOAuthNotConfigured
	}

	session, err := m.oauthManager.ProcessCallback(ctx, userID, state, code, iss)
	if err != nil {
		return nil, err
	}

	// Delete the client to force a re-creation (close first, like DisconnectUserOAuth).
	m.InvalidateUserClients(userID)

	return session, nil
}

// DisconnectUserOAuth removes the stored OAuth token for a user and server,
// and invalidates the cached MCP client so a fresh connection is established
// on the next request. The stored grant is also best-effort revoked at the
// authorization server (RFC 7009) before deletion.
func (m *ClientManager) DisconnectUserOAuth(ctx context.Context, userID, serverName string) error {
	if m.oauthManager == nil {
		return ErrOAuthNotConfigured
	}

	if err := m.oauthManager.DeleteUserToken(ctx, userID, serverName); err != nil {
		return err
	}

	m.InvalidateUserClients(userID)

	return nil
}

// MarkOAuthNeeded stores the latest upstream OAuth-needed state for a user/server
// and drops any cached client so subsequent tool discovery reflects the reconnectable state.
func (m *ClientManager) MarkOAuthNeeded(userID, serverName, authURL string) error {
	var storeErr error
	if m.oauthManager != nil {
		storeErr = m.oauthManager.StoreAuthNeededState(userID, serverName, authURL)
	}

	m.InvalidateUserClients(userID)

	return storeErr
}

// GetOAuthManager returns the OAuth manager instance
func (m *ClientManager) GetOAuthManager() *OAuthManager {
	return m.oauthManager
}

// GetToolsCache returns the tools cache instance
func (m *ClientManager) GetToolsCache() *ToolsCache {
	return m.toolsCache
}

// GetEmbeddedServer returns the embedded MCP server instance (may be nil)
// This method is kept for API compatibility
func (m *ClientManager) GetEmbeddedServer() EmbeddedMCPServer {
	if m.embeddedClient == nil {
		return nil
	}
	return m.embeddedClient.server
}

// GetHTTPClient returns the HTTP client for upstream requests
func (m *ClientManager) GetHTTPClient() *http.Client {
	return m.httpClient
}

// GetConfig returns a snapshot of the current MCP configuration.
func (m *ClientManager) GetConfig() Config {
	return m.config
}

// MCPServerIDByOrigin snapshots the stable-ID mapping for the manager's current
// config. See config.MCPConfig.ServerIDByOrigin.
func (m *ClientManager) MCPServerIDByOrigin() map[string]string {
	cfg := m.config
	return cfg.ServerIDByOrigin()
}

// RegisterPluginServer stores or overwrites a plugin-server registration.
// Callers must ensure cfg.PluginID is non-empty.
func (m *ClientManager) RegisterPluginServer(cfg PluginServerConfig) {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()
	m.pluginServers[cfg.PluginID] = cfg
	m.pluginRegistered[cfg.PluginID] = true
	m.mutatePersistedPluginRegistrations(func(registrations map[string]PluginServerConfig) {
		registrations[cfg.PluginID] = cfg
	})
}

// UpdatePluginServerAdminFields applies the admin-owned fields (Enabled,
// ToolConfigs) onto the current registry entry for pluginID without touching
// the plugin-owned fields (Name, Path, ExposeExternal), so an admin update can
// never revert a concurrent re-registration by the source plugin. Returns the
// resulting entry; reports false when pluginID is not registered.
func (m *ClientManager) UpdatePluginServerAdminFields(pluginID string, enabled bool, toolConfigs []ToolConfig) (PluginServerConfig, bool) {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()
	cfg, ok := m.pluginServers[pluginID]
	if !ok {
		return PluginServerConfig{}, false
	}
	cfg.Enabled = enabled
	cfg.ToolConfigs = toolConfigs
	m.pluginServers[pluginID] = cfg
	return cfg, true
}

func (m *ClientManager) UnregisterPluginServer(pluginID string) {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()
	delete(m.pluginServers, pluginID)
	delete(m.pluginRegistered, pluginID)
	m.mutatePersistedPluginRegistrations(func(registrations map[string]PluginServerConfig) {
		delete(registrations, pluginID)
	})
}

// ListPluginServers returns live-registered plugin MCP servers only.
// Persisted config-only orphans are not listed.
func (m *ClientManager) ListPluginServers() []PluginServerConfig {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()
	out := make([]PluginServerConfig, 0, len(m.pluginServers))
	for pluginID, cfg := range m.pluginServers {
		if !m.pluginRegistered[pluginID] {
			continue
		}
		out = append(out, cfg)
	}
	return out
}

// GetPluginServer returns a value-copy of the stored config for pluginID.
func (m *ClientManager) GetPluginServer(pluginID string) (PluginServerConfig, bool) {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()
	cfg, ok := m.pluginServers[pluginID]
	return cfg, ok
}

func (m *ClientManager) hydratePluginRegistrations() {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()

	registrations, ok := m.loadPersistedPluginRegistrationsLocked()
	if !ok {
		return
	}

	verifyPluginStates := len(registrations) > 0
	var pluginStates map[string]*model.PluginState
	if verifyPluginStates {
		serverConfig := m.pluginAPI.Configuration.GetConfig()
		if serverConfig == nil {
			m.log.Warn("Unable to verify plugin states while restoring MCP registrations; keeping all registrations")
			verifyPluginStates = false
		} else {
			pluginStates = serverConfig.PluginSettings.PluginStates
		}
	}

	prunedPluginIDs := make([]string, 0)
	restored := 0
	for pluginID, cfg := range registrations {
		if verifyPluginStates {
			state := pluginStates[pluginID]
			if state == nil || !state.Enable {
				prunedPluginIDs = append(prunedPluginIDs, pluginID)
				continue
			}
		}

		m.pluginServers[pluginID] = cfg
		m.pluginRegistered[pluginID] = true
		restored++
	}

	if len(prunedPluginIDs) > 0 {
		m.mutatePersistedPluginRegistrations(func(registrations map[string]PluginServerConfig) {
			for _, pluginID := range prunedPluginIDs {
				delete(registrations, pluginID)
			}
		})
	}
	m.log.Debug("Restored plugin MCP registrations from KV store", "count", restored, "pruned", len(prunedPluginIDs))
}

func (m *ClientManager) mutatePersistedPluginRegistrations(update func(map[string]PluginServerConfig)) {
	err := m.pluginAPI.KV.SetAtomicWithRetries(pluginRegistrationsKVKey, func(oldValue []byte) (any, error) {
		registrations := make(map[string]PluginServerConfig)
		if len(oldValue) > 0 {
			if err := json.Unmarshal(oldValue, &registrations); err != nil {
				return nil, fmt.Errorf("unmarshal plugin MCP registrations: %w", err)
			}
		}
		if registrations == nil {
			registrations = make(map[string]PluginServerConfig)
		}
		update(registrations)
		return registrations, nil
	})
	if err != nil {
		m.log.Error("Failed to persist plugin MCP registrations to KV store", "error", err)
	}
}

func (m *ClientManager) loadPersistedPluginRegistrationsLocked() (map[string]PluginServerConfig, bool) {
	var registrations map[string]PluginServerConfig
	if err := m.pluginAPI.KV.Get(pluginRegistrationsKVKey, &registrations); err != nil {
		m.log.Error("Failed to load plugin MCP registrations from KV store", "error", err)
		return nil, false
	}
	if registrations == nil {
		registrations = make(map[string]PluginServerConfig)
	}
	return registrations, true
}

// ApplyPersistedPluginServerFields overlays admin-owned persisted fields
// (Enabled, ToolConfigs, ID) onto a live registration. Name/Path/ExposeExternal
// remain plugin-owned.
func ApplyPersistedPluginServerFields(live, persisted PluginServerConfig) PluginServerConfig {
	live.Enabled = persisted.Enabled
	live.ToolConfigs = persisted.ToolConfigs
	if persisted.ID != "" {
		live.ID = persisted.ID
	}
	return live
}

// syncPluginServersFromConfig merges persisted admin-owned plugin-server fields
// onto live-registered entries only. Config-only orphan rows keep their
// identity/policy in config but never become runtime registry members —
// hydratePluginRegistrations (KV) and RegisterPluginServer own membership.
// Callers must not hold pluginServersMu.
func (m *ClientManager) syncPluginServersFromConfig(cfg Config) {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()

	for _, persisted := range cfg.PluginServers {
		if persisted.PluginID == "" {
			continue
		}
		existing, ok := m.pluginServers[persisted.PluginID]
		if !ok || !m.pluginRegistered[persisted.PluginID] {
			continue
		}
		m.pluginServers[persisted.PluginID] = ApplyPersistedPluginServerFields(existing, persisted)
	}
}

func (m *ClientManager) DiscoverPluginServerTools(ctx context.Context, userID string, cfg PluginServerConfig) ([]ToolInfo, error) {
	return DiscoverPluginServerTools(ctx, userID, cfg, m.sourcePluginAPI, m.log)
}

// filterToolsByConfig filters raw discovered tools against admin-configured
// policies. Result is ordered by configured server order, then by tool name.
// The embedded server falls back to the vetted seed when ToolConfigs is empty.
// Plugin-registered servers flow through via synthetic ServerConfig entries
// keyed by "plugin://<pluginID>".
func filterToolsByConfig(rawTools []llm.Tool, cfg Config, embeddedClient *EmbeddedServerClient, pluginServers []PluginServerConfig) []llm.Tool {
	serverByOrigin := make(map[string]*ServerConfig, len(cfg.Servers)+len(pluginServers)+1)
	serverOrder := make([]string, 0, len(cfg.Servers)+len(pluginServers)+1)

	for i := range cfg.Servers {
		s := &cfg.Servers[i]
		if !s.Enabled {
			continue
		}
		serverByOrigin[s.BaseURL] = s
		serverOrder = append(serverOrder, s.BaseURL)
	}

	if embeddedClient != nil {
		embeddedCfg := &ServerConfig{
			Name:    EmbeddedServerName,
			Enabled: true,
			BaseURL: EmbeddedClientKey,
		}
		// Persisted tool configs override the vetted seed.
		if len(cfg.EmbeddedServer.ToolConfigs) > 0 {
			embeddedCfg.ToolConfigs = cfg.EmbeddedServer.ToolConfigs
		} else {
			embeddedCfg.ToolConfigs = SeedVettedToolConfigs(EmbeddedClientKey)
		}
		serverByOrigin[EmbeddedClientKey] = embeddedCfg
		serverOrder = append(serverOrder, EmbeddedClientKey)
	}

	for _, ps := range pluginServers {
		if !ps.Enabled {
			continue
		}
		origin := config.PluginServerOrigin(ps.PluginID)
		serverByOrigin[origin] = &ServerConfig{
			Name:        ps.Name,
			Enabled:     true,
			BaseURL:     origin,
			ToolConfigs: ps.ToolConfigs,
		}
		serverOrder = append(serverOrder, origin)
	}

	toolsByOrigin := make(map[string][]llm.Tool, len(rawTools))
	for _, t := range rawTools {
		toolsByOrigin[t.ServerOrigin] = append(toolsByOrigin[t.ServerOrigin], t)
	}

	var result []llm.Tool
	for _, origin := range serverOrder {
		sc, ok := serverByOrigin[origin]
		if !ok {
			continue
		}

		tools, hasTool := toolsByOrigin[origin]
		if !hasTool {
			continue
		}

		var filtered []llm.Tool
		for _, t := range tools {
			_, enabled := sc.GetToolPolicy(ToolPolicyLookupName(sc, t.Name))
			if enabled {
				filtered = append(filtered, t)
			}
		}

		// Sort for deterministic output.
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].Name < filtered[j].Name
		})

		result = append(result, filtered...)
	}

	return result
}
