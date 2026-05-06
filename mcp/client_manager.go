// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var ErrOAuthNotConfigured = errors.New("oauth is not configured for this plugin")

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
}

// NewClientManager creates a new MCP client manager
// embeddedServer can be nil if embedded server is not available
func NewClientManager(config Config, log pluginapi.LogService, pluginAPI *pluginapi.Client, oauthManager *OAuthManager, embeddedServer EmbeddedMCPServer, httpClient *http.Client) *ClientManager {
	manager := &ClientManager{
		log:          log,
		pluginAPI:    pluginAPI,
		oauthManager: oauthManager,
		httpClient:   httpClient,
		toolsCache:   NewToolsCache(&pluginAPI.KV, &log),
	}
	manager.ReInit(config, embeddedServer)
	return manager
}

// EnsureMCPSessionID ensures there is a valid MCP session for the user
// This is used by both embedded and HTTP MCP servers to get a dedicated session
func (m *ClientManager) EnsureMCPSessionID(userID string) (string, error) {
	return m.ensureEmbeddedSessionID(userID)
}

// cleanupInactiveClients periodically checks for and closes inactive client connections
func (m *ClientManager) cleanupInactiveClients() {
	for {
		select {
		case <-m.cleanupTicker.C:
			m.clientsMu.Lock()
			now := time.Now()
			for userID, client := range m.clients {
				if now.Sub(m.activity[userID]) > m.clientTimeout {
					m.log.Debug("Closing inactive MCP client", "userID", userID)
					client.Close()
					delete(m.clients, userID)
					delete(m.activity, userID)
				}
			}
			m.clientsMu.Unlock()
		case <-m.closeChan:
			m.cleanupTicker.Stop()
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

	// Start cleanup ticker to remove inactive clients
	m.cleanupTicker = time.NewTicker(5 * time.Minute)
	go m.cleanupInactiveClients()
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
	m.activity = make(map[string]time.Time)
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
	m.activity[userID] = time.Now()

	return userClients, mcpErrors
}

// getClientForUser gets or creates an MCP client for a specific user
func (m *ClientManager) getClientForUser(userID string) (*UserClients, *Errors) {
	m.clientsMu.Lock()
	client, exists := m.clients[userID]
	if exists {
		m.activity[userID] = time.Now()
		m.clientsMu.Unlock()
		return client, client.initialRemoteConnectErrors
	}
	m.clientsMu.Unlock()

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

	// Return admin-filtered tools from all connected servers (remote + embedded if connected)
	rawTools := userClient.GetTools()
	filtered := filterToolsByConfig(rawTools, m.config, m.embeddedClient)
	return filtered, mcpErrors
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

// filterToolsByConfig filters raw discovered tools against the admin-configured
// tool policies. Only tools that have a matching ServerConfig entry with a
// ToolConfigs entry where enabled=true are returned. The result is ordered by
// configured server order, then alphabetically by tool name within each server.
//
// For the embedded server, if no explicit ToolConfigs are present, the vetted
// tool seed is used as the effective config.
func filterToolsByConfig(rawTools []llm.Tool, cfg Config, embeddedClient *EmbeddedServerClient) []llm.Tool {
	// Build a lookup: ServerOrigin (BaseURL) -> *ServerConfig
	serverByOrigin := make(map[string]*ServerConfig, len(cfg.Servers))
	serverOrder := make([]string, 0, len(cfg.Servers)+1)

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
