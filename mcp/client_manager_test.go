// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakePluginHTTPClient stubs mmapi.Client; only PluginHTTP is exercised in
// these tests, so the embedded interface is left nil.
type fakePluginHTTPClient struct {
	mmapi.Client
	pluginHTTP func(*http.Request) *http.Response
}

func (f *fakePluginHTTPClient) PluginHTTP(req *http.Request) *http.Response {
	return f.pluginHTTP(req)
}

type recordKVSetWithExpiryClient struct {
	mmapi.Client
	key    string
	value  any
	ttl    time.Duration
	setErr error
}

func (c *recordKVSetWithExpiryClient) KVSetWithExpiry(key string, value interface{}, ttl time.Duration) error {
	c.key = key
	c.value = value
	c.ttl = ttl
	return c.setErr
}

func TestClientManagerReInitIdleTimeoutDefaulting(t *testing.T) {
	testCases := []struct {
		name                string
		idleTimeoutMinutes  int
		expectedConfigValue int
		expectedTimeout     time.Duration
	}{
		{
			name:                "defaults when timeout is zero",
			idleTimeoutMinutes:  0,
			expectedConfigValue: 30,
			expectedTimeout:     30 * time.Minute,
		},
		{
			name:                "defaults when timeout is negative",
			idleTimeoutMinutes:  -10,
			expectedConfigValue: 30,
			expectedTimeout:     30 * time.Minute,
		},
		{
			name:                "keeps positive timeout",
			idleTimeoutMinutes:  12,
			expectedConfigValue: 12,
			expectedTimeout:     12 * time.Minute,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := &ClientManager{}
			t.Cleanup(manager.Close)

			manager.ReInit(Config{
				IdleTimeoutMinutes: tc.idleTimeoutMinutes,
			}, nil)

			require.Equal(t, tc.expectedConfigValue, manager.config.IdleTimeoutMinutes)
			require.Equal(t, tc.expectedTimeout, manager.clientTimeout)
		})
	}
}

func TestClientManager_PluginServerRegistry_RegisterUnregisterList(t *testing.T) {
	m := &ClientManager{pluginServers: map[string]PluginServerConfig{}}
	t.Cleanup(m.Close)

	cfgA := PluginServerConfig{PluginID: "a", Name: "A", Path: "/mcp", Enabled: true}
	cfgB := PluginServerConfig{PluginID: "b", Name: "B", Path: "/mcp", Enabled: false}

	m.RegisterPluginServer(cfgA)
	m.RegisterPluginServer(cfgB)

	list := m.ListPluginServers()
	require.Len(t, list, 2)

	cfgA2 := PluginServerConfig{PluginID: "a", Name: "A prime", Path: "/mcp", Enabled: true}
	m.RegisterPluginServer(cfgA2)
	foundAPrime := false
	for _, c := range m.ListPluginServers() {
		if c.PluginID == "a" {
			require.Equal(t, "A prime", c.Name)
			foundAPrime = true
		}
	}
	require.True(t, foundAPrime, "expected re-registered entry with overwritten Name")

	enabled := m.snapshotEnabledPluginServers()
	require.Len(t, enabled, 1)
	require.Equal(t, "a", enabled[0].PluginID)

	m.UnregisterPluginServer("a")
	list = m.ListPluginServers()
	require.Len(t, list, 1)
	require.Equal(t, "b", list[0].PluginID)

	m.UnregisterPluginServer("nonexistent")
	require.Len(t, m.ListPluginServers(), 1)
}

func TestClientManager_GetPluginServer(t *testing.T) {
	m := &ClientManager{pluginServers: map[string]PluginServerConfig{}}
	t.Cleanup(m.Close)

	cfg, ok := m.GetPluginServer("missing")
	require.False(t, ok)
	require.Equal(t, PluginServerConfig{}, cfg)

	stored := PluginServerConfig{
		PluginID:       "com.example.mcp",
		Name:           "Example",
		Path:           "/mcp",
		Enabled:        true,
		ExposeExternal: true,
	}
	m.RegisterPluginServer(stored)

	got, ok := m.GetPluginServer("com.example.mcp")
	require.True(t, ok)
	require.Equal(t, stored, got)

	// Returned value must be a copy.
	got.Enabled = false
	got.Name = "mutated"
	again, ok := m.GetPluginServer("com.example.mcp")
	require.True(t, ok)
	require.Equal(t, stored, again, "GetPluginServer must return an independent value copy")
}

