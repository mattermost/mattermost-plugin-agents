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

// ClientManager manages MCP clients for multiple users.
//
// Nested locks are always taken in the order lifecycleMu -> clientsMu ->
// pluginServersMu -> UserClients.clientsMu.
type ClientManager struct {
	// lifecycleMu is held exclusively by ReInit, Close, and the plugin
	// registry mutations, which publish new connection identities and detach
	// the sessions those identities invalidate. It is held shared while a
	// request snapshots the runtime, builds its connect tasks, and plans them,
	// so no plan can straddle an identity change. Client.Close and MCP network
	// work always happen after it is released.
	lifecycleMu    sync.RWMutex
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

	// admission caps overlapping remote/plugin connection sequences on this
	// manager instance. It outlives ReInit and is closed only by Close.
	admission *connectionAdmission
	// closed is set by Close and makes ReInit a no-op so shutdown stays permanent.
	closed bool
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
		admission:        newConnectionAdmission(maxNodeConnections),
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
			idle := make([]*UserClients, 0)
			for userID, client := range m.clients {
				if now.Sub(m.activity[userID]) > m.clientTimeout {
					m.log.Debug("Closing inactive MCP client", "userID", userID)
					idle = append(idle, client)
					delete(m.clients, userID)
					delete(m.activity, userID)
				}
			}
			m.clientsMu.Unlock()
			for _, client := range idle {
				client.Close()
			}
		case <-closeChan:
			ticker.Stop()
			return
		}
	}
}

// ReInit applies a new configuration and embedded server without discarding
// sessions whose connection identity is unchanged. Close is the only path that
// tears down every user client.
func (m *ClientManager) ReInit(config Config, embeddedServer EmbeddedMCPServer) {
	if config.IdleTimeoutMinutes <= 0 {
		config.IdleTimeoutMinutes = 30
	}

	var newEmbedded *EmbeddedServerClient
	if embeddedServer != nil {
		newEmbedded = NewEmbeddedServerClientWithCache(embeddedServer, m.log, m.pluginAPI, m.toolsCache)
	}

	m.lifecycleMu.Lock()
	m.clientsMu.Lock()
	if m.closed {
		m.clientsMu.Unlock()
		m.lifecycleMu.Unlock()
		return
	}

	m.config = config
	m.embeddedClient = newEmbedded
	m.clientTimeout = time.Duration(config.IdleTimeoutMinutes) * time.Minute

	if m.clients == nil {
		m.clients = make(map[string]*UserClients)
	}
	if m.activity == nil {
		m.activity = make(map[string]time.Time)
	}

	if m.closeChan == nil {
		m.closeChan = make(chan struct{})
		m.cleanupTicker = time.NewTicker(5 * time.Minute)
		go m.cleanupInactiveClients(m.closeChan, m.cleanupTicker)
	}
	m.clientsMu.Unlock()

	m.syncPluginServersFromConfig(config)

	valid := m.liveOriginIdentities(config, newEmbedded)
	var discarded []*Client
	for _, userClients := range m.snapshotUserClients() {
		discarded = append(discarded, userClients.detachInvalidIdentities(valid)...)
	}
	m.lifecycleMu.Unlock()
	closeDetachedClients(m.log, discarded)
}

// Close closes the client manager and all managed clients.
// The client manager should not be used after Close is called.
func (m *ClientManager) Close() {
	if m == nil {
		return
	}

	m.lifecycleMu.Lock()
	m.clientsMu.Lock()
	if m.closed {
		m.clientsMu.Unlock()
		m.lifecycleMu.Unlock()
		return
	}
	m.closed = true
	closeChan := m.closeChan
	ticker := m.cleanupTicker
	clients := m.clients
	m.closeChan = nil
	m.clients = make(map[string]*UserClients)
	m.activity = make(map[string]time.Time)
	m.clientsMu.Unlock()

	if closeChan != nil {
		close(closeChan)
	}
	if ticker != nil {
		ticker.Stop()
	}
	m.admission.close()
	m.lifecycleMu.Unlock()

	for _, client := range clients {
		client.Close()
	}
}

