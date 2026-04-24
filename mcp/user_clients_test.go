// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// setupTestLogger registers catch-all .Maybe() mocks for all log methods and
// arg counts the mcp package tends to use. plugintest.API.LogDebug/LogError
// are variadic-expanded in the mock (each variadic arg becomes a separate
// positional arg under the hood), so each arity requires its own expectation.
func setupTestLogger(mockAPI *plugintest.API) {
	for _, method := range []string{"LogDebug", "LogError", "LogWarn", "LogInfo"} {
		for arity := 1; arity <= 16; arity++ {
			args := make([]interface{}, arity)
			for i := range args {
				args[i] = mock.Anything
			}
			mockAPI.On(method, args...).Return().Maybe()
		}
	}
}

// newFakePluginMCPServer spins up an httptest.Server exposing a go-sdk MCP
// Streamable HTTP handler with toolCount tools prefixed "test_tool_0"..."test_tool_{N-1}".
func newFakePluginMCPServer(t *testing.T, toolCount int) *httptest.Server {
	t.Helper()
	return newFakePluginMCPServerWithPrefix(t, "test_tool", toolCount)
}

// newFakePluginMCPServerWithPrefix is like newFakePluginMCPServer but allows
// the caller to choose a unique tool-name prefix. This matters when multiple
// fake servers are composed in the same UserClients, because UserClients.GetTools
// drops duplicate tool names across servers (first server wins) — see
// user_clients.go:GetTools. Distinct prefixes prevent silent deduping.
func newFakePluginMCPServerWithPrefix(t *testing.T, prefix string, toolCount int) *httptest.Server {
	t.Helper()
	srv := gosdkmcp.NewServer(&gosdkmcp.Implementation{Name: "fake", Version: "1.0"}, nil)
	type echoIn struct {
		Message string `json:"message"`
	}
	type echoOut struct {
		Echo string `json:"echo"`
	}
	for i := 0; i < toolCount; i++ {
		name := fmt.Sprintf("%s_%d", prefix, i)
		gosdkmcp.AddTool(srv, &gosdkmcp.Tool{Name: name, Description: "test"}, func(_ context.Context, _ *gosdkmcp.CallToolRequest, in echoIn) (*gosdkmcp.CallToolResult, echoOut, error) {
			return nil, echoOut{Echo: in.Message}, nil
		})
	}
	h := gosdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *gosdkmcp.Server { return srv },
		&gosdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
	return httptest.NewServer(h)
}

// newPluginHTTPForwarder returns a mock mmapi.Client whose PluginHTTP forwards
// to target.Config.Handler via an httptest.ResponseRecorder. It ignores the
// URL rewrite performed by PluginHTTPRoundTripper and always dispatches to the
// test server's root handler — this is the reverse of what PluginHTTP does in
// production but suffices for unit testing ConnectToPluginServer end-to-end.
func newPluginHTTPForwarder(t *testing.T, target *httptest.Server) *mocks.MockClient {
	t.Helper()
	m := mocks.NewMockClient(t)
	m.EXPECT().PluginHTTP(mock.Anything).RunAndReturn(func(req *http.Request) *http.Response {
		rec := httptest.NewRecorder()
		target.Config.Handler.ServeHTTP(rec, req)
		return rec.Result()
	}).Maybe()
	return m
}

func TestConnectToPluginServer_HappyPath(t *testing.T) {
	target := newFakePluginMCPServer(t, 2)
	t.Cleanup(target.Close)

	mockAPI := newPluginHTTPForwarder(t, target)

	// Minimal UserClients. httpClient and toolsCache not used on the plugin path.
	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)
	uc := NewUserClients("alice", client.Log, nil, nil, nil)

	cfg := PluginServerConfig{
		PluginID: "com.mattermost.plugin-mcp-demo",
		Name:     "MCP Demo",
		Path:     "/mcp",
		Enabled:  true,
	}

	err := uc.ConnectToPluginServer(context.Background(), cfg, mockAPI)
	require.NoError(t, err)

	originKey := "plugin://" + cfg.PluginID
	c, ok := uc.clients[originKey]
	require.True(t, ok, "expected client under origin key %s", originKey)
	require.NotNil(t, c)
	require.Equal(t, originKey, c.config.BaseURL)
	require.Len(t, c.tools, 2)
}

func TestConnectToPluginServer_Idempotent(t *testing.T) {
	target := newFakePluginMCPServer(t, 1)
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)

	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)
	uc := NewUserClients("alice", client.Log, nil, nil, nil)

	cfg := PluginServerConfig{PluginID: "com.example.test", Name: "Test", Path: "/mcp", Enabled: true}

	require.NoError(t, uc.ConnectToPluginServer(context.Background(), cfg, mockAPI))
	// Second call should be a no-op and not fail even if the remote server is torn down.
	target.Close()
	require.NoError(t, uc.ConnectToPluginServer(context.Background(), cfg, mockAPI))
}

func TestConnectToPluginServer_NilAPI(t *testing.T) {
	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)
	uc := NewUserClients("alice", client.Log, nil, nil, nil)
	err := uc.ConnectToPluginServer(context.Background(), PluginServerConfig{PluginID: "x", Path: "/mcp"}, nil)
	require.Error(t, err)
}
