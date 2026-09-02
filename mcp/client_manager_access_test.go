// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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
	users  []string              // userIDs, in call order
	hook   func(serverID string) // optional; runs after each recorded call
}

func (s *stubServerAccessChecker) CanUseMCPServer(_ context.Context, userID, serverID string) error {
	s.calls = append(s.calls, serverID)
	s.users = append(s.users, userID)
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

func TestDeniedMCPServerOriginsAndToolFiltering(t *testing.T) {
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

			plugins := m.snapshotEnabledPluginServers()
			denied := m.deniedMCPServerOrigins(context.Background(), "userid", m.config, m.embeddedClient, plugins)
			if tt.wantDenied == nil {
				assert.Empty(t, denied)
			} else {
				assert.Equal(t, tt.wantDenied, denied)
			}

			servers := m.resolveEligibleServers(m.config, m.embeddedClient, plugins, ToolSelection{}, denied, false)
			var names []string
			for _, tool := range retainToolsFromOrigins(tools, servers.origins) {
				names = append(names, tool.Name)
			}
			assert.Equal(t, tt.wantNames, names)

			if tt.checker != nil {
				assert.ElementsMatch(t, tt.wantCalls, tt.checker.calls)
			}
		})
	}
}

func TestDeniedMCPServerOriginsSkipsConfigOnlyPluginServers(t *testing.T) {
	checker := &stubServerAccessChecker{denied: map[string]bool{accessPluginSrvID: true}}
	m := newAccessTestManager(t, checker)

	// Config carries an orphan identity that never registered.
	const orphanID = "oooooooooooooooooooooooooo"
	m.config.PluginServers = append(m.config.PluginServers, PluginServerConfig{
		ID: orphanID, PluginID: "com.example.orphan", Name: "Orphan", Enabled: true,
	})

	plugins := m.snapshotEnabledPluginServers()
	denied := m.deniedMCPServerOrigins(context.Background(), "userid", m.config, m.embeddedClient, plugins)
	assert.Equal(t, map[string]bool{accessPluginOrigin: true}, denied)
	assert.NotContains(t, checker.calls, orphanID, "config-only orphans must not be PDP-evaluated")
	assert.Contains(t, checker.calls, accessPluginSrvID)
}

// Mid-request registration must not enter the request-scoped snapshot used for
// ABAC, connect, filter, and response rendering.
func TestGetCatalogAccessPinsPluginSnapshotAcrossABAC(t *testing.T) {
	const (
		latePluginID = "com.example.late"
		lateServerID = "llllllllllllllllllllllllll"
		lateOrigin   = "plugin://" + latePluginID
	)

	checker := &stubServerAccessChecker{denied: map[string]bool{accessPluginSrvID: true}}
	m := newAccessTestManager(t, checker)
	m.clients = make(map[clientKey]*UserClients)
	m.activity = make(map[clientKey]time.Time)
	// Avoid remote/embedded connect side effects; policy denies the plugin
	// before connection planning.
	m.config.Servers = nil
	m.embeddedClient = nil

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

	access := m.GetCatalogAccess(context.Background(), UserCatalogRequest("userid"))

	require.Len(t, access.PluginServers, 1)
	assert.Equal(t, accessPluginID, access.PluginServers[0].PluginID)
	assert.NotContains(t, checker.calls, lateServerID, "late registration must not be PDP-evaluated")
	assert.True(t, access.DeniedOrigins[accessPluginOrigin])
	assert.False(t, access.DeniedOrigins[lateOrigin])
	for _, tool := range access.Tools {
		assert.NotEqual(t, lateOrigin, tool.ServerOrigin)
	}
	// Live registry did mutate; the response snapshot did not.
	_, ok := m.GetPluginServer(latePluginID)
	require.True(t, ok)
}

func TestRefreshCatalogAccessEvaluatesABACOnce(t *testing.T) {
	checker := &stubServerAccessChecker{denied: map[string]bool{accessPluginSrvID: true}}
	m := newAccessTestManager(t, checker)
	m.clients = make(map[clientKey]*UserClients)
	m.activity = make(map[clientKey]time.Time)
	// Avoid remote/embedded connect side effects; policy denies the plugin
	// before connection planning.
	m.config.Servers = nil
	m.embeddedClient = nil

	access, err := m.RefreshCatalogAccess(context.Background(), UserCatalogRequest("userid"))
	require.NoError(t, err)

	assert.True(t, access.DeniedOrigins[accessPluginOrigin])
	assert.Equal(t, []string{accessPluginSrvID}, checker.calls,
		"refresh must evaluate ABAC once per gated server, not twice")
}