// getOrCreateUserClients returns the cached per-user client, creating and
// registering an empty one when this is the user's first request. Registration
// happens before any dialing so concurrent cold requests share one instance and
// therefore one session per server.
func (m *ClientManager) getOrCreateUserClients(userID string) *UserClients {
	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	if m.closed {
		return nil
	}

	if m.clients == nil {
		m.clients = make(map[string]*UserClients)
	}
	if m.activity == nil {
		m.activity = make(map[string]time.Time)
	}

	userClients, exists := m.clients[userID]
	if !exists {
		userClients = NewUserClients(userID, m.log, m.oauthManager, m.httpClient, m.toolsCache)
		userClients.admission = m.admission
		m.clients[userID] = userClients
	}
	m.activity[userID] = time.Now()

	return userClients
}

// eligibleServers is the per-request view of which MCP servers a user's tool
// construction may reach: admin-enabled servers intersected with the caller's
// selection, minus configuration conflicts that make a server ambiguous.
type eligibleServers struct {
	remote   []ServerConfig
	plugins  []PluginServerConfig
	embedded bool
	// origins holds every eligible origin, so discovered tools from a server
	// that is cached but no longer eligible are never handed to the LLM.
	origins map[string]bool
}

// snapshotRuntime returns the published config and embedded client. The Config
// is a shallow copy: its slices and maps belong to the configuration store and
// must be treated as read-only.
func (m *ClientManager) snapshotRuntime() (Config, *EmbeddedServerClient) {
	m.lifecycleMu.RLock()
	defer m.lifecycleMu.RUnlock()
	return m.config, m.embeddedClient
}

func (m *ClientManager) resolveEligibleServers(cfg Config, embeddedClient *EmbeddedServerClient, plugins []PluginServerConfig, selection ToolSelection) eligibleServers {
	resolved := eligibleServers{origins: make(map[string]bool)}

	// A duplicated name or endpoint makes every member of the group ambiguous:
	// they share a client-map key or a tools-cache entry, so none of them can
	// be safely picked. They stay out of the runtime until an admin fixes the
	// configuration; admin discovery reports the conflict.
	conflicting := make(map[int]bool)
	for _, conflict := range cfg.ServerConflicts() {
		conflicting[conflict.Index] = true
	}

	for i, server := range cfg.Servers {
		switch {
		case !server.Enabled || server.BaseURL == "":
			continue
		case conflicting[i]:
			m.log.Warn("Skipping MCP server with a duplicate name or URL; fix the MCP configuration to enable it",
				"serverID", server.Name, "serverOrigin", server.BaseURL)
			continue
		case !selection.Allows(server.BaseURL):
			continue
		}
		resolved.remote = append(resolved.remote, server)
		resolved.origins[llm.NormalizeMCPServerOrigin(server.BaseURL)] = true
	}

	if embeddedClient != nil && cfg.EmbeddedServer.Enabled && selection.Allows(EmbeddedClientKey) {
		resolved.embedded = true
		resolved.origins[EmbeddedClientKey] = true
	}

	for _, pluginCfg := range plugins {
		origin := pluginServerOriginKey(pluginCfg.PluginID)
		if !selection.Allows(origin) {
			continue
		}
		resolved.plugins = append(resolved.plugins, pluginCfg)
		resolved.origins[origin] = true
	}

	return resolved
}