func TestClientManager_HydratesPluginServersFromConfig(t *testing.T) {
	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	persisted := []PluginServerConfig{
		{
			PluginID:       "com.example.a",
			Name:           "A",
			Path:           "/mcp",
			Enabled:        true,
			ExposeExternal: false,
			ToolConfigs: []ToolConfig{
				{Name: "tool_a1", Policy: ToolPolicyAsk, Enabled: true},
				{Name: "tool_a2", Policy: ToolPolicyAsk, Enabled: false},
			},
		},
		{
			PluginID:       "com.example.b",
			Name:           "B",
			Path:           "/mcp",
			Enabled:        false,
			ExposeExternal: true,
		},
	}

	m := NewClientManager(
		Config{IdleTimeoutMinutes: 30, PluginServers: persisted},
		client.Log,
		client,
		nil,
		nil,
		nil,
		nil,
	)
	t.Cleanup(m.Close)

	got := m.ListPluginServers()
	require.Len(t, got, 2, "both persisted entries must be hydrated synchronously")

	byID := map[string]PluginServerConfig{}
	for _, c := range got {
		byID[c.PluginID] = c
	}

	a := byID["com.example.a"]
	require.Equal(t, "A", a.Name)
	require.Equal(t, "/mcp", a.Path)
	require.True(t, a.Enabled)
	require.False(t, a.ExposeExternal)
	require.Len(t, a.ToolConfigs, 2)
	require.Equal(t, "tool_a1", a.ToolConfigs[0].Name)
	require.True(t, a.ToolConfigs[0].Enabled)
	require.False(t, a.ToolConfigs[1].Enabled)

	b := byID["com.example.b"]
	require.Equal(t, "B", b.Name)
	require.False(t, b.Enabled)
	require.True(t, b.ExposeExternal)
	require.Empty(t, b.ToolConfigs)
}

// A config broadcast must merge persisted admin-owned fields (Enabled,
// ToolConfigs) onto in-memory entries while preserving runtime identity
// fields and the plugin-controlled ExposeExternal flag set by the source
// plugin.
func TestClientManager_ReInitSyncsPluginServerAdminFields(t *testing.T) {
	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, nil)
	t.Cleanup(m.Close)

	m.RegisterPluginServer(PluginServerConfig{
		PluginID:       "com.example.mcp",
		Name:           "Live Name",
		Path:           "/live-mcp",
		Enabled:        false,
		ExposeExternal: false,
	})

	newCfg := Config{
		IdleTimeoutMinutes: 30,
		PluginServers: []PluginServerConfig{{
			PluginID:       "com.example.mcp",
			Name:           "Stale Name From Config", // must be ignored on merge
			Path:           "/stale-from-config",     // must be ignored on merge
			Enabled:        true,
			ExposeExternal: true,
			ToolConfigs: []ToolConfig{
				{Name: "echo", Policy: ToolPolicyAsk, Enabled: false},
			},
		}},
	}

	m.ReInit(newCfg, nil)

	got, ok := m.GetPluginServer("com.example.mcp")
	require.True(t, ok)

	require.True(t, got.Enabled, "Enabled merged from config")
	require.False(t, got.ExposeExternal, "ExposeExternal must remain plugin-controlled")
	require.Len(t, got.ToolConfigs, 1, "ToolConfigs merged from config")
	require.Equal(t, "echo", got.ToolConfigs[0].Name)
	require.False(t, got.ToolConfigs[0].Enabled)

	require.Equal(t, "Live Name", got.Name)
	require.Equal(t, "/live-mcp", got.Path)
}

