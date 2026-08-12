// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// stubServerAccessChecker denies the listed server IDs and records calls.
type stubServerAccessChecker struct {
	denied map[string]bool
	calls  []string              // serverIDs, in call order
	hook   func(serverID string) // optional; runs after each recorded call
}

func (s *stubServerAccessChecker) CanUseMCPServer(_ context.Context, _, serverID string) error {
	s.calls = append(s.calls, serverID)
	if s.hook != nil {
		s.hook(serverID)
	}
	if s.denied[serverID] {
		return fmt.Errorf("server %s: denied", serverID)
	}
	return nil
}

const (
	accessAllowedOrigin = "https://mcp-allowed.example.com"
	accessDeniedOrigin  = "https://mcp-denied.example.com"
	accessNoIDOrigin    = "https://mcp-legacy-no-id.example.com"
	accessPluginID      = "com.example.mcp"
	accessPluginOrigin  = "plugin://" + accessPluginID

	accessAllowedID   = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	accessDeniedID    = "dddddddddddddddddddddddddd"
	accessEmbeddedID  = "eeeeeeeeeeeeeeeeeeeeeeeeee"
	accessPluginSrvID = "pppppppppppppppppppppppppp"
)

func accessTestConfig() Config {
	return Config{
		Servers: []ServerConfig{
			{ID: accessAllowedID, Name: "Allowed", Enabled: true, BaseURL: accessAllowedOrigin},
			{ID: accessDeniedID, Name: "Denied", Enabled: true, BaseURL: accessDeniedOrigin},
			{Name: "Legacy", Enabled: true, BaseURL: accessNoIDOrigin}, // no stable ID: not policy-addressable
			{ID: "cccccccccccccccccccccccccc", Name: "Disabled", Enabled: false, BaseURL: "https://mcp-disabled.example.com"},
		},
		EmbeddedServer: EmbeddedServerConfig{ID: accessEmbeddedID, Enabled: true},
		PluginServers: []PluginServerConfig{{
			ID: accessPluginSrvID, PluginID: accessPluginID, Name: "Plugin MCP", Enabled: true,
		}},
	}
}

func newAccessTestManager(t *testing.T, checker ServerAccessChecker) *ClientManager {
	t.Helper()
	mockAPI := &plugintest.API{}
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	client := pluginapi.NewClient(mockAPI, nil)

	m := &ClientManager{
		log:              client.Log,
		config:           accessTestConfig(),
		embeddedClient:   &EmbeddedServerClient{},
		pluginServers:    make(map[string]PluginServerConfig),
		pluginRegistered: make(map[string]bool),
	}
	m.pluginServers[accessPluginID] = PluginServerConfig{
		ID: accessPluginSrvID, PluginID: accessPluginID, Name: "Plugin MCP", Enabled: true, Path: "/mcp",
	}
	m.pluginRegistered[accessPluginID] = true
	if checker != nil {
		m.accessChecker = checker
	}
	return m
}

