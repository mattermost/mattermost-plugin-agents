// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/llm"
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
	embeddedClient *EmbeddedServerClient // Helper for embedded server (nil if disabled)
	toolsCache     *ToolsCache
}

// NewClientManager creates a new MCP client manager
// embeddedServer can be nil if embedded server is not available
func NewClientManager(config Config, log pluginapi.LogService, pluginAPI *pluginapi.Client, oauthManager *OAuthManager, embeddedServer EmbeddedMCPServer) *ClientManager {
	manager := &ClientManager{
		log:          log,
		pluginAPI:    pluginAPI,
		oauthManager: oauthManager,
		toolsCache:   NewToolsCache(&pluginAPI.KV, &log),
	}
	manager.ReInit(config, embeddedServer)
	return manager
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
}

// createAndStoreUserClient creates a new UserClients instance and stores it in the manager
func (m *ClientManager) createAndStoreUserClient(userID string) (*UserClients, *Errors) {
	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	// Check again in case another goroutine created the client while we were waiting for the lock
	client, exists := m.clients[userID]
	if exists {
		m.activity[userID] = time.Now()
		return client, nil
	}

	userClients := NewUserClients(userID, m.log, m.oauthManager, m.toolsCache)

	// Let user client connect to remote servers only
	mcpErrors := userClients.ConnectToRemoteServers(m.config.Servers)

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
		return client, nil
	}

	return m.createAndStoreUserClient(userID)
}

// GetToolsForUser returns the tools available for a specific user, connecting to embedded server if session ID provided
func (m *ClientManager) GetToolsForUser(userID string) ([]llm.Tool, *Errors) {
	// Get or create client for this user (connects to remote servers only)
	userClient, mcpErrors := m.getClientForUser(userID)

	// Connect to embedded server using a dedicated per-user session (stored/created in KV)
	if m.embeddedClient != nil && m.config.EmbeddedServer.Enabled {
		ensuredSessionID, ensureErr := m.ensureEmbeddedSessionID(userID)
		if ensureErr != nil {
			m.log.Debug("Failed to ensure embedded session for user", "userID", userID, "error", ensureErr)
		} else if ensuredSessionID != "" {
			if embeddedErr := userClient.ConnectToEmbeddedServerIfAvailable(ensuredSessionID, m.embeddedClient, m.config.EmbeddedServer); embeddedErr != nil {
				m.log.Debug("Failed to connect to embedded server for user", "userID", userID, "error", embeddedErr)
			}
		}
	}

	// Return tools from all connected servers (remote + embedded if connected)
	return userClient.GetTools(), mcpErrors
}

// ProcessOAuthCallback processes the OAuth callback for a user
func (m *ClientManager) ProcessOAuthCallback(ctx context.Context, userID, state, code string) (*OAuthSession, error) {
	session, err := m.oauthManager.ProcessCallback(ctx, userID, state, code)
	if err != nil {
		return nil, err
	}

	// Delete the client to force a re-creation
	m.clientsMu.Lock()
	delete(m.clients, userID)
	m.clientsMu.Unlock()

	return session, nil
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
