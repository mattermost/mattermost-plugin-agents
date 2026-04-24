// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

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

	// Plugin-server registry (see PluginServerConfig). Populated by the bridge
	// /mcp/register endpoint (Phase 1E) and consumed by GetToolsForUser's
	// third connect loop + filterToolsByConfig's synthetic-entry injection.
	// Protected by pluginServersMu — never held across an HTTP round trip
	// (use the snapshot+unlock pattern in GetToolsForUser).
	pluginServersMu sync.RWMutex
	pluginServers   map[string]PluginServerConfig // keyed by PluginID
	// sourcePluginAPI supplies PluginHTTP for PluginHTTPRoundTrippers built in
	// ConnectToPluginServer. It is the agents-plugin mmapi.Client from main.go.
	sourcePluginAPI mmapi.Client
}

// NewClientManager creates a new MCP client manager.
// embeddedServer may be nil if the embedded server is not available.
// sourcePluginAPI is the agents-plugin mmapi.Client used to route PluginHTTP
// requests to source plugins registered via the bridge /mcp/register endpoint.
func NewClientManager(config Config, log pluginapi.LogService, pluginAPI *pluginapi.Client, oauthManager *OAuthManager, embeddedServer EmbeddedMCPServer, httpClient *http.Client, sourcePluginAPI mmapi.Client) *ClientManager {
	manager := &ClientManager{
		log:             log,
		pluginAPI:       pluginAPI,
		oauthManager:    oauthManager,
		httpClient:      httpClient,
		toolsCache:      NewToolsCache(&pluginAPI.KV, &log),
		pluginServers:   make(map[string]PluginServerConfig),
		sourcePluginAPI: sourcePluginAPI,
	}
	manager.ReInit(config, embeddedServer)
	return manager
}

// EnsureMCPSessionID ensures there is a valid MCP session for the user
// This is used by both embedded and HTTP MCP servers to get a dedicated session
func (m *ClientManager) EnsureMCPSessionID(userID string) (string, error) {
	return m.ensureEmbeddedSessionID(userID)
}

// cleanupInactiveClients periodically checks for and closes inactive client connections.
// closeChan and ticker are passed in so the goroutine captures the values at launch time
// instead of reading m.closeChan / m.cleanupTicker on every iteration — those fields
// are mutated by Close() + ReInit() on the caller's goroutine and that would race
// with the select expression evaluation here.
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
		m.embeddedClient = NewEmbeddedServerClient(embeddedServer, m.log, m.pluginAPI)
	} else {
		m.embeddedClient = nil
	}

	m.config = config
	m.clients = make(map[string]*UserClients)
	m.clientTimeout = time.Duration(config.IdleTimeoutMinutes) * time.Minute
	m.closeChan = make(chan struct{})
	m.activity = make(map[string]time.Time)

	// Start cleanup ticker to remove inactive clients. The ticker and close
	// channel are passed in so the goroutine captures them at launch time
	// — avoids a race with Close()/ReInit() reassigning m.closeChan and
	// m.cleanupTicker later.
	m.cleanupTicker = time.NewTicker(5 * time.Minute)
	go m.cleanupInactiveClients(m.closeChan, m.cleanupTicker)
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

// createAndStoreUserClient creates a new UserClients instance and stores it in the manager
func (m *ClientManager) createAndStoreUserClient(userID string) (*UserClients, *Errors) {
	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	// Check again in case another goroutine created the client while we were waiting for the lock
	client, exists := m.clients[userID]
	if exists {
		m.activity[userID] = time.Now()
		return client, client.initialRemoteConnectErrors
	}

	userClients := NewUserClients(userID, m.log, m.oauthManager, m.httpClient, m.toolsCache)

	// Let user client connect to remote servers only
	mcpErrors := userClients.ConnectToRemoteServers(m.config.Servers)
	userClients.initialRemoteConnectErrors = mcpErrors

	// Store the client even if some servers failed to connect
	// This allows partial success - user gets tools from working servers
	m.clients[userID] = userClients

	return userClients, mcpErrors
}

// getClientForUser gets or creates an MCP client for a specific user
func (m *ClientManager) getClientForUser(userID string) (*UserClients, *Errors) {
	m.clientsMu.RLock()
	client, exists := m.clients[userID]
	m.clientsMu.RUnlock()
	if exists {
		m.activity[userID] = time.Now()
		return client, client.initialRemoteConnectErrors
	}

	return m.createAndStoreUserClient(userID)
}

