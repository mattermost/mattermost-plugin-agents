// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// stubRegistry is a mutable PluginServerRegistry for tests.
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

var _ PluginServerRegistry = (*stubRegistry)(nil)

// stubPluginAPI only overrides PluginHTTP for these tests.
type stubPluginAPI struct {
	mmapi.Client
	pluginHTTP func(req *http.Request) *http.Response
}

func (s *stubPluginAPI) PluginHTTP(req *http.Request) *http.Response {
	return s.pluginHTTP(req)
}

// listToolNamesNoRequire avoids require so it is safe from goroutines.
func listToolNamesNoRequire(t *testing.T, h *PluginMCPHandlers) ([]string, error) {
	t.Helper()
	ts := httptest.NewServer(h.MCPHandler)
	t.Cleanup(ts.Close)

	client := gosdkmcp.NewClient(&gosdkmcp.Implementation{Name: "test-lister", Version: "1.0"}, &gosdkmcp.ClientOptions{})
	sess, err := client.Connect(context.Background(), &gosdkmcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: &http.Client{},
	}, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = sess.Close() })

	res, err := sess.ListTools(context.Background(), &gosdkmcp.ListToolsParams{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names, nil
}

func listToolNames(t *testing.T, h *PluginMCPHandlers) []string {
	t.Helper()
	names, err := listToolNamesNoRequire(t, h)
	require.NoError(t, err)
	return names
}

func callTool(t *testing.T, h *PluginMCPHandlers, name string, args map[string]interface{}) (*gosdkmcp.CallToolResult, error) {
	t.Helper()
	ts := httptest.NewServer(h.MCPHandler)
	t.Cleanup(ts.Close)

	client := gosdkmcp.NewClient(&gosdkmcp.Implementation{Name: "test-caller", Version: "1.0"}, &gosdkmcp.ClientOptions{})
	sess, err := client.Connect(context.Background(), &gosdkmcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: &http.Client{},
	}, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = sess.Close() })

	return sess.CallTool(context.Background(), &gosdkmcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
}

func toolResultText(result *gosdkmcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var text strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*gosdkmcp.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	return text.String()
}

func newFakePluginMCPServerWithToolNames(t *testing.T, toolNames ...string) *httptest.Server {
	t.Helper()
	srv := gosdkmcp.NewServer(&gosdkmcp.Implementation{Name: "fake", Version: "1.0"}, nil)
	type echoIn struct {
		Message string `json:"message"`
	}
	type echoOut struct {
		Echo string `json:"echo"`
	}
	for _, toolName := range toolNames {
		gosdkmcp.AddTool(srv, &gosdkmcp.Tool{Name: toolName, Description: "test"}, func(_ context.Context, _ *gosdkmcp.CallToolRequest, in echoIn) (*gosdkmcp.CallToolResult, echoOut, error) {
			return nil, echoOut{Echo: in.Message}, nil
		})
	}
	streamable := gosdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *gosdkmcp.Server { return srv },
		&gosdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
	return httptest.NewServer(streamable)
}

func TestNewPluginMCPHandlers_IteratesRegistry(t *testing.T) {
	target := newFakePluginMCPServer(t, 1, nil)
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

func TestNewPluginMCPHandlers_SkipsPluginToolConflictingWithNativeTool(t *testing.T) {
	target := newFakePluginMCPServerWithToolNames(t, "create_post", "plugin_unique")
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)

	reg := &stubRegistry{servers: []mcppkg.PluginServerConfig{{
		PluginID:       "com.example.shadow",
		Name:           "Shadow",
		Path:           "/mcp",
		Enabled:        true,
		ExposeExternal: true,
	}}}

	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI)
	require.NoError(t, err)

	toolNames := listToolNames(t, h)
	var nativeNameCount int
	var sawPluginUnique bool
	for _, name := range toolNames {
		if name == "create_post" {
			nativeNameCount++
		}
		if name == "plugin_unique" {
			sawPluginUnique = true
		}
	}
	require.Equal(t, 1, nativeNameCount, "native tool should remain registered once")
	require.True(t, sawPluginUnique, "non-conflicting plugin tools should still be aggregated")

	result, err := callTool(t, h, "create_post", map[string]interface{}{
		"channel_id": "channel-id",
		"message":    "from test",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	text := toolResultText(result)
	require.Contains(t, text, "session authentication provider requires token resolver")
	require.NotContains(t, text, "proxy tool create_post")
}

func TestNewPluginMCPHandlers_FiltersToolsByPolicy(t *testing.T) {
	target := newFakePluginMCPServer(t, 2, nil)
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)

	reg := &stubRegistry{servers: []mcppkg.PluginServerConfig{{
		PluginID:       "com.example.policy",
		Name:           "Policy",
		Path:           "/mcp",
		Enabled:        true,
		ExposeExternal: true,
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

// ToolConfigs are scoped per plugin server.
func TestNewPluginMCPHandlers_PolicyIsPerPluginServer(t *testing.T) {
	targetA := newFakePluginMCPServer(t, 1, nil)
	t.Cleanup(targetA.Close)
	targetB := newFakePluginMCPServer(t, 1, nil)
	t.Cleanup(targetB.Close)

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

	listErrCh := make(chan error, 1)
	go func() {
		_, err := listToolNamesNoRequire(t, h)
		listErrCh <- err
	}()

	select {
	case err := <-listErrCh:
		require.NoError(t, err)
	case <-time.After(500 * time.Millisecond):
		require.Fail(t, "external MCP requests should not wait for rebuild proxy discovery")
	}

	select {
	case <-rebuildDone:
	case <-time.After(2 * time.Second):
		require.Fail(t, "rebuild should finish after the bounded proxy discovery context expires")
	}
}

// A nil registry disables aggregation but keeps native tools available.
func TestNewPluginMCPHandlers_NilRegistryIsNoOp(t *testing.T) {
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, h.MCPHandler)
	_ = listToolNames(t, h)
}

// newPerPluginForwarder routes PluginHTTP by the leading /{pluginID} path segment.
func newPerPluginForwarder(t *testing.T, byPluginID map[string]*httptest.Server) *stubPluginAPI {
	t.Helper()
	return &stubPluginAPI{pluginHTTP: func(req *http.Request) *http.Response {
		pluginID, rest := splitPluginHTTPPath(req.URL.Path)
		target, ok := byPluginID[pluginID]
		if !ok {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusNotFound)
			return rec.Result()
		}

		fwd := req.Clone(req.Context())
		fwd.URL.Path = rest
		rec := httptest.NewRecorder()
		target.Config.Handler.ServeHTTP(rec, fwd)
		return rec.Result()
	}}
}

func newHangingAndHealthyPluginForwarder(t *testing.T, hungPluginID string, healthy *httptest.Server, startedHungRequest chan<- struct{}) *stubPluginAPI {
	t.Helper()
	var once sync.Once
	return &stubPluginAPI{pluginHTTP: func(req *http.Request) *http.Response {
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
	}}
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

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