func TestClientManager_ReInitInsertsConfigOnlyEntries(t *testing.T) {
	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, nil)
	t.Cleanup(m.Close)

	require.Empty(t, m.ListPluginServers(), "precondition: empty registry")

	cfg := Config{
		IdleTimeoutMinutes: 30,
		PluginServers: []PluginServerConfig{{
			PluginID:       "com.example.mcp",
			Name:           "From Config",
			Path:           "/from-config",
			Enabled:        true,
			ExposeExternal: false,
		}},
	}

	m.ReInit(cfg, nil)

	got, ok := m.GetPluginServer("com.example.mcp")
	require.True(t, ok)
	require.Equal(t, "From Config", got.Name)
	require.Equal(t, "/from-config", got.Path)
	require.True(t, got.Enabled)
}

// Live registrations absent from config must survive config broadcasts.
func TestClientManager_ReInitPreservesUnpersistedRuntimeEntries(t *testing.T) {
	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, nil)
	t.Cleanup(m.Close)

	live := PluginServerConfig{
		PluginID: "com.example.live",
		Name:     "Live",
		Path:     "/live",
		Enabled:  true,
	}
	m.RegisterPluginServer(live)

	cfg := Config{
		IdleTimeoutMinutes: 30,
		PluginServers: []PluginServerConfig{{
			PluginID: "com.example.other",
			Name:     "Other",
			Path:     "/other",
			Enabled:  true,
		}},
	}
	m.ReInit(cfg, nil)

	require.Len(t, m.ListPluginServers(), 2)
	stillLive, ok := m.GetPluginServer("com.example.live")
	require.True(t, ok, "runtime registration must survive ReInit")
	require.Equal(t, live, stillLive)
}

func TestClientManager_SyncPluginServersFromConfig_SkipsEmptyPluginID(t *testing.T) {
	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	cfg := Config{
		IdleTimeoutMinutes: 30,
		PluginServers: []PluginServerConfig{
			{PluginID: "", Name: "Empty ID", Path: "/x", Enabled: true},
			{PluginID: "com.example.valid", Name: "Valid", Path: "/mcp", Enabled: true},
		},
	}

	m := NewClientManager(cfg, client.Log, client, nil, nil, nil, nil)
	t.Cleanup(m.Close)

	got := m.ListPluginServers()
	require.Len(t, got, 1, "empty-PluginID entry must be skipped; only valid entry hydrated")
	require.Equal(t, "com.example.valid", got[0].PluginID)
}

func TestClientManager_GetToolsForUser_PluginEnabled(t *testing.T) {
	target := newFakePluginMCPServer(t, 2)
	t.Cleanup(target.Close)

	mockAPI := newPluginHTTPForwarder(t, target)

	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, mockAPI)
	t.Cleanup(m.Close)

	cfg := PluginServerConfig{
		PluginID: "com.example.mcp",
		Name:     "Example",
		Path:     "/mcp",
		Enabled:  true,
	}
	m.RegisterPluginServer(cfg)

	tools, mcpErrors := m.GetToolsForUser("alice")
	require.Nil(t, mcpErrors, "no errors expected on happy path")
	require.Len(t, tools, 2, "expected 2 tools from plugin server")
	for _, tool := range tools {
		assert.Equal(t, "plugin://com.example.mcp", tool.ServerOrigin)
	}
}

func TestClientManager_GetToolsForUser_PluginDisabled_ZeroTools(t *testing.T) {
	mockAPI := mocks.NewMockClient(t)
	mockAPI.EXPECT().PluginHTTP(mock.Anything).RunAndReturn(func(req *http.Request) *http.Response {
		t.Fatalf("PluginHTTP must not be called for disabled plugin server; got path %q", req.URL.Path)
		return nil
	}).Maybe()

	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, mockAPI)
	t.Cleanup(m.Close)

	cfg := PluginServerConfig{
		PluginID: "com.example.mcp",
		Name:     "Example",
		Path:     "/mcp",
		Enabled:  false,
	}
	m.RegisterPluginServer(cfg)

	tools, mcpErrors := m.GetToolsForUser("alice")
	require.Nil(t, mcpErrors, "no errors expected when plugin is simply disabled")
	require.Empty(t, tools, "disabled plugin must contribute zero tools")

	mockAPI.AssertNotCalled(t, "PluginHTTP")
}