func TestDeniedExternalOriginsAndToolFiltering(t *testing.T) {
	tools := []llm.Tool{
		{Name: "allowed_tool", ServerOrigin: accessAllowedOrigin},
		{Name: "denied_tool_a", ServerOrigin: accessDeniedOrigin},
		{Name: "denied_tool_b", ServerOrigin: accessDeniedOrigin},
		{Name: "legacy_tool", ServerOrigin: accessNoIDOrigin},
		{Name: "embedded_tool", ServerOrigin: EmbeddedClientKey},
		{Name: "plugin_tool", ServerOrigin: accessPluginOrigin},
	}

	allNames := []string{"allowed_tool", "denied_tool_a", "denied_tool_b", "legacy_tool", "embedded_tool", "plugin_tool"}
	allGatedIDs := []string{accessAllowedID, accessDeniedID, accessEmbeddedID, accessPluginSrvID}

	tests := []struct {
		name       string
		checker    *stubServerAccessChecker
		nilChecker bool
		wantDenied map[string]bool
		wantNames  []string
		wantCalls  []string
	}{
		{
			name:       "denied remote server tools dropped silently, others untouched",
			checker:    &stubServerAccessChecker{denied: map[string]bool{accessDeniedID: true}},
			wantDenied: map[string]bool{accessDeniedOrigin: true},
			wantNames: []string{
				"allowed_tool", "legacy_tool", "embedded_tool", "plugin_tool",
			},
			wantCalls: allGatedIDs,
		},
		{
			name:       "denied embedded origin drops embedded tools",
			checker:    &stubServerAccessChecker{denied: map[string]bool{accessEmbeddedID: true}},
			wantDenied: map[string]bool{EmbeddedClientKey: true},
			wantNames: []string{
				"allowed_tool", "denied_tool_a", "denied_tool_b", "legacy_tool", "plugin_tool",
			},
			wantCalls: allGatedIDs,
		},
		{
			name:       "denied plugin origin drops plugin tools",
			checker:    &stubServerAccessChecker{denied: map[string]bool{accessPluginSrvID: true}},
			wantDenied: map[string]bool{accessPluginOrigin: true},
			wantNames: []string{
				"allowed_tool", "denied_tool_a", "denied_tool_b", "legacy_tool", "embedded_tool",
			},
			wantCalls: allGatedIDs,
		},
		{
			name:      "all allowed keeps everything",
			checker:   &stubServerAccessChecker{},
			wantNames: allNames,
			wantCalls: allGatedIDs,
		},
		{
			name:       "nil checker disables filtering",
			nilChecker: true,
			wantNames:  allNames,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var checker ServerAccessChecker
			if !tt.nilChecker {
				checker = tt.checker
			}
			m := newAccessTestManager(t, checker)

			denied := m.deniedExternalOrigins(context.Background(), "userid", m.ListPluginServers())
			if tt.wantDenied == nil {
				assert.Empty(t, denied)
			} else {
				assert.Equal(t, tt.wantDenied, denied)
			}

			var names []string
			for _, tool := range dropToolsFromDeniedOrigins(tools, denied) {
				names = append(names, tool.Name)
			}
			assert.Equal(t, tt.wantNames, names)

			if tt.checker != nil {
				assert.ElementsMatch(t, tt.wantCalls, tt.checker.calls)
			}
		})
	}
}

func TestDeniedExternalOriginsSkipsConfigOnlyPluginServers(t *testing.T) {
	checker := &stubServerAccessChecker{denied: map[string]bool{accessPluginSrvID: true}}
	m := newAccessTestManager(t, checker)

	// Config carries an orphan identity that never registered.
	const orphanID = "oooooooooooooooooooooooooo"
	m.config.PluginServers = append(m.config.PluginServers, PluginServerConfig{
		ID: orphanID, PluginID: "com.example.orphan", Name: "Orphan", Enabled: true,
	})

	denied := m.deniedExternalOrigins(context.Background(), "userid", m.ListPluginServers())
	assert.Equal(t, map[string]bool{accessPluginOrigin: true}, denied)
	assert.NotContains(t, checker.calls, orphanID, "config-only orphans must not be PDP-evaluated")
	assert.Contains(t, checker.calls, accessPluginSrvID)
}

// Mid-request registration must not enter the request-scoped snapshot used for
// ABAC, connect, filter, and response rendering.
func TestBuildUserToolsAccessPinsPluginSnapshotAcrossABAC(t *testing.T) {
	const (
		latePluginID = "com.example.late"
		lateServerID = "llllllllllllllllllllllllll"
		lateOrigin   = "plugin://" + latePluginID
	)

	checker := &stubServerAccessChecker{}
	m := newAccessTestManager(t, checker)
	m.clients = make(map[string]*UserClients)
	m.activity = make(map[string]time.Time)
	// Avoid remote/embedded connect side effects; plugin stays gated by ID.
	m.config.Servers = nil
	m.embeddedClient = nil
	ps := m.pluginServers[accessPluginID]
	ps.Enabled = false
	m.pluginServers[accessPluginID] = ps

	checker.hook = func(serverID string) {
		if serverID != accessPluginSrvID {
			return
		}
		// Simulate a registration that lands after the request snapshot.
		m.pluginServersMu.Lock()
		m.pluginServers[latePluginID] = PluginServerConfig{
			ID: lateServerID, PluginID: latePluginID, Name: "Late", Path: "/mcp", Enabled: true,
		}
		m.pluginRegistered[latePluginID] = true
		m.pluginServersMu.Unlock()
	}

	access := m.GetUserToolsAccess(context.Background(), "userid")

	require.Len(t, access.PluginServers, 1)
	assert.Equal(t, accessPluginID, access.PluginServers[0].PluginID)
	assert.NotContains(t, checker.calls, lateServerID, "late registration must not be PDP-evaluated")
	assert.False(t, access.DeniedOrigins[lateOrigin])
	for _, tool := range access.Tools {
		assert.NotEqual(t, lateOrigin, tool.ServerOrigin)
	}
	// Live registry did mutate; the response snapshot did not.
	_, ok := m.GetPluginServer(latePluginID)
	require.True(t, ok)
}

