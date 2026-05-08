// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// setupTestLogger registers catch-all .Maybe() mocks for log methods.
// plugintest mocks expand each variadic arg into a separate positional arg,
// so we must register one expectation per arity.
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

func newFakePluginMCPServer(t *testing.T, toolCount int) *httptest.Server {
	t.Helper()
	return newFakePluginMCPServerWithPrefix(t, "test_tool", toolCount)
}

// newFakePluginMCPServerWithPrefix lets callers pick a unique tool-name
// prefix; UserClients.GetTools dedupes by tool name across servers.
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

// newPluginHTTPForwarder returns an mmapi.Client whose PluginHTTP forwards
// to target.Config.Handler. The PluginHTTPRoundTripper URL rewrite is ignored;
// every call dispatches to the test server's root handler.
func newPluginHTTPForwarder(t *testing.T, target *httptest.Server) *fakePluginHTTPClient {
	t.Helper()
	return &fakePluginHTTPClient{
		pluginHTTP: func(req *http.Request) *http.Response {
			rec := httptest.NewRecorder()
			target.Config.Handler.ServeHTTP(rec, req)
			return rec.Result()
		},
	}
}

func TestConnectToPluginServer_HappyPath(t *testing.T) {
	target := newFakePluginMCPServer(t, 2)
	t.Cleanup(target.Close)

	mockAPI := newPluginHTTPForwarder(t, target)

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
	// Second call must not re-dial; tearing down the target proves it.
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

func TestPrepareToolCallMetadata_EmbeddedMergesCallMetadataAndBotUserID(t *testing.T) {
	llmContext := llm.NewContext()
	llmContext.BotUserID = "bot-user-id"
	llmContext.Tools = llm.NewToolStore()
	llmContext.Tools.AddTools([]llm.Tool{
		llm.Tool{Name: "search_posts"}.WithCallMetadata(map[string]any{
			"tool_hooks": map[string]any{
				"search_posts": map[string]any{
					"before_hook_key": "beforeHook:user-1:secret",
				},
			},
		}),
		{Name: "no_hooks"},
	})

	clients := &UserClients{}
	embeddedClient := &Client{config: ServerConfig{Name: EmbeddedClientKey}}
	remoteClient := &Client{config: ServerConfig{Name: "remote-server"}}

	embeddedMeta := clients.prepareToolCallMetadata(embeddedClient, "search_posts", llmContext)
	require.NotNil(t, embeddedMeta)
	require.Equal(t, "bot-user-id", embeddedMeta["bot_user_id"])
	hooks, ok := embeddedMeta["tool_hooks"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, hooks, "search_posts")

	noHookMeta := clients.prepareToolCallMetadata(embeddedClient, "no_hooks", llmContext)
	require.Equal(t, map[string]any{"bot_user_id": "bot-user-id"}, noHookMeta)

	remoteMeta := clients.prepareToolCallMetadata(remoteClient, "search_posts", llmContext)
	require.Nil(t, remoteMeta)
}