// buildConnectTasks assembles one flat task list covering remote, embedded, and
// plugin servers. Keeping them in a single batch is the point: they dial
// concurrently instead of one category waiting on the previous one.
func (m *ClientManager) buildConnectTasks(ctx context.Context, userClients *UserClients, servers eligibleServers, embeddedClient *EmbeddedServerClient, forceRefresh bool, sessionID string, sessionErr error) []connectTask {
	tasks := make([]connectTask, 0, len(servers.remote)+len(servers.plugins)+1)

	// Remote dials outlive the request that starts them because they warm a
	// shared session cache; embedded and plugin dials keep the request context.
	for _, server := range servers.remote {
		tasks = append(tasks, userClients.remoteConnectTask(ctx, server, RemoteConnectTimeout, forceRefresh))
	}

	if servers.embedded {
		switch {
		case sessionErr != nil:
			m.log.Debug("Failed to ensure embedded session for user - embedded MCP tools will not be available",
				"userID", userClients.userID, "error", sessionErr)
		case userClients.needsEmbeddedReconnect(sessionID):
			tasks = append(tasks, userClients.embeddedConnectTask(ctx, sessionID, embeddedClient))
		}
	}

	for _, cfg := range servers.plugins {
		tasks = append(tasks, userClients.pluginConnectTask(ctx, cfg, pluginConnectTimeout, m.sourcePluginAPI))
	}

	return tasks
}

// GetToolsForUser returns the MCP tools a user may use for this operation, as
// narrowed by selection (see ToolSelection).
//
// Eligible servers this user has not connected yet are dialed now, in one
// concurrent batch, so a cold request costs roughly the slowest server rather
// than the sum of all of them. Servers outside the selection are never
// contacted and never contribute tools, even if an earlier request cached a
// session for one.
func (m *ClientManager) GetToolsForUser(ctx context.Context, userID string, selection ToolSelection) ([]llm.Tool, *Errors) {
	return m.getToolsForUser(ctx, userID, selection, false)
}

func (m *ClientManager) getToolsForUser(ctx context.Context, userID string, selection ToolSelection, forceRefresh bool) ([]llm.Tool, *Errors) {
	userClients := m.getOrCreateUserClients(userID)
	if userClients == nil {
		return nil, nil
	}

	m.lifecycleMu.RLock()
	if m.closed {
		m.lifecycleMu.RUnlock()
		return nil, nil
	}
	cfg := m.config
	embeddedClient := m.embeddedClient
	plugins := m.snapshotEnabledPluginServers()
	servers := m.resolveEligibleServers(cfg, embeddedClient, plugins, selection)
	var sessionID string
	var sessionErr error
	if servers.embedded {
		// Mattermost session lookup, not an MCP dial. Kept inside the
		// lifecycle read lock so task construction stays atomic with plan.
		sessionID, _, sessionErr = m.ensureEmbeddedSessionID(userID)
	}
	tasks := m.buildConnectTasks(ctx, userClients, servers, embeddedClient, forceRefresh, sessionID, sessionErr)
	plans, discarded := userClients.planConnections(tasks)
	m.lifecycleMu.RUnlock()

	closeDetachedClients(m.log, discarded)
	mcpErrors := userClients.executeConnections(ctx, plans)

	rawTools := userClients.GetTools(ctx)
	filtered := filterToolsByConfig(rawTools, cfg, embeddedClient, servers.plugins)
	return retainToolsFromOrigins(filtered, servers.origins), mcpErrors
}

// retainToolsFromOrigins drops tools whose server is not part of this
// operation's selection. A user client is a long-lived cache, so it can hold
// sessions for servers an agent switch has since made ineligible.
func retainToolsFromOrigins(tools []llm.Tool, origins map[string]bool) []llm.Tool {
	if len(tools) == 0 {
		return tools
	}

	retained := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		if origins[llm.NormalizeMCPServerOrigin(tool.ServerOrigin)] {
			retained = append(retained, tool)
		}
	}
	return retained
}

// RefreshToolsForUser drops the cached user client and shared server tool
// lists, then rediscovers every eligible server from scratch.
func (m *ClientManager) RefreshToolsForUser(ctx context.Context, userID string, selection ToolSelection) ([]llm.Tool, *Errors, error) {
	if userID == "" {
		return nil, nil, errors.New("userID is required")
	}

	if refreshErr := m.invalidateSharedToolsCacheForRefresh(); refreshErr != nil {
		m.log.Warn("Failed to invalidate shared MCP tools cache during user refresh; bypassing cache for rediscovery", "userID", userID, "error", refreshErr)
	}
	m.InvalidateUserClients(userID)

	tools, mcpErrors := m.getToolsForUser(ctx, userID, selection, true)
	return tools, mcpErrors, nil
}

