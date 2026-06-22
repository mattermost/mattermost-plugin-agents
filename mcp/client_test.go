// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const testListToolsMethod = "tools/list"

type fixedPluginAPI struct {
	plugintest.API
	kvGet       func(string) ([]byte, *model.AppError)
	sessionByID map[string]*model.Session
	userByID    map[string]*model.User
}

func (f *fixedPluginAPI) LogDebug(string, ...interface{}) {}

func (f *fixedPluginAPI) LogInfo(string, ...interface{}) {}

func (f *fixedPluginAPI) LogWarn(string, ...interface{}) {}

func (f *fixedPluginAPI) LogError(string, ...interface{}) {}

func (f *fixedPluginAPI) KVGet(key string) ([]byte, *model.AppError) {
	if f.kvGet != nil {
		return f.kvGet(key)
	}
	return nil, nil
}

func (f *fixedPluginAPI) KVSet(string, []byte) *model.AppError {
	return nil
}

func (f *fixedPluginAPI) KVSetWithOptions(string, []byte, model.PluginKVSetOptions) (bool, *model.AppError) {
	return true, nil
}

func (f *fixedPluginAPI) KVDelete(string) *model.AppError {
	return nil
}

func (f *fixedPluginAPI) GetSession(sessionID string) (*model.Session, *model.AppError) {
	if f.sessionByID == nil {
		return nil, nil
	}
	return f.sessionByID[sessionID], nil
}

func (f *fixedPluginAPI) GetUser(userID string) (*model.User, *model.AppError) {
	if f.userByID == nil {
		return nil, nil
	}
	return f.userByID[userID], nil
}

type fakeEmbeddedMCPServer struct {
	ctx    context.Context
	server *mcp.Server
}

func (f *fakeEmbeddedMCPServer) CreateClientTransport(_ string, _ string, _ *pluginapi.Client) (*mcp.InMemoryTransport, error) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() {
		_ = f.server.Run(f.ctx, serverTransport)
	}()
	return clientTransport, nil
}

func newTestMCPServer(pageSize int, toolNames ...string) *mcp.Server {
	return newTestMCPServerWithCapabilities(pageSize, nil, toolNames...)
}

func newTestMCPServerWithCapabilities(pageSize int, capabilities *mcp.ServerCapabilities, toolNames ...string) *mcp.Server {
	var opts *mcp.ServerOptions
	if pageSize > 0 || capabilities != nil {
		opts = &mcp.ServerOptions{
			PageSize:     pageSize,
			Capabilities: capabilities,
		}
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-mcp-server",
		Version: "1.0.0",
	}, opts)
	for _, toolName := range toolNames {
		addTestMCPTool(server, toolName)
	}
	return server
}

func newStaticToolListMCPServer(pageSize int, toolNames ...string) *mcp.Server {
	return newTestMCPServerWithCapabilities(pageSize, &mcp.ServerCapabilities{
		Tools: &mcp.ToolCapabilities{ListChanged: false},
	}, toolNames...)
}

func newEmptyToolsMCPServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{
		Name:    "test-empty-mcp-server",
		Version: "1.0.0",
	}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{ListChanged: true},
		},
	})
}

func addTestMCPTool(server *mcp.Server, toolName string) {
	server.AddTool(&mcp.Tool{
		Name:        toolName,
		Description: fmt.Sprintf("Test tool %s", toolName),
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%s ok", toolName)}},
		}, nil
	})
}

func connectInMemoryTestSession(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func startStreamableMCPServer(t *testing.T, server *mcp.Server) *httptest.Server {
	t.Helper()

	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil))
	t.Cleanup(httpServer.Close)
	return httpServer
}

func newTestToolsCache() *ToolsCache {
	return NewToolsCache(newMockKVService(), &mockLogService{})
}

func newTestLogService() pluginapi.LogService {
	return newTestPluginAPIWithSession("").Log
}

func newTestOAuthManager() *OAuthManager {
	pluginAPI := newTestPluginAPIWithSession("")
	return NewOAuthManager(mmapi.NewClient(pluginAPI), "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", http.DefaultClient, nil)
}

