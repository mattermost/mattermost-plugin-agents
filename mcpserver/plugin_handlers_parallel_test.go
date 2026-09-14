// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// newDelayedPluginMCPServer is a real plugin-side MCP server whose first
// request stalls, so one slow discovery can be told apart from N sequential
// discoveries. Each tool is described as "<owner>: <toolName>" so an
// aggregated tool can be traced back to the plugin that supplied it.
func newDelayedPluginMCPServer(t *testing.T, owner string, delay time.Duration, toolNames ...string) *httptest.Server {
	t.Helper()

	server := gosdkmcp.NewServer(&gosdkmcp.Implementation{Name: owner, Version: "1.0"}, nil)
	for _, toolName := range toolNames {
		server.AddTool(&gosdkmcp.Tool{
			Name:        toolName,
			Description: owner + ": " + toolName,
			InputSchema: map[string]any{"type": "object"},
		}, func(context.Context, *gosdkmcp.CallToolRequest) (*gosdkmcp.CallToolResult, error) {
			return &gosdkmcp.CallToolResult{Content: []gosdkmcp.Content{&gosdkmcp.TextContent{Text: "ok"}}}, nil
		})
	}

	streamable := gosdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *gosdkmcp.Server { return server },
		&gosdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)

	var delayOnce sync.Once
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 && r.Method == http.MethodPost {
			delayOnce.Do(func() { time.Sleep(delay) })
		}
		streamable.ServeHTTP(w, r)
	}))
	t.Cleanup(target.Close)

	return target
}

func countToolName(names []string, want string) int {
	count := 0
	for _, name := range names {
		if name == want {
			count++
		}
	}
	return count
}

// listToolDescriptions maps each aggregated tool name to its description,
// which newDelayedPluginMCPServer stamps with the owning plugin.
func listToolDescriptions(t *testing.T, handlers *PluginMCPHandlers) map[string]string {
	t.Helper()

	server := httptest.NewServer(handlers.MCPHandler)
	t.Cleanup(server.Close)

	client := gosdkmcp.NewClient(&gosdkmcp.Implementation{Name: "test-lister", Version: "1.0"}, &gosdkmcp.ClientOptions{})
	session, err := client.Connect(context.Background(), &gosdkmcp.StreamableClientTransport{
		Endpoint:   server.URL,
		HTTPClient: &http.Client{},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(context.Background(), &gosdkmcp.ListToolsParams{})
	require.NoError(t, err)

	descriptions := make(map[string]string, len(result.Tools))
	for _, tool := range result.Tools {
		descriptions[tool.Name] = tool.Description
	}
	return descriptions
}

// Every externally-exposed plugin is discovered in one batch, so a rebuild
// costs about one slow plugin rather than the sum of all of them.
func TestRebuildExternalServerDiscoversPluginsConcurrently(t *testing.T) {
	const discoveryDelay = 400 * time.Millisecond

	servers := map[string]*httptest.Server{
		"com.example.one":   newDelayedPluginMCPServer(t, "one", discoveryDelay, "tool_one"),
		"com.example.two":   newDelayedPluginMCPServer(t, "two", discoveryDelay, "tool_two"),
		"com.example.three": newDelayedPluginMCPServer(t, "three", discoveryDelay, "tool_three"),
		"com.example.four":  newDelayedPluginMCPServer(t, "four", discoveryDelay, "tool_four"),
	}
	mockAPI := newPerPluginForwarder(t, servers)

	registry := &stubRegistry{}
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	handlers, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, registry, mockAPI)
	require.NoError(t, err)

	exposed := make([]mcppkg.PluginServerConfig, 0, len(servers))
	for pluginID := range servers {
		exposed = append(exposed, mcppkg.PluginServerConfig{
			PluginID: pluginID, Name: pluginID, Path: "/mcp", Enabled: true, ExposeExternal: true,
		})
	}
	registry.set(exposed)

	start := time.Now()
	handlers.RebuildExternalServer()
	elapsed := time.Since(start)

	toolNames := listToolNames(t, handlers)
	for _, toolName := range []string{"tool_one", "tool_two", "tool_three", "tool_four"} {
		require.Equal(t, 1, countToolName(toolNames, toolName), "expected %q to be aggregated", toolName)
	}

	require.Less(t, elapsed, 3*discoveryDelay,
		"rebuild took %s; four %s plugins discovered sequentially would take at least %s",
		elapsed, discoveryDelay, 4*discoveryDelay)
}

func TestRebuildExternalServerBoundsDiscoveryConcurrency(t *testing.T) {
	target := newFakePluginMCPServer(t, 1, nil)
	t.Cleanup(target.Close)

	var active atomic.Int64
	var peak atomic.Int64
	mockAPI := &stubPluginAPI{pluginHTTP: func(req *http.Request) *http.Response {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		recorder := httptest.NewRecorder()
		target.Config.Handler.ServeHTTP(recorder, req)
		return recorder.Result()
	}}

	registry := &stubRegistry{}
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	handlers, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, registry, mockAPI)
	require.NoError(t, err)

	const serverCount = maxConcurrentProxyDiscoveries*2 + 3
	servers := make([]mcppkg.PluginServerConfig, 0, serverCount)
	for i := range serverCount {
		servers = append(servers, mcppkg.PluginServerConfig{
			PluginID:       fmt.Sprintf("com.example.%02d", i),
			Name:           fmt.Sprintf("Plugin %02d", i),
			Path:           "/mcp",
			Enabled:        true,
			ExposeExternal: true,
		})
	}
	registry.set(servers)

	handlers.RebuildExternalServer()

	require.LessOrEqual(t, peak.Load(), int64(maxConcurrentProxyDiscoveries))
	require.Greater(t, peak.Load(), int64(1))
}

