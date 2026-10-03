// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"errors"
	"net/http"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
)

// ServerFactory constructs a bound sandbox Server. Injectable for tests.
type ServerFactory func(listenAddr, hostOrigin string, logger Logger) (*Server, error)

// ConfigSource supplies the current apps config and Site URL. Called only
// while the Manager holds its mutex (serialized section).
type ConfigSource func() (apps config.MCPAppsConfig, siteURL string)

// Manager owns the sandbox listener lifecycle with a serialized
// ApplyCurrent / Close state machine. Close is terminal: late ApplyCurrent
// calls after Close are no-ops.
type Manager struct {
	mu         sync.Mutex
	closed     bool
	server     *Server
	addr       string
	hostOrigin string
	factory    ServerFactory
	config     ConfigSource
	logger     Logger
}

// NewManager constructs a Manager. factory may be nil (defaults to NewServer).
func NewManager(config ConfigSource, logger Logger, factory ServerFactory) *Manager {
	if factory == nil {
		factory = NewServer
	}
	return &Manager{
		factory: factory,
		config:  config,
		logger:  logger,
	}
}

// ApplyCurrent (re)starts or stops the listener from the current config
// source. Safe to call concurrently and repeatedly. No-op when closed or
// when the desired listen address and host origin already match.
func (m *Manager) ApplyCurrent() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return
	}

	apps, siteURL := m.config()
	resolved := Resolve(apps, siteURL)
	addr, hostOrigin, enabled := ListenSpecFromResolved(resolved)

	if enabled && m.server != nil && m.addr == addr && m.hostOrigin == hostOrigin {
		return
	}

	m.stopLocked()

	if !enabled {
		return
	}

	server, err := m.factory(addr, hostOrigin, m.logger)
	if err != nil {
		if m.logger != nil {
			m.logger.Error("MCP Apps sandbox: failed to start listener; apps sandbox serving is disabled on this node until the configuration changes", "listen_address", addr, "error", err)
		}
		return
	}
	m.server = server
	m.addr = addr
	m.hostOrigin = hostOrigin

	go m.serve(server)

	if m.logger != nil {
		m.logger.Info("MCP Apps sandbox server listening", "listen_address", server.Addr(), "host_origin", hostOrigin)
	}
}

// Close stops the listener and marks the manager terminal. Subsequent
// ApplyCurrent calls are ignored.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.stopLocked()
}

// Running reports whether a listener is currently owned (for tests).
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.server != nil
}

// Addr returns the bound address of the current listener, or "".
func (m *Manager) Addr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server == nil {
		return ""
	}
	return m.server.Addr()
}

func (m *Manager) stopLocked() {
	if m.server == nil {
		return
	}
	if err := m.server.Shutdown(); err != nil && m.logger != nil {
		m.logger.Warn("MCP Apps sandbox: error shutting down previous server", "error", err)
	}
	m.server = nil
	m.addr = ""
	m.hostOrigin = ""
}

func (m *Manager) serve(server *Server) {
	err := server.Run()
	if err != nil && !errors.Is(err, http.ErrServerClosed) && m.logger != nil {
		m.logger.Error("MCP Apps sandbox server exited", "error", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Clear state only if this server is still the current one (unexpected exit).
	if m.server == server {
		m.server = nil
		m.addr = ""
		m.hostOrigin = ""
	}
}
