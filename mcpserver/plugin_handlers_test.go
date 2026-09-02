// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
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
	return listToolNamesFromHandler(t, h.MCPHandler)
}

func listToolNames(t *testing.T, h *PluginMCPHandlers) []string {
	t.Helper()
	names, err := listToolNamesNoRequire(t, h)
	require.NoError(t, err)
	return names
}

func listToolNamesAs(t *testing.T, h *PluginMCPHandlers, userID string) []string {
	t.Helper()
	names, err := listToolNamesFromHandler(t, injectUserID(h.MCPHandler, userID))
	require.NoError(t, err)
	return names
}

func listToolNamesFromHandler(t *testing.T, handler http.Handler) ([]string, error) {
	t.Helper()
	ts := httptest.NewServer(handler)
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

func callTool(t *testing.T, h *PluginMCPHandlers, name string, args map[string]any) (*gosdkmcp.CallToolResult, error) {
	t.Helper()
	return callToolWithHandler(t, h.MCPHandler, name, args)
}

func callToolAs(t *testing.T, h *PluginMCPHandlers, userID, name string, args map[string]interface{}) (*gosdkmcp.CallToolResult, error) {
	t.Helper()
	return callToolWithHandler(t, injectUserID(h.MCPHandler, userID), name, args)
}

func callToolWithHandler(t *testing.T, handler http.Handler, name string, args map[string]interface{}) (*gosdkmcp.CallToolResult, error) {
	t.Helper()
	ts := httptest.NewServer(handler)
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

func injectUserID(next http.Handler, userID string) http.Handler {
	if userID == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), auth.UserIDContextKey, userID)))
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
		&gosdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true},
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

	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI, nil, nil)
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
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI, nil, nil)
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

	result, err := callTool(t, h, "create_post", map[string]any{
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
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI, nil, nil)
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
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI, nil, nil)
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

	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI, nil, nil)
	require.NoError(t, err)

	initial := listToolNames(t, h)
	for _, n := range initial {
		require.NotEqual(t, "test_tool_0", n, "precondition: no proxy tools before registration")
	}

	reg.set([]mcppkg.PluginServerConfig{{PluginID: "com.example.late", Name: "Late", Path: "/mcp", Enabled: true, ExposeExternal: true}})
	h.RebuildExternalServer()

	after := listToolNames(t, h)
	var sawProxy bool
	if slices.Contains(after, "test_tool_0") {
		sawProxy = true
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
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI, nil, nil)
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
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI, nil, nil)
	require.NoError(t, err)
	h.proxyDiscoveryTimeout = 25 * time.Millisecond

	reg.set([]mcppkg.PluginServerConfig{
		{PluginID: "com.example.hung", Name: "Hung", Path: "/mcp", Enabled: true, ExposeExternal: true},
		{PluginID: "com.example.healthy", Name: "Healthy", Path: "/mcp", Enabled: true, ExposeExternal: true},
	})
	h.RebuildExternalServer()

	after := listToolNames(t, h)
	var sawHealthy bool
	if slices.Contains(after, "test_tool_0") {
		sawHealthy = true
	}
	require.True(t, sawHealthy, "healthy plugins should still be aggregated after another plugin times out")
}

func TestRebuildExternalServer_DoesNotBlockExternalRequestsWhileDiscovering(t *testing.T) {
	startedHungRequest := make(chan struct{})
	reg := &stubRegistry{servers: nil}
	mockAPI := newHangingAndHealthyPluginForwarder(t, "com.example.hung", nil, startedHungRequest)
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI, nil, nil)
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
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, h.MCPHandler)
	_ = listToolNames(t, h)
}

type stubMCPAccessChecker struct {
	denied  map[string]bool
	evalErr error
}

func (s *stubMCPAccessChecker) CanUseMCPServer(_ context.Context, _, serverID string) error {
	if s.evalErr != nil {
		return s.evalErr
	}
	if s.denied[serverID] {
		return errors.New("denied")
	}
	return nil
}