// TestGetCatalogAccessSkipsDeniedServers proves denied servers are
// never connected to: the denied origin is unreachable, so connecting to it
// would produce a generic connect error — none may appear.
func TestGetCatalogAccessSkipsDeniedServers(t *testing.T) {
	var requests atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(httpServer.Close)

	checker := &stubServerAccessChecker{denied: map[string]bool{accessDeniedID: true}}
	manager := newRemoteAccessTestManager(t, []ServerConfig{{
		ID: accessDeniedID, Name: "Denied", Enabled: true, BaseURL: httpServer.URL,
	}}, checker, httpServer.Client())

	access := manager.GetCatalogAccess(context.Background(), UserCatalogRequest("user-1"))
	assert.True(t, access.DeniedOrigins[httpServer.URL])
	assert.Nil(t, access.Errors, "a denied server must never be connected to, so it can produce no error artifacts")
	assert.Zero(t, requests.Load())
}

func newRemoteAccessTestManager(t *testing.T, servers []ServerConfig, checker ServerAccessChecker, httpClient *http.Client) *ClientManager {
	t.Helper()
	mockAPI := &plugintest.API{}
	setupTestLogger(mockAPI)
	client := pluginapi.NewClient(mockAPI, nil)
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &ClientManager{
		log:           client.Log,
		config:        Config{Servers: servers},
		clients:       make(map[clientKey]*UserClients),
		activity:      make(map[clientKey]time.Time),
		httpClient:    httpClient,
		accessChecker: checker,
	}
}

func hasToolFromOrigin(tools []llm.Tool, origin string) bool {
	for _, tool := range tools {
		if tool.ServerOrigin == origin {
			return true
		}
	}
	return false
}

// TestGetCatalogAccessDoesNotRedialFailedRemote proves an allowed remote
// whose connect fails is not re-dialed on cache hits and its error is not
// re-appended on every request.
func TestGetCatalogAccessDoesNotRedialFailedRemote(t *testing.T) {
	ctx := context.Background()
	const userID = "user-1"
	req := UserCatalogRequest(userID)

	var requests atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(httpServer.Close)

	checker := &stubServerAccessChecker{denied: map[string]bool{}}
	m := newRemoteAccessTestManager(t, []ServerConfig{{
		ID: accessAllowedID, Name: "Down", Enabled: true, BaseURL: httpServer.URL,
	}}, checker, httpServer.Client())
	t.Cleanup(func() {
		if uc := m.clients[req.remoteKey()]; uc != nil {
			uc.Close()
		}
	})

	first := m.GetCatalogAccess(ctx, req)
	require.NotNil(t, first.Errors)
	require.Len(t, first.Errors.Errors, 1)
	cached := m.clients[req.remoteKey()]
	require.NotNil(t, cached)
	dialsAfterFirst := requests.Load()
	require.Positive(t, dialsAfterFirst)

	for i := 0; i < 3; i++ {
		again := m.GetCatalogAccess(ctx, req)
		require.NotNil(t, again.Errors)
		assert.Len(t, again.Errors.Errors, 1, "connect error must not accumulate across cache hits")
		assert.Same(t, cached, m.clients[req.remoteKey()])
		assert.Equal(t, dialsAfterFirst, requests.Load(), "cache hit must not re-dial a failed remote")
	}
}