func (m *ClientManager) invalidateSharedToolsCacheForRefresh() error {
	if m.toolsCache == nil {
		return nil
	}

	cfg, _ := m.snapshotRuntime()
	var refreshErr error
	for _, serverConfig := range cfg.Servers {
		if !serverConfig.Enabled || serverConfig.BaseURL == "" || !shouldUseSharedToolsCache(serverConfig) {
			continue
		}
		if err := m.toolsCache.InvalidateServer(serverConfig.Name); err != nil {
			refreshErr = errors.Join(refreshErr, fmt.Errorf("failed to invalidate tools cache for server %s: %w", serverConfig.Name, err))
		}
	}
	return refreshErr
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

	cfg, _ := m.snapshotRuntime()
	for _, server := range cfg.Servers {
		if !server.Enabled {
			continue
		}
		for _, toolConfig := range server.ToolConfigs {
			addOverride(server.BaseURL, toolConfig)
		}
	}

	for _, toolConfig := range cfg.EmbeddedServer.ToolConfigs {
		addOverride(EmbeddedClientKey, toolConfig)
	}

	for _, server := range cfg.PluginServers {
		if !server.Enabled || server.PluginID == "" {
			continue
		}
		for _, toolConfig := range server.ToolConfigs {
			addOverride(pluginServerOriginKey(server.PluginID), toolConfig)
		}
	}

	return overrides
}

func (m *ClientManager) snapshotEnabledPluginServers() []PluginServerConfig {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()

	enabled := make([]PluginServerConfig, 0, len(m.pluginServers))
	for _, cfg := range m.pluginServers {
		if cfg.Enabled && m.pluginRegistered[cfg.PluginID] {
			enabled = append(enabled, cfg)
		}
	}
	sort.Slice(enabled, func(i, j int) bool {
		return enabled[i].PluginID < enabled[j].PluginID
	})
	return enabled
}