func newTestPluginAPIWithSession(sessionID string) *pluginapi.Client {
	fakeAPI := &fixedPluginAPI{
		sessionByID: map[string]*model.Session{
			sessionID: {
				Id:     sessionID,
				UserId: "test-user",
				Token:  "test-token",
			},
		},
	}
	return pluginapi.NewClient(fakeAPI, nil)
}

func newTestPluginAPIForEmbeddedManager(userID, sessionID string) *pluginapi.Client {
	fakeAPI := &fixedPluginAPI{
		kvGet: func(key string) ([]byte, *model.AppError) {
			if key == buildEmbeddedSessionKey(userID) {
				return []byte(sessionID), nil
			}
			return nil, nil
		},
		sessionByID: map[string]*model.Session{
			sessionID: {
				Id:        sessionID,
				UserId:    userID,
				Token:     "test-token",
				ExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
			},
		},
		userByID: map[string]*model.User{
			userID: {
				Id:    userID,
				Roles: "system_user",
			},
		},
	}
	return pluginapi.NewClient(fakeAPI, nil)
}

func requireToolNames(t *testing.T, tools []llm.Tool, expectedNames ...string) {
	t.Helper()

	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	require.ElementsMatch(t, expectedNames, names)
}

// TestCacheHitBehavior verifies that when tools are in cache,
// they can be retrieved and reused correctly
func TestCacheHitBehavior(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "test_server"
	serverName := "Test MCP Server"
	serverURL := "http://localhost:8080"

	// Simulate tools that would be fetched from an MCP server
	tools := map[string]*mcp.Tool{
		"calculator": {
			Name:        "calculator",
			Description: "Performs calculations",
		},
		"weather": {
			Name:        "weather",
			Description: "Gets weather information",
		},
	}

	// Store tools in cache
	err := cache.SetTools(serverID, serverName, serverURL, tools, time.Now())
	require.NoError(t, err)

	// Retrieve from cache
	cachedTools := cache.GetTools(serverID)
	require.NotNil(t, cachedTools)
	require.Equal(t, len(tools), len(cachedTools))

	// Verify tool details are preserved
	require.Equal(t, "calculator", cachedTools["calculator"].Name)
	require.Equal(t, "Performs calculations", cachedTools["calculator"].Description)
	require.Equal(t, "weather", cachedTools["weather"].Name)
	require.Equal(t, "Gets weather information", cachedTools["weather"].Description)
}

// TestCacheMissBehavior verifies that when tools are not in cache,
// nil is returned (indicating a cache miss)
func TestCacheMissBehavior(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	// Try to get tools for a server that doesn't exist in cache
	cachedTools := cache.GetTools("nonexistent_server")
	require.Nil(t, cachedTools, "Cache miss should return nil")
}

// TestCacheUpdateOnNewTools verifies that cache is updated when new tools are fetched
func TestCacheUpdateOnNewTools(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "test_server"

	// Initially no tools in cache
	cachedTools := cache.GetTools(serverID)
	require.Nil(t, cachedTools)

	// Simulate fetching tools from server and updating cache
	newTools := map[string]*mcp.Tool{
		"file_read": {
			Name:        "file_read",
			Description: "Reads a file",
		},
		"file_write": {
			Name:        "file_write",
			Description: "Writes to a file",
		},
	}

	err := cache.SetTools(serverID, "File Server", "http://fileserver.com", newTools, time.Now())
	require.NoError(t, err)

	// Now tools should be in cache
	cachedTools = cache.GetTools(serverID)
	require.NotNil(t, cachedTools)
	require.Equal(t, 2, len(cachedTools))
	require.Contains(t, cachedTools, "file_read")
	require.Contains(t, cachedTools, "file_write")
}

func TestListAllToolsCollectsPaginatedTools(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2", "tool_3", "tool_4", "tool_5")
	session := connectInMemoryTestSession(t, server)

	tools, err := listAllTools(context.Background(), session)
	require.NoError(t, err)
	require.Len(t, tools, 5)
	for _, toolName := range []string{"tool_1", "tool_2", "tool_3", "tool_4", "tool_5"} {
		require.Contains(t, tools, toolName)
	}
}

