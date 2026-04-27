// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newFakePluginMCPServer spins up an httptest.Server exposing a go-sdk MCP
// Streamable HTTP handler with toolCount echo tools named "test_tool_N".
// If sawUserIDOut is non-nil, each incoming request's X-Mattermost-UserID
// header is written to *sawUserIDOut before the MCP handler runs.
func newFakePluginMCPServer(t *testing.T, toolCount int, sawUserIDOut *string) *httptest.Server {
	t.Helper()
	srv := gosdkmcp.NewServer(&gosdkmcp.Implementation{Name: "fake", Version: "1.0"}, nil)
	type echoIn struct {
		Message string `json:"message"`
	}
	type echoOut struct {
		Echo string `json:"echo"`
	}
	for i := 0; i < toolCount; i++ {
		name := fmt.Sprintf("test_tool_%d", i)
		gosdkmcp.AddTool(srv, &gosdkmcp.Tool{Name: name, Description: "test"}, func(_ context.Context, _ *gosdkmcp.CallToolRequest, in echoIn) (*gosdkmcp.CallToolResult, echoOut, error) {
			return nil, echoOut{Echo: in.Message}, nil
		})
	}
	streamable := gosdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *gosdkmcp.Server { return srv },
		&gosdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sawUserIDOut != nil {
			if v := r.Header.Get("X-Mattermost-UserID"); v != "" {
				*sawUserIDOut = v
			}
		}
		streamable.ServeHTTP(w, r)
	}))
}

// newPluginHTTPForwarder returns a mock mmapi.Client whose PluginHTTP forwards
// the request to target.Config.Handler via an httptest.ResponseRecorder.
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

func TestBuildProxyTools_HappyPath(t *testing.T) {
	target := newFakePluginMCPServer(t, 2, nil)
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)

	cfg := mcppkg.PluginServerConfig{PluginID: "com.example.demo", Name: "Demo", Path: "/mcp", Enabled: true, ExposeExternal: true}
	tools, handlers, err := BuildProxyTools(context.Background(), cfg, mockAPI)
	require.NoError(t, err)
	require.Len(t, tools, 2)
	require.Len(t, handlers, 2)
	// Names are passed through verbatim (no rename in proxy layer).
	require.Equal(t, "test_tool_0", tools[0].Name)
	require.Equal(t, "test_tool_1", tools[1].Name)
}

// TestBuildProxyTools_HandlerPropagatesUserID verifies end-to-end
// X-Mattermost-UserID propagation into the source plugin request.
func TestBuildProxyTools_HandlerPropagatesUserID(t *testing.T) {
	var sawUserID string
	target := newFakePluginMCPServer(t, 1, &sawUserID)
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)

	cfg := mcppkg.PluginServerConfig{PluginID: "com.example.demo", Name: "Demo", Path: "/mcp", Enabled: true, ExposeExternal: true}
	_, handlers, err := BuildProxyTools(context.Background(), cfg, mockAPI)
	require.NoError(t, err)
	require.Len(t, handlers, 1)

	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "alice")
	_, callErr := handlers[0](ctx, &gosdkmcp.CallToolRequest{Params: &gosdkmcp.CallToolParamsRaw{Name: "test_tool_0", Arguments: []byte(`{"message":"hi"}`)}})
	require.NoError(t, callErr)
	require.Equal(t, "alice", sawUserID, "handler should set X-Mattermost-UserID from auth.UserIDContextKey")
}

func TestBuildProxyTools_HandlerMissingUserID(t *testing.T) {
	target := newFakePluginMCPServer(t, 1, nil)
	t.Cleanup(target.Close)
	mockAPI := newPluginHTTPForwarder(t, target)

	cfg := mcppkg.PluginServerConfig{PluginID: "com.example.demo", Name: "Demo", Path: "/mcp", Enabled: true, ExposeExternal: true}
	_, handlers, err := BuildProxyTools(context.Background(), cfg, mockAPI)
	require.NoError(t, err)

	// No auth.UserIDContextKey in context -> handler returns an error.
	_, callErr := handlers[0](context.Background(), &gosdkmcp.CallToolRequest{Params: &gosdkmcp.CallToolParamsRaw{Name: "test_tool_0"}})
	require.Error(t, callErr)
}

func TestBuildProxyTools_UnreachablePluginReturnsError(t *testing.T) {
	mockAPI := mocks.NewMockClient(t)
	mockAPI.EXPECT().PluginHTTP(mock.Anything).Return((*http.Response)(nil)).Maybe()

	cfg := mcppkg.PluginServerConfig{PluginID: "com.example.dead", Name: "Dead", Path: "/mcp", Enabled: true, ExposeExternal: true}
	_, _, err := BuildProxyTools(context.Background(), cfg, mockAPI)
	require.Error(t, err, "unreachable plugin should return a non-nil error; caller decides whether to skip or propagate")
}

func TestBuildProxyTools_NilSourcePluginAPI(t *testing.T) {
	cfg := mcppkg.PluginServerConfig{PluginID: "x", Path: "/mcp"}
	_, _, err := BuildProxyTools(context.Background(), cfg, nil)
	require.Error(t, err)
}