func TestClientManager_GetToolsForUser_PluginEnabled_HTTPFailure(t *testing.T) {
	testCases := []struct {
		name       string
		pluginHTTP func(t *testing.T, req *http.Request) *http.Response
	}{
		{
			name: "nil response",
			pluginHTTP: func(t *testing.T, req *http.Request) *http.Response {
				return nil
			},
		},
		{
			name: "server error",
			pluginHTTP: func(t *testing.T, req *http.Request) *http.Response {
				rec := httptest.NewRecorder()
				rec.WriteHeader(http.StatusInternalServerError)
				return rec.Result()
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &fakePluginHTTPClient{
				pluginHTTP: func(req *http.Request) *http.Response {
					return tc.pluginHTTP(t, req)
				},
			}

			pluginTestAPI := &plugintest.API{}
			setupTestLogger(pluginTestAPI)
			client := pluginapi.NewClient(pluginTestAPI, nil)

			m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, mockAPI)
			t.Cleanup(m.Close)

			m.RegisterPluginServer(PluginServerConfig{
				PluginID: "com.example.mcp",
				Name:     "Example",
				Path:     "/mcp",
				Enabled:  true,
			})

			tools, mcpErrors := m.GetToolsForUser("alice")
			require.NotNil(t, mcpErrors, "plugin connection failure must be surfaced")
			require.NotEmpty(t, mcpErrors.Errors, "plugin connection failure must populate generic MCP errors")
			require.Empty(t, mcpErrors.ToolAuthErrors, "plugin HTTP failures should not be treated as OAuth errors")
			for _, tool := range tools {
				require.NotEqual(t, "plugin://com.example.mcp", tool.ServerOrigin, "failed plugin server must not contribute bogus tools")
			}
			require.Empty(t, tools, "failed plugin server must not contribute tools")
		})
	}
}

func TestClientManager_GetToolsForUser_MultiplePluginServers(t *testing.T) {
	targetA := newFakePluginMCPServerWithPrefix(t, "tool_a", 2)
	t.Cleanup(targetA.Close)
	targetB := newFakePluginMCPServerWithPrefix(t, "tool_b", 1)
	t.Cleanup(targetB.Close)

	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	// PluginHTTPRoundTripper rewrites paths to "/<pluginID>/mcp"; route accordingly.
	mockAPI := &fakePluginHTTPClient{
		pluginHTTP: func(req *http.Request) *http.Response {
			rec := httptest.NewRecorder()
			switch {
			case strings.HasPrefix(req.URL.Path, "/com.example.a"):
				targetA.Config.Handler.ServeHTTP(rec, req)
			case strings.HasPrefix(req.URL.Path, "/com.example.b"):
				targetB.Config.Handler.ServeHTTP(rec, req)
			default:
				rec.WriteHeader(http.StatusNotFound)
			}
			return rec.Result()
		},
	}

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, mockAPI)
	t.Cleanup(m.Close)

	m.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.a", Name: "A", Path: "/mcp", Enabled: true})
	m.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.b", Name: "B", Path: "/mcp", Enabled: true})

	tools, mcpErrors := m.GetToolsForUser("alice")
	require.Nil(t, mcpErrors)
	require.Len(t, tools, 3, "expected 2 tools from A + 1 tool from B")

	// Cross-server order is map-iteration-defined, so bucket-count.
	counts := map[string]int{}
	for _, tool := range tools {
		counts[tool.ServerOrigin]++
	}
	require.Equal(t, 2, counts["plugin://com.example.a"])
	require.Equal(t, 1, counts["plugin://com.example.b"])
}