func TestListAllToolsSkipsNilTools(t *testing.T) {
	server := newTestMCPServer(0, "tool_1")
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != testListToolsMethod {
				return result, err
			}
			listResult, ok := result.(*mcp.ListToolsResult)
			require.True(t, ok)
			listResult.Tools = append(listResult.Tools, nil)
			return listResult, nil
		}
	})
	session := connectInMemoryTestSession(t, server)

	tools, err := listAllTools(context.Background(), session)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Contains(t, tools, "tool_1")
}

func TestNewClientDiscoversPaginatedRemoteTools(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2", "tool_3", "tool_4", "tool_5")
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()

	client, err := NewClient(context.Background(), "user-id", ServerConfig{
		Name:    "paged",
		BaseURL: httpServer.URL,
		Enabled: true,
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.Len(t, client.Tools(), 5)
	cachedTools := cache.GetTools("paged")
	require.Len(t, cachedTools, 5)
	for _, toolName := range []string{"tool_1", "tool_2", "tool_3", "tool_4", "tool_5"} {
		require.Contains(t, client.Tools(), toolName)
		require.Contains(t, cachedTools, toolName)
	}
}

func TestRemoteReconnectRefreshesToolCatalog(t *testing.T) {
	server := newTestMCPServer(0, "tool_1")
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()

	client, err := NewClient(context.Background(), "user-id", ServerConfig{
		Name:    "remote",
		BaseURL: httpServer.URL,
		Enabled: true,
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.Len(t, client.Tools(), 1)

	addTestMCPTool(server, "new_tool")
	require.NoError(t, client.session.Close())

	result, err := client.CallTool(context.Background(), "tool_1", map[string]any{})
	require.NoError(t, err)
	require.Contains(t, result, "tool_1 ok")
	require.Contains(t, client.Tools(), "new_tool")
	require.Len(t, client.Tools(), 2)
	require.Len(t, cache.GetTools("remote"), 2)
}

func TestNewClientUsesCacheWithoutPaginationCall(t *testing.T) {
	var listCalls atomic.Int32
	server := newStaticToolListMCPServer(2, "server_tool")
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == testListToolsMethod {
				listCalls.Add(1)
				return nil, fmt.Errorf("unexpected tools/list call on cache hit")
			}
			return next(ctx, method, req)
		}
	})
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()
	cachedTools := map[string]*mcp.Tool{
		"cached_tool": {
			Name:        "cached_tool",
			Description: "Cached tool",
			InputSchema: map[string]any{"type": "object"},
		},
	}
	require.NoError(t, cache.SetTools("paged", "Paged", httpServer.URL, cachedTools, time.Now()))

	client, err := NewClient(context.Background(), "user-id", ServerConfig{
		Name:    "paged",
		BaseURL: httpServer.URL,
		Enabled: true,
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.Zero(t, listCalls.Load())
	require.Equal(t, cachedTools, client.Tools())
}

func TestNewClientDoesNotCachePartialPaginationOnError(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2", "tool_3")
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == testListToolsMethod {
				if params, ok := req.GetParams().(*mcp.ListToolsParams); ok && params.Cursor != "" {
					return nil, fmt.Errorf("page 2 failed")
				}
			}
			return next(ctx, method, req)
		}
	})
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()

	client, err := NewClient(context.Background(), "user-id", ServerConfig{
		Name:    "paged",
		BaseURL: httpServer.URL,
		Enabled: true,
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache, false)
	require.Error(t, err)
	require.Nil(t, client)
	require.Nil(t, cache.GetTools("paged"))
}

func TestNewClientErrorsOnEmptyRemoteToolCatalog(t *testing.T) {
	server := newEmptyToolsMCPServer()
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()

	client, err := NewClient(context.Background(), "user-id", ServerConfig{
		Name:    "empty",
		BaseURL: httpServer.URL,
		Enabled: true,
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache, false)
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "no tools found")
	require.Nil(t, cache.GetTools("empty"))
}
