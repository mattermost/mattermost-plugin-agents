// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var ErrOAuthNotConfigured = errors.New("oauth not configured")

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
	// pluginRegistered marks entries with a live RegisterPluginServer call;
	// orphan entries hydrated only from persisted config are absent.
	pluginRegistered map[string]bool
	// sourcePluginAPI is the agents-plugin mmapi.Client; used by
	// PluginHTTPRoundTripper to dispatch to source plugins.
	sourcePluginAPI mmapi.Client
}

// NewClientManager creates a new MCP client manager. embeddedServer may be nil.
// sourcePluginAPI routes PluginHTTP to source plugins; may be nil.
func NewClientManager(config Config, log pluginapi.LogService, pluginAPI *pluginapi.Client, oauthManager *OAuthManager, embeddedServer EmbeddedMCPServer, httpClient *http.Client, sourcePluginAPI mmapi.Client) *ClientManager {
	manager := &ClientManager{
		log:              log,
		pluginAPI:        pluginAPI,
		oauthManager:     oauthManager,
		httpClient:       httpClient,
		toolsCache:       NewToolsCache(&pluginAPI.KV, &log),
		pluginServers:    make(map[string]PluginServerConfig),
		pluginRegistered: make(map[string]bool),
		sourcePluginAPI:  sourcePluginAPI,
	}
	manager.ReInit(config, embeddedServer)
	return manager
}