// Run with -race. Concurrent Register/Unregister/List/snapshot must not
// deadlock or race.
func TestClientManager_PluginServerRegistry_RaceSafe(t *testing.T) {
	m := &ClientManager{pluginServers: map[string]PluginServerConfig{}}
	t.Cleanup(m.Close)

	const writers = 8
	const readers = 8
	const iterations = 200

	var wg sync.WaitGroup
	var stop atomic.Bool

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			pluginID := "com.example." + string(rune('a'+id))
			for iter := 0; iter < iterations && !stop.Load(); iter++ {
				m.RegisterPluginServer(PluginServerConfig{
					PluginID: pluginID,
					Name:     "Test",
					Path:     "/mcp",
					Enabled:  iter%2 == 0,
				})
				m.UnregisterPluginServer(pluginID)
			}
		}(i)
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iter := 0; iter < iterations && !stop.Load(); iter++ {
				_ = m.ListPluginServers()
				_ = m.snapshotEnabledPluginServers()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		stop.Store(true)
		t.Fatal("deadlock or excessive contention in Register/Unregister vs List/snapshot")
	}
}

func TestClientManagerInvalidateUserClients(t *testing.T) {
	now := time.Now()
	testCases := []struct {
		name                 string
		userID               string
		expectedClientKeys   []string
		expectedActivityKeys []string
	}{
		{
			name:                 "removes existing user",
			userID:               "user-1",
			expectedClientKeys:   []string{"user-2"},
			expectedActivityKeys: []string{"user-2"},
		},
		{
			name:                 "ignores missing user",
			userID:               "missing-user",
			expectedClientKeys:   []string{"user-1", "user-2"},
			expectedActivityKeys: []string{"user-1", "user-2"},
		},
		{
			name:                 "ignores empty user",
			userID:               "",
			expectedClientKeys:   []string{"user-1", "user-2"},
			expectedActivityKeys: []string{"user-1", "user-2"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := &ClientManager{
				clients: map[string]*UserClients{
					"user-1": {clients: map[string]*Client{}},
					"user-2": {clients: map[string]*Client{}},
				},
				activity: map[string]time.Time{
					"user-1": now,
					"user-2": now.Add(time.Minute),
				},
			}

			manager.InvalidateUserClients(tc.userID)

			require.Len(t, manager.clients, len(tc.expectedClientKeys))
			for _, key := range tc.expectedClientKeys {
				require.Contains(t, manager.clients, key)
			}
			require.Len(t, manager.activity, len(tc.expectedActivityKeys))
			for _, key := range tc.expectedActivityKeys {
				require.Contains(t, manager.activity, key)
			}
			require.Equal(t, now.Add(time.Minute), manager.activity["user-2"])
		})
	}
}

func TestClientManagerCreateAndStoreUserClientSetsInitialActivity(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("LogDebug", "No remote MCP servers provided for user", "userID", "user-1").Return().Maybe()
	client := pluginapi.NewClient(mockAPI, nil)
	manager := &ClientManager{
		config:   Config{},
		log:      client.Log,
		clients:  make(map[string]*UserClients),
		activity: make(map[string]time.Time),
	}

	before := time.Now()
	userClients, mcpErrors := manager.createAndStoreUserClient("user-1")
	after := time.Now()

	require.NotNil(t, userClients)
	require.Nil(t, mcpErrors)
	require.Contains(t, manager.clients, "user-1")

	lastActivity, ok := manager.activity["user-1"]
	require.True(t, ok)
	require.False(t, lastActivity.Before(before))
	require.False(t, lastActivity.After(after))
}

func TestClientManagerGetClientForUserExistingClientConcurrent(t *testing.T) {
	before := time.Now()
	userClients := &UserClients{clients: map[string]*Client{}}
	manager := &ClientManager{
		clients: map[string]*UserClients{
			"user-1": userClients,
		},
		activity: map[string]time.Time{
			"user-1": before.Add(-time.Minute),
		},
	}

	const goroutines = 16
	const iterations = 200

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range iterations {
				got, errs := manager.getClientForUser("user-1")
				if got != userClients || errs != nil {
					t.Errorf("getClientForUser returned unexpected result: got=%p errs=%v", got, errs)
					return
				}
			}
		}()
	}

	close(start)
	wg.Wait()

	lastActivity, ok := manager.activity["user-1"]
	require.True(t, ok)
	require.False(t, lastActivity.Before(before))
}