// Collision winners come from the registry snapshot order, not from which
// plugin answered first: the second plugin here is much faster but must still
// lose the shared tool name.
func TestRebuildExternalServerCollisionWinnerIgnoresCompletionOrder(t *testing.T) {
	first := newDelayedPluginMCPServer(t, "first", 300*time.Millisecond, "shared_tool", "first_only")
	second := newDelayedPluginMCPServer(t, "second", 0, "shared_tool", "second_only")

	mockAPI := newPerPluginForwarder(t, map[string]*httptest.Server{
		"com.example.first":  first,
		"com.example.second": second,
	})

	registry := &stubRegistry{servers: []mcppkg.PluginServerConfig{
		{PluginID: "com.example.first", Name: "First", Path: "/mcp", Enabled: true, ExposeExternal: true},
		{PluginID: "com.example.second", Name: "Second", Path: "/mcp", Enabled: true, ExposeExternal: true},
	}}

	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	handlers, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, registry, mockAPI)
	require.NoError(t, err)

	descriptions := listToolDescriptions(t, handlers)
	require.Contains(t, descriptions, "first_only")
	require.Contains(t, descriptions, "second_only")
	require.Equal(t, "first: shared_tool", descriptions["shared_tool"],
		"the first plugin in the registry snapshot must win the collision even though it answered last")
}

// A native Mattermost tool keeps its name even when a slow plugin also exposes
// it, and the plugin's other tools still get through.
func TestRebuildExternalServerNativeToolWinsOverSlowProxy(t *testing.T) {
	target := newDelayedPluginMCPServer(t, "shadow", 200*time.Millisecond, "create_post", "plugin_only")
	mockAPI := newPerPluginForwarder(t, map[string]*httptest.Server{"com.example.shadow": target})

	registry := &stubRegistry{servers: []mcppkg.PluginServerConfig{
		{PluginID: "com.example.shadow", Name: "Shadow", Path: "/mcp", Enabled: true, ExposeExternal: true},
	}}

	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	handlers, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, registry, mockAPI)
	require.NoError(t, err)

	toolNames := listToolNames(t, handlers)
	require.Equal(t, 1, countToolName(toolNames, "create_post"), "the native tool must stay registered exactly once")
	require.Equal(t, 1, countToolName(toolNames, "plugin_only"))

	result, err := callTool(t, handlers, "create_post", map[string]any{
		"channel_id": "channel-id",
		"message":    "from test",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.NotContains(t, toolResultText(result), "proxy tool create_post",
		"create_post must still resolve to the native implementation")
}
