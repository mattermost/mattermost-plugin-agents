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

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// Only PluginID, Name, Path, and ExposeExternal are authoritative here; Enabled and ToolConfigs come from admin config.
const pluginRegistrationsKVKey = "mcp_plugin_registrations_v1"

var ErrOAuthNotConfigured = errors.New("oauth not configured")

func cacheableContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

// clientKind is the structural role of a pooled bag. Remotes bags cannot
// attach embedded/plugin clients; local bags cannot attach remotes.
type clientKind int

const (
	clientKindUserRemote clientKind = iota
	clientKindSARemote
	clientKindLocal
)

// clientKey identifies one pooled client bag.
type clientKey struct {
	userID string
	kind   clientKind
}

// ClientManager manages MCP clients for multiple users
type ClientManager struct {
	config         Config
	log            pluginapi.LogService
	pluginAPI      *pluginapi.Client
	clientsMu      sync.RWMutex
	clients        map[clientKey]*UserClients
	activity       map[clientKey]time.Time
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
			for key, client := range m.clients {
				if now.Sub(m.activity[key]) > m.clientTimeout {
					m.log.Debug("Closing inactive MCP client", "userID", key.userID, "kind", key.kind)
					client.Close()
					delete(m.clients, key)
					delete(m.activity, key)
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
	m.clients = make(map[clientKey]*UserClients)
	m.clientTimeout = time.Duration(config.IdleTimeoutMinutes) * time.Minute
	m.closeChan = make(chan struct{})
	m.activity = make(map[clientKey]time.Time)

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

	m.clients = make(map[clientKey]*UserClients)
}

// createAndStoreUserClient creates a new UserClients instance and stores it in the manager.
// When forceRefresh is true the remote connect bypasses the shared tools cache and any
// existing cached client is replaced.
func (m *ClientManager) createAndStoreUserClient(ctx context.Context, key clientKey, forceRefresh bool) (*UserClients, *Errors) {
	// Unless forcing a refresh, reuse an already-cached client so we skip a
	// redundant remote connect when another goroutine cached one first.
	if !forceRefresh {
		m.clientsMu.Lock()
		if client, exists := m.clients[key]; exists {
			m.activity[key] = time.Now()
			m.clientsMu.Unlock()
			return client, client.InitialRemoteConnectErrors()
		}
		m.clientsMu.Unlock()
	}

	userClients := newRemoteClients(key.userID, key.kind, m.log, m.oauthManager, m.httpClient, m.toolsCache)

	// Connect outside the manager lock so remote MCP handshakes do not block other users.
	// Cacheable client creation must not inherit request cancellation; a canceled
	// popover/tab close would otherwise poison initialRemoteConnectErrors until TTL.
	mcpErrors := userClients.ConnectToRemoteServers(cacheableContext(ctx), m.config.Servers, forceRefresh)
	userClients.setInitialRemoteConnectErrors(mcpErrors)

	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	// Check again in case another goroutine created the client while we were connecting.
	// On a forced refresh we intentionally replace (and close) any existing client.
	if client, exists := m.clients[key]; exists {
		if !forceRefresh {
			userClients.Close()
			m.activity[key] = time.Now()
			return client, client.InitialRemoteConnectErrors()
		}
		client.Close()
	}

	// Store the client even if some servers failed to connect
	// This allows partial success - user gets tools from working servers
	m.clients[key] = userClients
	m.activity[key] = time.Now()

	return userClients, mcpErrors
}

// getClient gets or creates an MCP client bag for a specific identity and auth mode.
func (m *ClientManager) getClient(ctx context.Context, key clientKey) (*UserClients, *Errors) {
	m.clientsMu.Lock()
	client, exists := m.clients[key]
	if exists {
		m.activity[key] = time.Now()
		m.clientsMu.Unlock()
		return client, client.InitialRemoteConnectErrors()
	}
	m.clientsMu.Unlock()

	return m.createAndStoreUserClient(ctx, key, false)
}

// GetTools is the single catalog boundary: it builds the MCP tool catalog for
// req. Remotes come from the pooled bag identified by req; embedded and plugin
// servers always connect as the invoking user on a local bag. Namespacing runs
// once across both bags.
func (m *ClientManager) GetTools(ctx context.Context, req CatalogRequest) ([]llm.Tool, *Errors) {
	if err := req.validate(); err != nil {
		return nil, &Errors{Errors: []error{err}}
	}

	remoteClient, initialErrors := m.getClient(ctx, req.remoteKey())
	mcpErrors := cloneMCPErrors(initialErrors)

	pluginSnap := m.snapshotEnabledPluginServers()
	var localSnapshots []userClientSnapshot
	if m.embeddedClient != nil || len(pluginSnap) > 0 {
		localClient := m.getOrCreateLocalClient(req.InvokingUserID)
		mcpErrors = m.connectLocalServers(ctx, localClient, pluginSnap, mcpErrors)
		localSnapshots = localClient.snapshotClients()
	}

	rawTools := collectToolsFromSnapshots(req.InvokingUserID, m.log, remoteClient.snapshotClients(), localSnapshots)
	return filterToolsByConfig(rawTools, m.config, m.embeddedClient, pluginSnap), mcpErrors
}

// getOrCreateLocalClient returns the per-user embedded+plugin bag.
func (m *ClientManager) getOrCreateLocalClient(userID string) *UserClients {
	key := clientKey{userID: userID, kind: clientKindLocal}

	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()
	if client, exists := m.clients[key]; exists {
		m.activity[key] = time.Now()
		return client
	}
	client := newLocalClients(key.userID, m.log, m.httpClient, m.toolsCache)
	m.clients[key] = client
	m.activity[key] = time.Now()
	return client
}

// connectLocalServers attaches the embedded Mattermost server and plugin MCP
// servers to a per-user bag, appending connect failures to mcpErrors. The raw
// cancelable ctx is intentional: these connects run per-request. Existing
// clients on the bag are reused.
func (m *ClientManager) connectLocalServers(ctx context.Context, userClient *UserClients, pluginSnap []PluginServerConfig, mcpErrors *Errors) *Errors {
	userID := userClient.userID

	if m.embeddedClient != nil {
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
		if connectErr := userClient.ConnectToPluginServer(ctx, cfg, m.sourcePluginAPI); connectErr != nil {
			m.log.Error("Failed to connect to plugin MCP server", "userID", userID, "pluginID", cfg.PluginID, "error", connectErr)
			mcpErrors = appendMCPError(mcpErrors, connectErr)
		}
	}
	return mcpErrors
}

// RefreshToolsForUser drops cached user clients and shared server tool lists,
// pre-warms a fresh user client, then delegates to GetTools for the
// embedded/plugin connect + filtering it shares with the normal lookup path.
func (m *ClientManager) RefreshToolsForUser(ctx context.Context, userID string) ([]llm.Tool, *Errors, error) {
	if userID == "" {
		return nil, nil, errors.New("userID is required")
	}

	if refreshErr := m.invalidateSharedToolsCacheForRefresh(); refreshErr != nil {
		m.log.Warn("Failed to invalidate shared MCP tools cache during user refresh; bypassing cache for rediscovery", "userID", userID, "error", refreshErr)
	}
	m.InvalidateUserClients(userID)
	req := UserCatalogRequest(userID)
	// Pre-warm the user remotes bag with a forced remote rediscovery; GetTools
	// then reuses this cached client rather than rebuilding it.
	m.createAndStoreUserClient(ctx, req.remoteKey(), true)

	tools, mcpErrors := m.GetTools(ctx, req)
	return tools, mcpErrors, nil
}

func (m *ClientManager) invalidateSharedToolsCacheForRefresh() error {
	if m.toolsCache == nil {
		return nil
	}

	var refreshErr error
	invalidate := func(cacheID string) {
		if err := m.toolsCache.InvalidateServer(cacheID); err != nil {
			refreshErr = errors.Join(refreshErr, fmt.Errorf("failed to invalidate tools cache for server %s: %w", cacheID, err))
		}
	}

	for _, serverConfig := range m.config.Servers {
		if !serverConfig.Enabled || serverConfig.BaseURL == "" {
			continue
		}
		if sharedToolsCacheAllowedForServer(serverConfig) {
			invalidate(serverConfig.Name)
		}
		// Service-account entries are always shared-cached, even for static-OAuth servers.
		if serverConfig.HasServiceAccountAuth() {
			invalidate(serviceAccountToolsCacheID(serverConfig.Name))
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

// InvalidateUserClients closes and removes cached MCP clients for a user, in both auth modes.
func (m *ClientManager) InvalidateUserClients(userID string) {
	if userID == "" {
		return
	}

	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	for _, kind := range []clientKind{clientKindUserRemote, clientKindSARemote, clientKindLocal} {
		key := clientKey{userID: userID, kind: kind}
		if uc, ok := m.clients[key]; ok {
			uc.Close()
			delete(m.clients, key)
		}
		delete(m.activity, key)
	}
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

// UpdatePluginServer applies admin-owned fields without changing registration state.
func (m *ClientManager) UpdatePluginServer(cfg PluginServerConfig) {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()
	m.pluginServers[cfg.PluginID] = cfg
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

// IsPluginRegistered reports whether an entry is backed by a source-plugin
// registration, including one restored from the KV store.
func (m *ClientManager) IsPluginRegistered(pluginID string) bool {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()
	return m.pluginRegistered[pluginID]
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
