// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/mcp"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/mcpserver/logger"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// stubRegistry is a hand-written PluginServerRegistry for tests. Protect with
// mu so tests can mutate the slice from the test goroutine between rebuilds.
type stubRegistry struct {
	mu      sync.Mutex
	servers []mcppkg.PluginServerConfig
}

func (s *stubRegistry) ListPluginServers() []mcppkg.PluginServerConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]mcppkg.PluginServerConfig, len(s.servers))
	copy(out, s.servers)
	return out
}

func (s *stubRegistry) set(servers []mcppkg.PluginServerConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers = servers
}

// Compile-time sanity: stubRegistry must satisfy PluginServerRegistry.
var _ PluginServerRegistry = (*stubRegistry)(nil)

// listToolNames enumerates the tools on the currently-active *mcp.Server by
// standing up an httptest.Server around h.MCPHandler and driving it with an
// in-process MCP client. This exercises the full public contract (the factory
// read, the streamable handler, and tool listing) rather than reaching into
// private state.
func listToolNames(t *testing.T, h *PluginMCPHandlers) []string {
	t.Helper()
	ts := httptest.NewServer(h.MCPHandler)
	t.Cleanup(ts.Close)

	client := gosdkmcp.NewClient(&gosdkmcp.Implementation{Name: "test-lister", Version: "1.0"}, &gosdkmcp.ClientOptions{})
	sess, err := client.Connect(context.Background(), &gosdkmcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: &http.Client{},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	res, err := sess.ListTools(context.Background(), &gosdkmcp.ListToolsParams{})
	require.NoError(t, err)
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestNewPluginMCPHandlers_IteratesRegistry is a release-gate test. It asserts
// that a plugin server with Enabled=true && ExposeExternal=true contributes
// its tools to the factory server, and that plugins with either flag false
// contribute ZERO tools. If this regressed, external MCP clients could see
// tools that the admin explicitly disabled.
func TestNewPluginMCPHandlers_IteratesRegistry(t *testing.T) {
	target := newFakePluginMCPServer(t, 1, nil) // 1 tool named test_tool_0
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)

	reg := &stubRegistry{servers: []mcppkg.PluginServerConfig{
		{PluginID: "com.example.enabled", Name: "Enabled", Path: "/mcp", Enabled: true, ExposeExternal: true},
		{PluginID: "com.example.expose-off", Name: "ExposeOff", Path: "/mcp", Enabled: true, ExposeExternal: false},
		{PluginID: "com.example.disabled", Name: "Disabled", Path: "/mcp", Enabled: false, ExposeExternal: true},
	}}

	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)

	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI)
	require.NoError(t, err)
	require.NotNil(t, h.MCPHandler)

	toolNames := listToolNames(t, h)
	proxyCount := 0
	for _, n := range toolNames {
		if n == "test_tool_0" {
			proxyCount++
		}
	}
	require.Equal(t, 1, proxyCount, "only Enabled && ExposeExternal plugin tools should be aggregated")
}

// TestRebuildExternalServer_PicksUpNewRegistrations asserts the rebuild
// trigger chain works end-to-end: initially empty registry -> no proxy tools.
// After registering a plugin server and calling RebuildExternalServer, the
// tool appears on the currently-active server.
func TestRebuildExternalServer_PicksUpNewRegistrations(t *testing.T) {
	target := newFakePluginMCPServer(t, 1, nil)
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)

	reg := &stubRegistry{servers: nil}
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)

	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI)
	require.NoError(t, err)

	initial := listToolNames(t, h)
	for _, n := range initial {
		require.NotEqual(t, "test_tool_0", n, "precondition: no proxy tools before registration")
	}

	reg.set([]mcppkg.PluginServerConfig{{PluginID: "com.example.late", Name: "Late", Path: "/mcp", Enabled: true, ExposeExternal: true}})
	h.RebuildExternalServer()

	after := listToolNames(t, h)
	var sawProxy bool
	for _, n := range after {
		if n == "test_tool_0" {
			sawProxy = true
			break
		}
	}
	require.True(t, sawProxy, "RebuildExternalServer should have picked up the new registration")
}

// TestRebuildExternalServer_RemovesUnregistered asserts the converse: once a
// plugin is removed from the registry, RebuildExternalServer drops its tools.
func TestRebuildExternalServer_RemovesUnregistered(t *testing.T) {
	target := newFakePluginMCPServer(t, 1, nil)
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)

	reg := &stubRegistry{servers: []mcppkg.PluginServerConfig{
		{PluginID: "com.example.tmp", Name: "Tmp", Path: "/mcp", Enabled: true, ExposeExternal: true},
	}}
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI)
	require.NoError(t, err)

	reg.set(nil)
	h.RebuildExternalServer()

	after := listToolNames(t, h)
	for _, n := range after {
		require.NotEqual(t, "test_tool_0", n, "unregistered plugin's tools should be gone after rebuild")
	}
}

// TestNewPluginMCPHandlers_NilRegistryIsNoOp lets callers pass nil registry
// to disable aggregation entirely. Native tools should still register.
func TestNewPluginMCPHandlers_NilRegistryIsNoOp(t *testing.T) {
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, h.MCPHandler)
	// Listing tools should not panic or error — it's just the native set.
	_ = listToolNames(t, h)
}
