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
	config        Config
	log           pluginapi.LogService
	clientsMu     sync.RWMutex
	clients       map[string]*UserClients // userID to UserClients
	activity      map[string]time.Time    // userID to last activity time
	cleanupTicker *time.Ticker
	closeChan     chan struct{}
	clientTimeout time.Duration
	oauthManager  *OAuthManager
}

// NewClientManager creates a new MCP client manager
func NewClientManager(config Config, log pluginapi.LogService, pluginAPI *pluginapi.Client, oauthManager *OAuthManager) *ClientManager {
	manager := &ClientManager{
		log:          log,
		oauthManager: oauthManager,
	}
	manager.ReInit(config)
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

// ReInit re-initializes the client manager with a new configuration
func (m *ClientManager) ReInit(config Config) {
	m.Close()

	if config.IdleTimeoutMinutes <= 0 {
		config.IdleTimeoutMinutes = 30
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

	userClients := NewUserClients(userID, m.log, m.oauthManager)

	// Let user client connect to all servers
	mcpErrors := userClients.ConnectToAllServers(m.config.Servers)

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

// GetToolsForUser returns the tools available for a specific user
func (m *ClientManager) GetToolsForUser(userID string) ([]llm.Tool, *Errors) {
	// Get or create client for this user
	userClient, mcpErrors := m.getClientForUser(userID)

	// Return tools from successfully connected servers even if some failed
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