// GetToolsForUser returns the tools available for a specific user, connecting to embedded server if session ID provided
func (m *ClientManager) GetToolsForUser(userID string) ([]llm.Tool, *Errors) {
	// Get or create client for this user (connects to remote servers only)
	userClient, mcpErrors := m.getClientForUser(userID)

	// Connect to embedded server using a dedicated per-user session (stored/created in KV)
	if m.embeddedClient != nil {
		ensuredSessionID, ensureErr := m.ensureEmbeddedSessionID(userID)
		if ensureErr != nil {
			m.log.Debug("Failed to ensure embedded session for user - embedded MCP tools will not be available", "userID", userID, "error", ensureErr)
		} else if ensuredSessionID != "" {
			if embeddedErr := userClient.ConnectToEmbeddedServerIfAvailable(ensuredSessionID, m.embeddedClient, m.config.EmbeddedServer); embeddedErr != nil {
				m.log.Debug("Failed to connect to embedded server for user - embedded MCP tools will not be available", "userID", userID, "sessionID", ensuredSessionID, "error", embeddedErr)
			}
		}
	}

	// Connect to every enabled plugin-registered MCP server.
	// Snapshot under RLock then release before any HTTP work — the lock must
	// NEVER be held across ConnectToPluginServer (which does PluginHTTP round
	// trips that can block). See "Lock Contention Analysis" in the plan.
	pluginSnap := m.snapshotEnabledPluginServers()
	for _, cfg := range pluginSnap {
		if connectErr := userClient.ConnectToPluginServer(context.TODO(), cfg, m.sourcePluginAPI); connectErr != nil {
			m.log.Error("Failed to connect to plugin MCP server", "userID", userID, "pluginID", cfg.PluginID, "error", connectErr)
			if mcpErrors == nil {
				mcpErrors = &Errors{}
			}
			mcpErrors.Errors = append(mcpErrors.Errors, connectErr)
			// Pin to initialRemoteConnectErrors so subsequent cached lookups
			// keep surfacing the failure (matches the remote-server lane at
			// mcp/client_manager.go:142 and downstream consumer at
			// llmcontext/llm_context.go:200-227 which treats mcpErrors.Errors
			// as opaque — no split needed).
			userClient.initialRemoteConnectErrors = mcpErrors
		}
	}

	// Return admin-filtered tools from all connected servers (remote + embedded + plugin).
	rawTools := userClient.GetTools()
	filtered := filterToolsByConfig(rawTools, m.config, m.embeddedClient, pluginSnap)
	return filtered, mcpErrors
}

// snapshotEnabledPluginServers copies the enabled plugin-server configs out
// from under pluginServersMu so callers can iterate without holding the lock.
// Required because the iteration includes HTTP round trips and admin
// Register/Unregister operations must not be serialized behind connect latency.
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

// ProcessOAuthCallback processes the OAuth callback for a user
func (m *ClientManager) ProcessOAuthCallback(ctx context.Context, userID, state, code string) (*OAuthSession, error) {
	session, err := m.oauthManager.ProcessCallback(ctx, userID, state, code)
	if err != nil {
		return nil, err
	}

	// Delete the client to force a re-creation (close first, like DisconnectUserOAuth).
	m.clientsMu.Lock()
	if uc, ok := m.clients[userID]; ok {
		uc.Close()
		delete(m.clients, userID)
	}
	m.clientsMu.Unlock()

	return session, nil
}

