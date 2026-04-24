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

// Test A (release gate): plugin server Enabled=true with 2 tools → both flow through.
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

// Test B (release gate — MUST PASS per orchestration plan):
// plugin server Enabled=false → zero tools, and PluginHTTP is never called.
func TestClientManager_GetToolsForUser_PluginDisabled_ZeroTools(t *testing.T) {
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

// Test C (release gate): plugin + "remote-like" plugin bucketing through the
// GetToolsForUser → filterToolsByConfig pipeline.
//
// Deviation from planner-3 spec: embedded+remote+plugin in one unit test
// requires standing up an EmbeddedMCPServer stub (with an InMemoryTransport
// wrapper) + a real OAuthManager for remote. The function-level filter
// bucketing is already asserted by the "embedded + remote + plugin mix" row
// in client_manager_filter_test.go. This test therefore validates the
// GetToolsForUser integration against TWO plugin servers with different
// pluginIDs — proving the third-loop snapshot + per-plugin origin-key
// construction + filter bucketing all line up. Full-stack embedded+remote
// coverage remains under the `integration` build tag (see testhelpers_test.go).
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
// Scope note: this test deliberately avoids concurrent GetToolsForUser calls
// because that path writes to m.activity / m.clients under a separate lock
// (clientsMu) whose pre-existing concurrency contract is out of scope for
// Phase 1D. The registry + snapshot lane — which is what this phase adds —
// is exercised directly.
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