func TestClientManagerMarkOAuthNeededInvalidatesUserClient(t *testing.T) {
	testCases := []struct {
		name                     string
		manager                  *ClientManager
		expectedErr              string
		expectedStoredKey        string
		expectedStoredAuthURL    string
		expectedStoredTTL        time.Duration
		expectPersistenceAttempt bool
	}{
		{
			name: "persists state when oauth manager exists",
			manager: func() *ClientManager {
				storeClient := &recordKVSetWithExpiryClient{}
				manager := &ClientManager{
					clients: map[string]*UserClients{
						"user-1": {clients: map[string]*Client{}},
					},
					activity: map[string]time.Time{
						"user-1": time.Now(),
					},
				}
				manager.oauthManager = NewOAuthManager(storeClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", nil, nil)
				return manager
			}(),
			expectedStoredKey:        "mcp_oauth_needed_v1_user-1_GitHub",
			expectedStoredAuthURL:    "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/GitHub/start",
			expectedStoredTTL:        oauthNeededStateTTL,
			expectPersistenceAttempt: true,
		},
		{
			name: "returns persistence error but still invalidates",
			manager: func() *ClientManager {
				storeClient := &recordKVSetWithExpiryClient{
					setErr: model.NewAppError("test", "oauth_needed_store_failed", nil, "persist failed", http.StatusInternalServerError),
				}
				manager := &ClientManager{
					clients: map[string]*UserClients{
						"user-1": {clients: map[string]*Client{}},
					},
					activity: map[string]time.Time{
						"user-1": time.Now(),
					},
				}
				manager.oauthManager = NewOAuthManager(storeClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", nil, nil)
				return manager
			}(),
			expectedErr:              "failed to store OAuth-needed state",
			expectedStoredKey:        "mcp_oauth_needed_v1_user-1_GitHub",
			expectedStoredAuthURL:    "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/GitHub/start",
			expectedStoredTTL:        oauthNeededStateTTL,
			expectPersistenceAttempt: true,
		},
		{
			name: "still invalidates without oauth manager",
			manager: &ClientManager{
				clients: map[string]*UserClients{
					"user-1": {clients: map[string]*Client{}},
				},
				activity: map[string]time.Time{
					"user-1": time.Now(),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.manager.MarkOAuthNeeded("user-1", "GitHub", "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/GitHub/start")

			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErr)
			}
			require.Empty(t, tc.manager.clients)
			require.Empty(t, tc.manager.activity)

			if !tc.expectPersistenceAttempt {
				return
			}

			storeClient, ok := tc.manager.oauthManager.pluginAPI.(*recordKVSetWithExpiryClient)
			require.True(t, ok)
			require.Equal(t, tc.expectedStoredKey, storeClient.key)
			require.Equal(t, tc.expectedStoredTTL, storeClient.ttl)

			state, ok := storeClient.value.(*OAuthNeededState)
			require.True(t, ok)
			require.Equal(t, tc.expectedStoredAuthURL, state.AuthURL)
		})
	}
}

func TestClientManagerProcessOAuthCallbackRequiresOAuthManager(t *testing.T) {
	manager := &ClientManager{}

	session, err := manager.ProcessOAuthCallback(t.Context(), "user-1", "state", "code")

	require.Nil(t, session)
	require.ErrorIs(t, err, ErrOAuthNotConfigured)
}

func TestClientManagerDisconnectUserOAuthRequiresOAuthManager(t *testing.T) {
	manager := &ClientManager{}

	err := manager.DisconnectUserOAuth("user-1", "GitHub")

	require.ErrorIs(t, err, ErrOAuthNotConfigured)
}