func TestRefreshUserToolsAccessEvaluatesABACOnce(t *testing.T) {
	checker := &stubServerAccessChecker{}
	m := newAccessTestManager(t, checker)
	m.clients = make(map[string]*UserClients)
	m.activity = make(map[string]time.Time)
	// Avoid remote/embedded/plugin connect side effects; plugin stays gated by ID.
	m.config.Servers = nil
	m.embeddedClient = nil
	ps := m.pluginServers[accessPluginID]
	ps.Enabled = false
	m.pluginServers[accessPluginID] = ps

	_, err := m.RefreshUserToolsAccess(context.Background(), "userid")
	require.NoError(t, err)

	assert.Equal(t, []string{accessPluginSrvID}, checker.calls,
		"refresh must evaluate ABAC once per gated server, not twice")
}

// A denied OAuth server with zero tools must not surface its ToolAuthError
// (silent omission); auth errors from allowed servers and origin-less generic
// errors survive.
func TestFilterErrorsByDeniedOrigins(t *testing.T) {
	deniedAuthErr := llm.ToolAuthError{
		ServerName:   "Denied",
		ServerOrigin: accessDeniedOrigin,
		AuthURL:      "https://denied.example.com/oauth",
		Error:        errors.New("oauth needed"),
	}
	allowedAuthErr := llm.ToolAuthError{
		ServerName:   "Allowed",
		ServerOrigin: accessAllowedOrigin,
		AuthURL:      "https://allowed.example.com/oauth",
		Error:        errors.New("oauth needed"),
	}
	genericErr := errors.New("connection refused")

	denied := map[string]bool{accessDeniedOrigin: true}

	tests := []struct {
		name          string
		in            *Errors
		denied        map[string]bool
		wantNil       bool
		wantAuthNames []string
		wantGeneric   int
	}{
		{
			name:    "oauth-only denied server leaves no artifacts",
			in:      &Errors{ToolAuthErrors: []llm.ToolAuthError{deniedAuthErr}},
			denied:  denied,
			wantNil: true,
		},
		{
			name:          "allowed auth errors and generic errors survive",
			in:            &Errors{ToolAuthErrors: []llm.ToolAuthError{deniedAuthErr, allowedAuthErr}, Errors: []error{genericErr}},
			denied:        denied,
			wantAuthNames: []string{"Allowed"},
			wantGeneric:   1,
		},
		{
			name:          "no denied origins passes through",
			in:            &Errors{ToolAuthErrors: []llm.ToolAuthError{deniedAuthErr}},
			denied:        nil,
			wantAuthNames: []string{"Denied"},
		},
		{
			name:    "nil errors stay nil",
			in:      nil,
			denied:  denied,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterErrorsByDeniedOrigins(tt.in, tt.denied)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			var names []string
			for _, authErr := range got.ToolAuthErrors {
				names = append(names, authErr.ServerName)
			}
			assert.Equal(t, tt.wantAuthNames, names)
			assert.Len(t, got.Errors, tt.wantGeneric)
		})
	}
}

// TestCreateAndStoreUserClientSkipsDeniedServers proves denied servers are
// never connected to: the denied origin is unreachable, so connecting to it
// would produce a generic connect error — none may appear.
func TestCreateAndStoreUserClientSkipsDeniedServers(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Maybe()
	client := pluginapi.NewClient(mockAPI, nil)

	manager := &ClientManager{
		config: Config{
			Servers: []ServerConfig{
				// Unreachable on purpose: any connect attempt errors immediately.
				{ID: accessDeniedID, Name: "Denied", Enabled: true, BaseURL: "http://127.0.0.1:1"},
			},
		},
		log:      client.Log,
		clients:  make(map[string]*UserClients),
		activity: make(map[string]time.Time),
	}

	userClients, mcpErrors := manager.createAndStoreUserClient(context.Background(), "user-1", false, map[string]bool{"http://127.0.0.1:1": true})
	require.NotNil(t, userClients)
	assert.Nil(t, mcpErrors, "a denied server must never be connected to, so it can produce no error artifacts")
}