func TestPluginMCPHandlers_AccessFilter(t *testing.T) {
	const (
		userID      = "user-access-filter"
		embeddedID  = "eeeeeeeeeeeeeeeeeeeeeeeeee"
		pluginSrvID = "pppppppppppppppppppppppppp"
		nativeTool  = "create_post"
		pluginTool  = "test_tool_0"
	)

	target := newFakePluginMCPServer(t, 1, nil)
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)
	reg := &stubRegistry{servers: []mcppkg.PluginServerConfig{{
		ID:             pluginSrvID,
		PluginID:       "com.example.ext",
		Name:           "Ext",
		Path:           "/mcp",
		Enabled:        true,
		ExposeExternal: true,
	}}}
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)

	tests := []struct {
		name       string
		checker    mcppkg.ServerAccessChecker
		embeddedID string
		userID     string
		wantNative bool
		wantPlugin bool
		// unknownTool, when set, is called and must be rejected before reaching the SDK.
		unknownTool string
	}{
		{
			name:       "denied embedded omits native keeps plugin",
			checker:    &stubMCPAccessChecker{denied: map[string]bool{embeddedID: true}},
			embeddedID: embeddedID,
			userID:     userID,
			wantPlugin: true,
		},
		{
			name:       "denied plugin omits plugin keeps native",
			checker:    &stubMCPAccessChecker{denied: map[string]bool{pluginSrvID: true}},
			embeddedID: embeddedID,
			userID:     userID,
			wantNative: true,
		},
		{
			name:       "nil checker unrestricted",
			embeddedID: embeddedID,
			userID:     userID,
			wantNative: true,
			wantPlugin: true,
		},
		{
			name:       "missing userID fail closed",
			checker:    &stubMCPAccessChecker{},
			embeddedID: embeddedID,
		},
		{
			name:       "evaluation error fail closed",
			checker:    &stubMCPAccessChecker{evalErr: errors.New("pdp unavailable")},
			embeddedID: embeddedID,
			userID:     userID,
		},
		{
			name: "empty embedded ID does not deny native",
			checker: &stubMCPAccessChecker{denied: map[string]bool{
				"": true,
			}},
			userID:     userID,
			wantNative: true,
			wantPlugin: true,
		},
		{
			name:        "unknown tool name fail closed",
			checker:     &stubMCPAccessChecker{},
			embeddedID:  embeddedID,
			userID:      userID,
			wantNative:  true,
			wantPlugin:  true,
			unknownTool: "no_such_tool",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg, mockAPI, tc.checker, func() string {
				return tc.embeddedID
			})
			require.NoError(t, err)

			names := listToolNamesAs(t, h, tc.userID)
			require.Equal(t, tc.wantNative, slices.Contains(names, nativeTool), "native tool listing")
			require.Equal(t, tc.wantPlugin, slices.Contains(names, pluginTool), "plugin tool listing")

			nativeResult, nativeErr := callToolAs(t, h, tc.userID, nativeTool, map[string]interface{}{
				"channel_id": "channel-id",
				"message":    "from test",
			})
			if !tc.wantNative {
				require.Error(t, nativeErr)
				require.Contains(t, nativeErr.Error(), "tool not available")
			} else {
				require.NoError(t, nativeErr)
				require.True(t, nativeResult.IsError)
				require.Contains(t, toolResultText(nativeResult), "session authentication provider requires token resolver")
			}

			pluginResult, pluginErr := callToolAs(t, h, tc.userID, pluginTool, map[string]interface{}{
				"message": "hi",
			})
			if !tc.wantPlugin {
				require.Error(t, pluginErr)
				require.Contains(t, pluginErr.Error(), "tool not available")
			} else {
				require.NoError(t, pluginErr)
				require.False(t, pluginResult.IsError)
			}

			if tc.unknownTool != "" {
				_, unknownErr := callToolAs(t, h, tc.userID, tc.unknownTool, map[string]interface{}{})
				require.Error(t, unknownErr)
				require.Contains(t, unknownErr.Error(), "tool not available")
			}
		})
	}
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