// EnsureMCPSessionID ensures there is a valid MCP session for the user
// This is used by both embedded and HTTP MCP servers to get a dedicated session
func (m *ClientManager) EnsureMCPSessionID(userID string) (string, error) {
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
// existing cached client is replaced.
func (m *ClientManager) createAndStoreUserClient(ctx context.Context, userID string, forceRefresh bool) (*UserClients, *Errors) {
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

	// Connect outside the manager lock so remote MCP handshakes do not block other users.
	// Cacheable client creation must not inherit request cancellation; a canceled
	// popover/tab close would otherwise poison initialRemoteConnectErrors until TTL.
	mcpErrors := userClients.ConnectToRemoteServers(cacheableContext(ctx), m.config.Servers, forceRefresh)
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
func (m *ClientManager) getClientForUser(ctx context.Context, userID string) (*UserClients, *Errors) {
	m.clientsMu.Lock()
	client, exists := m.clients[userID]
	if exists {
		m.activity[userID] = time.Now()
		m.clientsMu.Unlock()
		return client, client.InitialRemoteConnectErrors()
	}
	m.clientsMu.Unlock()

	return m.createAndStoreUserClient(ctx, userID, false)
}

// GetToolsForUser returns the tools available for a specific user, connecting to embedded server if session ID provided.
func (m *ClientManager) GetToolsForUser(ctx context.Context, userID string) ([]llm.Tool, *Errors) {
	// Get or create client for this user (connects to remote servers only)
	userClient, initialErrors := m.getClientForUser(ctx, userID)
	mcpErrors := cloneMCPErrors(initialErrors)

	// Embedded and plugin connects intentionally receive the raw cancelable ctx:
	// they run per-request and are not cached, so a canceled request should abort
	// them. Only the remote connect uses cacheableContext(ctx) (in
	// createAndStoreUserClient) because its result is cached across requests.
	if m.embeddedClient != nil {
		ensuredSessionID, ensureErr := m.ensureEmbeddedSessionID(userID)
		if ensureErr != nil {
			m.log.Debug("Failed to ensure embedded session for user - embedded MCP tools will not be available", "userID", userID, "error", ensureErr)
		} else if ensuredSessionID != "" {
			if embeddedErr := userClient.ConnectToEmbeddedServerIfAvailable(ctx, ensuredSessionID, m.embeddedClient, m.config.EmbeddedServer); embeddedErr != nil {
				m.log.Debug("Failed to connect to embedded server for user - embedded MCP tools will not be available", "userID", userID, "sessionID", ensuredSessionID, "error", embeddedErr)
			}
		}
	}

	// Snapshot under RLock, then release before PluginHTTP work.
	pluginSnap := m.snapshotEnabledPluginServers()
	for _, cfg := range pluginSnap {
		if connectErr := userClient.ConnectToPluginServer(ctx, cfg, m.sourcePluginAPI); connectErr != nil {
			m.log.Error("Failed to connect to plugin MCP server", "userID", userID, "pluginID", cfg.PluginID, "error", connectErr)
			mcpErrors = appendMCPError(mcpErrors, connectErr)
		}
	}

	rawTools := userClient.GetTools(ctx)
	filtered := filterToolsByConfig(rawTools, m.config, m.embeddedClient, pluginSnap)
	return filtered, mcpErrors
}

// RefreshToolsForUser drops cached user clients and shared server tool lists,
// pre-warms a fresh user client, then delegates to GetToolsForUser for the
// embedded/plugin connect + filtering it shares with the normal lookup path.
func (m *ClientManager) RefreshToolsForUser(ctx context.Context, userID string) ([]llm.Tool, *Errors, error) {
	if userID == "" {
		return nil, nil, errors.New("userID is required")
	}

	if refreshErr := m.invalidateSharedToolsCacheForRefresh(); refreshErr != nil {
		m.log.Warn("Failed to invalidate shared MCP tools cache during user refresh; bypassing cache for rediscovery", "userID", userID, "error", refreshErr)
	}
	m.InvalidateUserClients(userID)
	// Pre-warm the user client with a forced remote rediscovery; GetToolsForUser
	// then reuses this cached client rather than rebuilding it.
	m.createAndStoreUserClient(ctx, userID, true)

	tools, mcpErrors := m.GetToolsForUser(ctx, userID)
	return tools, mcpErrors, nil
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

	for _, server := range m.config.PluginServers {
		if !server.Enabled || server.PluginID == "" {
			continue
		}
		for _, toolConfig := range server.ToolConfigs {
			addOverride(pluginServerOriginKey(server.PluginID), toolConfig)
		}
	}

	return overrides
}

// snapshotEnabledPluginServers returns a copy of enabled plugin configs so
// callers can iterate (and do HTTP work) without holding pluginServersMu.
func (m *ClientManager) snapshotEnabledPluginServers() []PluginServerConfig {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()
	out := make([]PluginServerConfig, 0, len(m.pluginServers))
	for _, cfg := range m.pluginServers {
		if cfg.Enabled {
			out = append(out, cfg)
		}
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

// ProcessOAuthCallback processes the OAuth callback for a user
func (m *ClientManager) ProcessOAuthCallback(ctx context.Context, userID, state, code string) (*OAuthSession, error) {
	if m.oauthManager == nil {
		return nil, ErrOAuthNotConfigured
	}

	session, err := m.oauthManager.ProcessCallback(ctx, userID, state, code)
	if err != nil {
		return nil, err
	}

	// Delete the client to force a re-creation (close first, like DisconnectUserOAuth).
	m.InvalidateUserClients(userID)

	return session, nil
}

// DisconnectUserOAuth removes the stored OAuth token for a user and server,
// and invalidates the cached MCP client so a fresh connection is established
// on the next request.
func (m *ClientManager) DisconnectUserOAuth(userID, serverName string) error {
	if m.oauthManager == nil {
		return ErrOAuthNotConfigured
	}

	if err := m.oauthManager.DeleteUserToken(userID, serverName); err != nil {
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

// RegisterPluginServer stores or overwrites a plugin-server registration.
// Callers must ensure cfg.PluginID is non-empty.
func (m *ClientManager) RegisterPluginServer(cfg PluginServerConfig) {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()
	m.pluginServers[cfg.PluginID] = cfg
	m.pluginRegistered[cfg.PluginID] = true
}

func (m *ClientManager) UnregisterPluginServer(pluginID string) {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()
	delete(m.pluginServers, pluginID)
	delete(m.pluginRegistered, pluginID)
}

func (m *ClientManager) ListPluginServers() []PluginServerConfig {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()
	out := make([]PluginServerConfig, 0, len(m.pluginServers))
	for _, cfg := range m.pluginServers {
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

// IsPluginRegistered reports whether the source plugin currently has a live
// in-process registration. Returns false for entries hydrated only from
// persisted config.
func (m *ClientManager) IsPluginRegistered(pluginID string) bool {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()
	return m.pluginRegistered[pluginID]
}

// syncPluginServersFromConfig merges persisted admin-owned plugin-server fields
// onto live plugin registrations. Callers must not hold pluginServersMu.
func (m *ClientManager) syncPluginServersFromConfig(cfg Config) {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()

	for _, persisted := range cfg.PluginServers {
		if persisted.PluginID == "" {
			continue
		}
		if existing, ok := m.pluginServers[persisted.PluginID]; ok {
			// Merge admin-owned fields onto the live entry; keep runtime identity
			// and the plugin-controlled external exposure flag.
			existing.Enabled = persisted.Enabled
			existing.ToolConfigs = persisted.ToolConfigs
			m.pluginServers[persisted.PluginID] = existing
			continue
		}
		m.pluginServers[persisted.PluginID] = persisted
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
		origin := "plugin://" + ps.PluginID
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
