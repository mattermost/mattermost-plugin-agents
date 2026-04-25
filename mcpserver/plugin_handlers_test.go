// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/mcp"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
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

// TestNewPluginMCPHandlers_FiltersToolsByPolicy verifies that per-tool admin
// policy is enforced on the external aggregated MCP
// endpoint. A plugin server's ToolConfigs entry with Enabled=false must drop
// the corresponding tool from the external server's tool list, while tools
// with no ToolConfigs entry default-allow through (matching the
// MCPServerConfig.GetToolPolicy ("ask", true) fallback for unconfigured
// tools).
//
// If this regresses, external MCP clients (Claude Desktop, scripts/e2e/main.go,
// any caller of /plugins/mattermost-ai/mcp-server/mcp) would see tools the
// admin explicitly denied.
func TestNewPluginMCPHandlers_FiltersToolsByPolicy(t *testing.T) {
	// Source plugin advertises 2 tools: test_tool_0 and test_tool_1.
	target := newFakePluginMCPServer(t, 2, nil)
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)

	reg := &stubRegistry{servers: []mcppkg.PluginServerConfig{{
		PluginID:       "com.example.policy",
		Name:           "Policy",
		Path:           "/mcp",
		Enabled:        true,
		ExposeExternal: true,
		// Admin policy: deny test_tool_0, leave test_tool_1 unconfigured
		// (default-allow).
		ToolConfigs: []mcppkg.ToolConfig{
			{Name: "test_tool_0", Policy: "ask", Enabled: false},
		},
	}}}

	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI)
	require.NoError(t, err)

	toolNames := listToolNames(t, h)

	var sawDenied, sawAllowed bool
	for _, n := range toolNames {
		if n == "test_tool_0" {
			sawDenied = true
		}
		if n == "test_tool_1" {
			sawAllowed = true
		}
	}
	require.False(t, sawDenied, "admin-denied tool must be hidden from the external endpoint")
	require.True(t, sawAllowed, "tool with no policy entry must default-allow through (matches GetToolPolicy fallback)")
}

// TestNewPluginMCPHandlers_PolicyIsPerPluginServer asserts that ToolConfigs
// from one plugin server do NOT bleed into another plugin server's policy
// lookup. Two plugins both advertise a tool named test_tool_0; one plugin
// denies it via ToolConfigs, the other does not. After aggregation the
// allowed plugin's tool must remain.
//
// Note: external clients see the unqualified tool name. With two plugins each
// advertising "test_tool_0", the go-sdk MCP server will see two AddTool calls
// for the same name. The test asserts AT LEAST one survives — which proves
// that policy lookup is correctly scoped per-plugin (the deny on plugin-A
// dropped its AddTool, leaving plugin-B's AddTool to land). If policy were
// global, both AddTool calls would be skipped and the tool would be missing.
func TestNewPluginMCPHandlers_PolicyIsPerPluginServer(t *testing.T) {
	// Two distinct fake plugin MCP servers, each advertising test_tool_0.
	targetA := newFakePluginMCPServer(t, 1, nil)
	t.Cleanup(targetA.Close)
	targetB := newFakePluginMCPServer(t, 1, nil)
	t.Cleanup(targetB.Close)

	// Forwarder dispatches PluginHTTP based on the rewritten URL.Path
	// prefix. Both targets are reachable; we use a custom forwarder so each
	// plugin ID routes to its own httptest.Server.
	mockAPI := newPerPluginForwarder(t, map[string]*httptest.Server{
		"com.example.deny":  targetA,
		"com.example.allow": targetB,
	})

	reg := &stubRegistry{servers: []mcppkg.PluginServerConfig{
		{
			PluginID: "com.example.deny", Name: "Deny", Path: "/mcp",
			Enabled: true, ExposeExternal: true,
			ToolConfigs: []mcppkg.ToolConfig{
				{Name: "test_tool_0", Policy: "ask", Enabled: false},
			},
		},
		{
			PluginID: "com.example.allow", Name: "Allow", Path: "/mcp",
			Enabled: true, ExposeExternal: true,
			// No ToolConfigs → default-allow.
		},
	}}

	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI)
	require.NoError(t, err)

	toolNames := listToolNames(t, h)
	count := 0
	for _, n := range toolNames {
		if n == "test_tool_0" {
			count++
		}
	}
	require.GreaterOrEqual(t, count, 1, "the allow-plugin's test_tool_0 must survive — policy scoping is per-plugin")
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