// InvalidateUserClients closes and removes cached MCP clients for a user.
func (m *ClientManager) InvalidateUserClients(userID string) {
	if userID == "" {
		return
	}

	m.clientsMu.Lock()
	uc := m.clients[userID]
	delete(m.clients, userID)
	delete(m.activity, userID)
	m.clientsMu.Unlock()

	if uc != nil {
		uc.Close()
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
	_, embeddedClient := m.snapshotRuntime()
	if embeddedClient == nil {
		return nil
	}
	return embeddedClient.server
}

// GetHTTPClient returns the HTTP client for upstream requests
func (m *ClientManager) GetHTTPClient() *http.Client {
	return m.httpClient
}

// GetConfig returns a snapshot of the current MCP configuration.
func (m *ClientManager) GetConfig() Config {
	cfg, _ := m.snapshotRuntime()
	return cfg
}

// liveOriginIdentities is the connection-identity map ReInit uses to decide
// which cached sessions remain valid. Tool policies are not part of identity.
func (m *ClientManager) liveOriginIdentities(cfg Config, embeddedClient *EmbeddedServerClient) map[string]originIdentity {
	identities := remoteOriginIdentities(cfg)

	var embeddedServer EmbeddedMCPServer
	if embeddedClient != nil {
		embeddedServer = embeddedClient.server
	}
	identities[EmbeddedClientKey] = embeddedOriginIdentity(embeddedServer, cfg.EmbeddedServer.Enabled)

	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()
	for pluginID := range m.pluginServers {
		if pluginID == "" {
			continue
		}
		identities[pluginServerOriginKey(pluginID)] = m.pluginIdentityLocked(pluginID)
	}
	return identities
}

// pluginIdentityLocked reports the live identity of one plugin origin. A
// config-only row is never contacted, so it has the same zero identity as an
// absent one: no cached session can match it. Callers hold pluginServersMu.
func (m *ClientManager) pluginIdentityLocked(pluginID string) originIdentity {
	if !m.pluginRegistered[pluginID] {
		return originIdentity{}
	}
	return pluginOriginIdentity(m.pluginServers[pluginID])
}

// RegisterPluginServer stores or overwrites a plugin-server registration.
// Callers must ensure cfg.PluginID is non-empty. Identity-affecting changes
// (name, path, enabled, registration) invalidate that origin immediately;
// ToolConfigs and ExposeExternal alone do not.
func (m *ClientManager) RegisterPluginServer(cfg PluginServerConfig) {
	m.updatePluginRegistry(cfg.PluginID, func() {
		m.pluginServers[cfg.PluginID] = cfg
		m.pluginRegistered[cfg.PluginID] = true
		m.mutatePersistedPluginRegistrations(func(registrations map[string]PluginServerConfig) {
			registrations[cfg.PluginID] = cfg
		})
	})
}

// UpdatePluginServer applies admin-owned fields without changing registration state.
func (m *ClientManager) UpdatePluginServer(cfg PluginServerConfig) {
	m.updatePluginRegistry(cfg.PluginID, func() {
		m.pluginServers[cfg.PluginID] = cfg
	})
}

func (m *ClientManager) UnregisterPluginServer(pluginID string) {
	m.updatePluginRegistry(pluginID, func() {
		delete(m.pluginServers, pluginID)
		delete(m.pluginRegistered, pluginID)
		m.mutatePersistedPluginRegistrations(func(registrations map[string]PluginServerConfig) {
			delete(registrations, pluginID)
		})
	})
}

// updatePluginRegistry applies mutate to the plugin registry and drops every
// cached session for that origin when the change altered its connection
// identity. mutate runs under pluginServersMu and must not do network work;
// the detached sessions are closed after every lock is released.
func (m *ClientManager) updatePluginRegistry(pluginID string, mutate func()) {
	if m == nil || pluginID == "" {
		return
	}

	m.lifecycleMu.Lock()
	if m.closed {
		m.lifecycleMu.Unlock()
		return
	}

	m.pluginServersMu.Lock()
	if m.pluginServers == nil {
		m.pluginServers = make(map[string]PluginServerConfig)
	}
	if m.pluginRegistered == nil {
		m.pluginRegistered = make(map[string]bool)
	}
	before := m.pluginIdentityLocked(pluginID)
	mutate()
	after := m.pluginIdentityLocked(pluginID)
	m.pluginServersMu.Unlock()

	var discarded []*Client
	if before != after {
		origin := pluginServerOriginKey(pluginID)
		for _, userClients := range m.snapshotUserClients() {
			discarded = append(discarded, userClients.detachOrigins(origin)...)
		}
	}
	m.lifecycleMu.Unlock()
	closeDetachedClients(m.log, discarded)
}

func (m *ClientManager) snapshotUserClients() []*UserClients {
	m.clientsMu.RLock()
	defer m.clientsMu.RUnlock()
	if len(m.clients) == 0 {
		return nil
	}
	users := make([]*UserClients, 0, len(m.clients))
	for _, userClients := range m.clients {
		users = append(users, userClients)
	}
	return users
}

// ListPluginServers returns a stable snapshot without holding the registry lock
// during caller work.
func (m *ClientManager) ListPluginServers() []PluginServerConfig {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()

	out := make([]PluginServerConfig, 0, len(m.pluginServers))
	for _, cfg := range m.pluginServers {
		out = append(out, cfg)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PluginID < out[j].PluginID
	})
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
	if m.pluginAPI == nil {
		return
	}
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

	if m.pluginServers == nil {
		m.pluginServers = make(map[string]PluginServerConfig)
	}
	if m.pluginRegistered == nil {
		m.pluginRegistered = make(map[string]bool)
	}

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