// DisconnectUserOAuth removes the stored OAuth token for a user and server,
// and invalidates the cached MCP client so a fresh connection is established
// on the next request.
func (m *ClientManager) DisconnectUserOAuth(userID, serverName string) error {
	if err := m.oauthManager.DeleteUserToken(userID, serverName); err != nil {
		return err
	}

	m.clientsMu.Lock()
	if uc, ok := m.clients[userID]; ok {
		uc.Close()
		delete(m.clients, userID)
	}
	m.clientsMu.Unlock()

	return nil
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

// RegisterPluginServer stores (or overwrites) a plugin-server registration.
// Called by the bridge /mcp/register handler in Phase 1E. Overwrite semantics
// match the intended "re-register on plugin OnActivate" behavior — a restarted
// source plugin should reset the stored config without the admin having to
// intervene. cfg.PluginID must be non-empty (callers validate).
func (m *ClientManager) RegisterPluginServer(cfg PluginServerConfig) {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()
	m.pluginServers[cfg.PluginID] = cfg
}

// UnregisterPluginServer removes a plugin-server registration. No-op if the
// pluginID is not registered. Called by the bridge /mcp/unregister handler
// (Phase 1E) when a source plugin deactivates.
func (m *ClientManager) UnregisterPluginServer(pluginID string) {
	m.pluginServersMu.Lock()
	defer m.pluginServersMu.Unlock()
	delete(m.pluginServers, pluginID)
}

// ListPluginServers returns a snapshot (value copy) of every registered
// plugin-server config. Safe to iterate without holding any lock.
// Used by the admin Tools tab (Phase 1F) and external-aggregation rebuild
// (Phase 1G).
func (m *ClientManager) ListPluginServers() []PluginServerConfig {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()
	out := make([]PluginServerConfig, 0, len(m.pluginServers))
	for _, cfg := range m.pluginServers {
		out = append(out, cfg)
	}
	return out
}

// GetPluginServer returns a value-copy of the stored config for pluginID plus
// a found flag. Used by the bridge /mcp/register handler so re-registrations
// from a plugin (e.g. after an OnActivate following a crash) can preserve
// admin-set fields (Enabled / ExposeExternal) rather than clobbering them
// with the plugin's self-declared defaults.
func (m *ClientManager) GetPluginServer(pluginID string) (PluginServerConfig, bool) {
	m.pluginServersMu.RLock()
	defer m.pluginServersMu.RUnlock()
	cfg, ok := m.pluginServers[pluginID]
	return cfg, ok
}

// DiscoverPluginServerTools performs an ephemeral connect+ListTools against
// the given plugin-registered MCP server and returns its tool list. Used by
// the admin Tools tab (Phase 1F); not cached. For per-user cached tool access
// see UserClients.ConnectToPluginServer.
func (m *ClientManager) DiscoverPluginServerTools(ctx context.Context, userID string, cfg PluginServerConfig) ([]ToolInfo, error) {
	return DiscoverPluginServerTools(ctx, userID, cfg, m.sourcePluginAPI, m.log)
}

// filterToolsByConfig filters raw discovered tools against the admin-configured
// tool policies. Only tools that have a matching ServerConfig entry with a
// ToolConfigs entry where enabled=true are returned. The result is ordered by
// configured server order, then alphabetically by tool name within each server.
//
// For the embedded server, if no explicit ToolConfigs are present, the vetted
// tool seed is used as the effective config.
//
// Plugin-registered servers flow through the same filter via synthetic
// ServerConfig entries keyed by "plugin://<pluginID>". Their ToolConfigs are
// intentionally left empty so MCPServerConfig.GetToolPolicy returns
// ("ask", true) for every tool — default-allow, matching the
// "unconfigured tool defaults to enabled" case in
// mcp/client_manager_filter_test.go. Admin-side enable/disable is handled at
// the plugin-server level (PluginServerConfig.Enabled): disabled entries are
// skipped here, which drops every tool they carried, mirroring remote-server
// semantics at lines 264-271.
func filterToolsByConfig(rawTools []llm.Tool, cfg Config, embeddedClient *EmbeddedServerClient, pluginServers []PluginServerConfig) []llm.Tool {
	// Build a lookup: ServerOrigin (BaseURL) -> *ServerConfig
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

	// Handle embedded server
	if embeddedClient != nil {
		embeddedCfg := &ServerConfig{
			Name:    EmbeddedServerName,
			Enabled: true,
			BaseURL: EmbeddedClientKey,
		}
		// Use persisted tool configs if present, otherwise fall back to vetted seed
		if len(cfg.EmbeddedServer.ToolConfigs) > 0 {
			embeddedCfg.ToolConfigs = cfg.EmbeddedServer.ToolConfigs
		} else {
			embeddedCfg.ToolConfigs = SeedVettedToolConfigs(EmbeddedClientKey)
		}
		serverByOrigin[EmbeddedClientKey] = embeddedCfg
		serverOrder = append(serverOrder, EmbeddedClientKey)
	}

	// Plugin-registered servers (default-allow via empty ToolConfigs).
	for _, ps := range pluginServers {
		if !ps.Enabled {
			continue
		}
		origin := "plugin://" + ps.PluginID
		serverByOrigin[origin] = &ServerConfig{
			Name:    ps.Name,
			Enabled: true,
			BaseURL: origin,
			// ToolConfigs intentionally empty -> GetToolPolicy returns ("ask", true).
		}
		serverOrder = append(serverOrder, origin)
	}

	// Group raw tools by ServerOrigin
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

		// Filter: only tools with enabled config entries
		var filtered []llm.Tool
		for _, t := range tools {
			_, enabled := sc.GetToolPolicy(t.Name)
			if enabled {
				filtered = append(filtered, t)
			}
		}

		// Sort by tool name for deterministic output
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].Name < filtered[j].Name
		})

		result = append(result, filtered...)
	}

	return result
}