func TestGetCatalogAccessDeltaConnectsNewlyAllowedRemote(t *testing.T) {
	ctx := context.Background()
	const userID = "user-1"
	const remoteName = "RemoteA"
	req := UserCatalogRequest(userID)

	tests := []struct {
		name              string
		realMCP           bool
		wantToolAfter     bool
		wantErrorsAfter   bool
		thenDenyDropsTool bool
	}{
		{
			name:              "deny then allow connects remote without refresh",
			realMCP:           true,
			wantToolAfter:     true,
			thenDenyDropsTool: true,
		},
		{
			name:            "deny then allow stores connect errors on cache hit",
			wantErrorsAfter: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origin := "http://127.0.0.1:1"
			var httpClient *http.Client
			if tt.realMCP {
				httpServer := startStreamableMCPServer(t, newTestMCPServer(0, "remote_tool"))
				origin = httpServer.URL
				httpClient = httpServer.Client()
			}

			checker := &stubServerAccessChecker{denied: map[string]bool{accessDeniedID: true}}
			m := newRemoteAccessTestManager(t, []ServerConfig{{
				ID: accessDeniedID, Name: remoteName, Enabled: true, BaseURL: origin,
			}}, checker, httpClient)
			t.Cleanup(func() {
				if uc := m.clients[req.remoteKey()]; uc != nil {
					uc.Close()
				}
			})

			first := m.GetCatalogAccess(ctx, req)
			assert.False(t, hasToolFromOrigin(first.Tools, origin))
			assert.Nil(t, first.Errors)
			cached := m.clients[req.remoteKey()]
			require.NotNil(t, cached)
			assert.False(t, cached.hasClient(remoteName))

			stillDenied := m.GetCatalogAccess(ctx, req)
			assert.False(t, hasToolFromOrigin(stillDenied.Tools, origin))
			assert.Nil(t, stillDenied.Errors)
			assert.False(t, cached.hasClient(remoteName))
			assert.Same(t, cached, m.clients[req.remoteKey()])

			checker.denied = map[string]bool{}

			// deny→allow incrementally connects the existing remote pool.
			allowed := m.GetCatalogAccess(ctx, req)
			pooled := m.clients[req.remoteKey()]
			require.NotNil(t, pooled)
			assert.Same(t, cached, pooled)
			if tt.wantToolAfter {
				assert.True(t, hasToolFromOrigin(allowed.Tools, origin))
				assert.True(t, pooled.hasClient(remoteName))
				assert.Nil(t, allowed.Errors)
			}
			if tt.wantErrorsAfter {
				require.NotNil(t, allowed.Errors)
				assert.Len(t, allowed.Errors.Errors, 1)
				assert.False(t, pooled.hasClient(remoteName))
			}

			if !tt.thenDenyDropsTool {
				return
			}

			checker.denied = map[string]bool{accessDeniedID: true}
			deniedAgain := m.GetCatalogAccess(ctx, req)
			assert.False(t, hasToolFromOrigin(deniedAgain.Tools, origin))
			assert.True(t, pooled.hasClient(remoteName), "allow→deny filters tools without disconnecting")
			assert.Same(t, pooled, m.clients[req.remoteKey()])
		})
	}
}

func TestGetCatalogAccessServiceAccountUsesInvokerAndRemoteOwnerPool(t *testing.T) {
	ctx := context.Background()
	httpServer := startStreamableMCPServer(t, newTestMCPServer(0, "remote_tool"))
	checker := &stubServerAccessChecker{}
	m := newRemoteAccessTestManager(t, []ServerConfig{{
		ID:                    accessAllowedID,
		Name:                  "Remote",
		Enabled:               true,
		BaseURL:               httpServer.URL,
		ServiceAccountHeaders: testServiceAccountHeaders(),
	}}, checker, httpServer.Client())

	firstReq := ServiceAccountCatalogRequest("bot-1", "user-a")
	first := m.GetCatalogAccess(ctx, firstReq)
	require.Nil(t, first.Errors)
	require.True(t, hasToolFromOrigin(first.Tools, httpServer.URL))
	pooled := m.clients[firstReq.remoteKey()]
	require.NotNil(t, pooled)
	t.Cleanup(pooled.Close)

	secondReq := ServiceAccountCatalogRequest("bot-1", "user-b")
	second := m.GetCatalogAccess(ctx, secondReq)
	require.Nil(t, second.Errors)
	require.True(t, hasToolFromOrigin(second.Tools, httpServer.URL))

	assert.Equal(t, []string{"user-a", "user-b"}, checker.users,
		"policy enforcement must use the invoking human in service-account mode")
	assert.Same(t, pooled, m.clients[secondReq.remoteKey()],
		"service-account remotes must remain pooled by the bot owner")
	assert.Nil(t, m.clients[clientKey{userID: "user-a", kind: clientKindSARemote}])
	assert.Nil(t, m.clients[clientKey{userID: "user-b", kind: clientKindSARemote}])
}