func TestRebuildExternalServer_SkipsTimedOutPluginAndKeepsHealthyPlugins(t *testing.T) {
	healthy := newFakePluginMCPServer(t, 1, nil)
	t.Cleanup(healthy.Close)

	reg := &stubRegistry{servers: nil}
	mockAPI := newHangingAndHealthyPluginForwarder(t, "com.example.hung", healthy, nil)
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI)
	require.NoError(t, err)
	h.proxyDiscoveryTimeout = 25 * time.Millisecond

	reg.set([]mcppkg.PluginServerConfig{
		{PluginID: "com.example.hung", Name: "Hung", Path: "/mcp", Enabled: true, ExposeExternal: true},
		{PluginID: "com.example.healthy", Name: "Healthy", Path: "/mcp", Enabled: true, ExposeExternal: true},
	})
	h.RebuildExternalServer()

	after := listToolNames(t, h)
	var sawHealthy bool
	for _, n := range after {
		if n == "test_tool_0" {
			sawHealthy = true
			break
		}
	}
	require.True(t, sawHealthy, "healthy plugins should still be aggregated after another plugin times out")
}

func TestRebuildExternalServer_DoesNotBlockExternalRequestsWhileDiscovering(t *testing.T) {
	startedHungRequest := make(chan struct{})
	reg := &stubRegistry{servers: nil}
	mockAPI := newHangingAndHealthyPluginForwarder(t, "com.example.hung", nil, startedHungRequest)
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI)
	require.NoError(t, err)
	h.proxyDiscoveryTimeout = 100 * time.Millisecond

	reg.set([]mcppkg.PluginServerConfig{
		{PluginID: "com.example.hung", Name: "Hung", Path: "/mcp", Enabled: true, ExposeExternal: true},
	})

	rebuildDone := make(chan struct{})
	go func() {
		h.RebuildExternalServer()
		close(rebuildDone)
	}()

	select {
	case <-startedHungRequest:
	case <-time.After(500 * time.Millisecond):
		require.Fail(t, "timed out waiting for rebuild to start proxy discovery")
	}

	listDone := make(chan struct{})
	go func() {
		_ = listToolNames(t, h)
		close(listDone)
	}()

	select {
	case <-listDone:
	case <-time.After(500 * time.Millisecond):
		require.Fail(t, "external MCP requests should not wait for rebuild proxy discovery")
	}

	select {
	case <-rebuildDone:
	case <-time.After(2 * time.Second):
		require.Fail(t, "rebuild should finish after the bounded proxy discovery context expires")
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

// newPerPluginForwarder returns a mock mmapi.Client whose PluginHTTP routes by
// the leading /{pluginID} path segment that proxyRoundTripper writes onto
// every outbound request. Used by TestNewPluginMCPHandlers_PolicyIsPerPluginServer
// to dispatch two simultaneously-active plugin MCP servers.
//
// Mirrors newPluginHTTPForwarder (proxy_tools_test.go:55) but with a routing
// table keyed on plugin ID — that helper hardcodes a single target.
func newPerPluginForwarder(t *testing.T, byPluginID map[string]*httptest.Server) *mocks.MockClient {
	t.Helper()
	m := mocks.NewMockClient(t)
	m.EXPECT().PluginHTTP(mock.Anything).RunAndReturn(func(req *http.Request) *http.Response {
		// proxyRoundTripper rewrote URL.Path to "/{pluginID}{basePath}".
		// Trim the leading slash and split on the next slash to recover the ID.
		p := req.URL.Path
		if len(p) > 0 && p[0] == '/' {
			p = p[1:]
		}
		var pluginID, rest string
		if idx := indexByte(p, '/'); idx >= 0 {
			pluginID = p[:idx]
			rest = p[idx:]
		} else {
			pluginID = p
		}

		target, ok := byPluginID[pluginID]
		if !ok {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusNotFound)
			return rec.Result()
		}

		// Forward to the target's handler with the basePath restored.
		fwd := req.Clone(req.Context())
		fwd.URL.Path = rest
		rec := httptest.NewRecorder()
		target.Config.Handler.ServeHTTP(rec, fwd)
		return rec.Result()
	}).Maybe()
	return m
}

func newHangingAndHealthyPluginForwarder(t *testing.T, hungPluginID string, healthy *httptest.Server, startedHungRequest chan<- struct{}) *mocks.MockClient {
	t.Helper()
	m := mocks.NewMockClient(t)
	var once sync.Once
	m.EXPECT().PluginHTTP(mock.Anything).RunAndReturn(func(req *http.Request) *http.Response {
		pluginID, rest := splitPluginHTTPPath(req.URL.Path)
		if pluginID == hungPluginID {
			once.Do(func() {
				if startedHungRequest != nil {
					close(startedHungRequest)
				}
			})
			<-req.Context().Done()
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusGatewayTimeout)
			return rec.Result()
		}

		if healthy == nil {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusNotFound)
			return rec.Result()
		}

		fwd := req.Clone(req.Context())
		fwd.URL.Path = rest
		rec := httptest.NewRecorder()
		healthy.Config.Handler.ServeHTTP(rec, fwd)
		return rec.Result()
	}).Maybe()
	return m
}

func splitPluginHTTPPath(path string) (pluginID, rest string) {
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	if idx := indexByte(path, '/'); idx >= 0 {
		return path[:idx], path[idx:]
	}
	return path, ""
}

// indexByte is a tiny strings.IndexByte equivalent without importing strings
// for one call site. Returns -1 if c is not present.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
