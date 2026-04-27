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

	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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

	// Idempotent re-register (overwrite).
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

	// Enabled-snapshot filters out disabled entries.
	enabled := m.snapshotEnabledPluginServers()
	require.Len(t, enabled, 1)
	require.Equal(t, "a", enabled[0].PluginID)

	m.UnregisterPluginServer("a")
	list = m.ListPluginServers()
	require.Len(t, list, 1)
	require.Equal(t, "b", list[0].PluginID)

	// Unregister-noop on unknown ID.
	m.UnregisterPluginServer("nonexistent")
	require.Len(t, m.ListPluginServers(), 1)
}

// TestClientManager_GetPluginServer covers the lookup used by the bridge
// register handler to preserve admin-set fields across plugin re-registration
// (see handleMCPRegister in api/api_bridge_mcp.go).
func TestClientManager_GetPluginServer(t *testing.T) {
	m := &ClientManager{pluginServers: map[string]PluginServerConfig{}}
	t.Cleanup(m.Close)

	// Not-found case returns zero value + false.
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

	// Mutating the returned value must not affect the stored entry (value copy).
	got.Enabled = false
	got.Name = "mutated"
	again, ok := m.GetPluginServer("com.example.mcp")
	require.True(t, ok)
	require.Equal(t, stored, again, "GetPluginServer must return an independent value copy")
}

// TestClientManager_HydratesPluginServersFromConfig verifies that persisted
// plugin-server admin state is available before NewClientManager returns.
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

// TestClientManager_ReInitSyncsPluginServerAdminFields covers the
// Container.Update → listener → ReInit path: a config broadcast must merge
// admin-owned fields (Enabled, ExposeExternal, ToolConfigs) onto in-memory
// entries, while preserving runtime identity fields (Name, Path) that the
// source plugin most recently set.
func TestClientManager_ReInitSyncsPluginServerAdminFields(t *testing.T) {
	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, nil)
	t.Cleanup(m.Close)

	// Source plugin registered at runtime with self-declared identity.
	m.RegisterPluginServer(PluginServerConfig{
		PluginID:       "com.example.mcp",
		Name:           "Live Name",
		Path:           "/live-mcp",
		Enabled:        false, // pre-merge state
		ExposeExternal: false,
	})

	// Cluster node receives a config broadcast that flips admin fields.
	newCfg := Config{
		IdleTimeoutMinutes: 30,
		PluginServers: []PluginServerConfig{{
			PluginID:       "com.example.mcp",
			Name:           "Stale Name From Config", // identity field — must be ignored on merge
			Path:           "/stale-from-config",     // identity field — must be ignored on merge
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

	// Admin-owned fields take config values.
	require.True(t, got.Enabled, "Enabled merged from config")
	require.True(t, got.ExposeExternal, "ExposeExternal merged from config")
	require.Len(t, got.ToolConfigs, 1, "ToolConfigs merged from config")
	require.Equal(t, "echo", got.ToolConfigs[0].Name)
	require.False(t, got.ToolConfigs[0].Enabled)

	// Identity fields preserved from the live registration — the source
	// plugin is the source of truth for these.
	require.Equal(t, "Live Name", got.Name)
	require.Equal(t, "/live-mcp", got.Path)
}

// TestClientManager_ReInitInsertsConfigOnlyEntries verifies config-only plugin
// entries remain available until the source plugin registers again.
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

// TestClientManager_ReInitPreservesUnpersistedRuntimeEntries verifies that
// live registrations absent from config survive config broadcasts.
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

	// Config broadcast carries a DIFFERENT plugin's persisted state — the
	// live registration above is absent from cfg.PluginServers.
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

	// Both entries must be present.
	require.Len(t, m.ListPluginServers(), 2)
	stillLive, ok := m.GetPluginServer("com.example.live")
	require.True(t, ok, "runtime registration must survive ReInit")
	require.Equal(t, live, stillLive)
}

// TestClientManager_SyncPluginServersFromConfig_SkipsEmptyPluginID exercises
// the defensive `if persisted.PluginID == ""` guard in
// syncPluginServersFromConfig. A malformed config blob carrying an empty
// PluginID must not poison the registry — the offending entry is silently
// skipped while well-formed entries still hydrate.
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

// Plugin server Enabled=true with 2 tools: both flow through.
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

// Plugin server Enabled=false: zero tools, and PluginHTTP is never called.
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
		Enabled:  false, // disabled — should be skipped entirely
	}
	m.RegisterPluginServer(cfg)

	tools, mcpErrors := m.GetToolsForUser("alice")
	require.Nil(t, mcpErrors, "no errors expected when plugin is simply disabled")
	require.Empty(t, tools, "disabled plugin must contribute zero tools")

	// PluginHTTP MUST NOT have been called — snapshotEnabledPluginServers filters
	// disabled entries before any HTTP work is done.
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
			mockAPI := mocks.NewMockClient(t)
			mockAPI.EXPECT().PluginHTTP(mock.Anything).RunAndReturn(func(req *http.Request) *http.Response {
				return tc.pluginHTTP(t, req)
			}).Maybe()

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

// TestClientManager_GetToolsForUser_MultiplePluginServers verifies per-plugin
// origin bucketing through GetToolsForUser and filterToolsByConfig.
func TestClientManager_GetToolsForUser_MultiplePluginServers(t *testing.T) {
	targetA := newFakePluginMCPServerWithPrefix(t, "tool_a", 2)
	t.Cleanup(targetA.Close)
	targetB := newFakePluginMCPServerWithPrefix(t, "tool_b", 1)
	t.Cleanup(targetB.Close)

	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	// Route PluginHTTP based on which plugin the req is destined for —
	// PluginHTTPRoundTripper rewrites the path to "/<pluginID>/mcp".
	mockAPI := mocks.NewMockClient(t)
	mockAPI.EXPECT().PluginHTTP(mock.Anything).RunAndReturn(func(req *http.Request) *http.Response {
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
	}).Maybe()

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, mockAPI)
	t.Cleanup(m.Close)

	m.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.a", Name: "A", Path: "/mcp", Enabled: true})
	m.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.b", Name: "B", Path: "/mcp", Enabled: true})

	tools, mcpErrors := m.GetToolsForUser("alice")
	require.Nil(t, mcpErrors)
	require.Len(t, tools, 3, "expected 2 tools from A + 1 tool from B")

	// Bucket by ServerOrigin — the filter orders by serverOrder (map iteration
	// order in the filter, so not deterministic across A/B; just count).
	counts := map[string]int{}
	for _, tool := range tools {
		counts[tool.ServerOrigin]++
	}
	require.Equal(t, 2, counts["plugin://com.example.a"])
	require.Equal(t, 1, counts["plugin://com.example.b"])
}

// Race test: concurrent Register/Unregister/List/snapshotEnabledPluginServers
// must not deadlock or race. Validates that pluginServersMu correctly
// serializes writes and allows concurrent readers.
//
// Must be run with -race to verify data-race safety.
func TestClientManager_PluginServerRegistry_RaceSafe(t *testing.T) {
	m := &ClientManager{pluginServers: map[string]PluginServerConfig{}}
	t.Cleanup(m.Close)

	const writers = 8
	const readers = 8
	const iterations = 200

	var wg sync.WaitGroup
	var stop atomic.Bool

	// Writer goroutines: continuously register + unregister plugin servers.
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

	// Reader goroutines: continuously list + snapshot enabled.
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

	// Deadline: if the goroutines don't finish in 10 s, we deadlocked.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(10 * time.Second):
		stop.Store(true)
		t.Fatal("deadlock or excessive contention in Register/Unregister vs List/snapshot")
	}
}
