// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func cleanupTestClientManager(manager *ClientManager) {
	manager.clientsMu.Lock()
	defer manager.clientsMu.Unlock()
	for _, userClient := range manager.clients {
		userClient.Close()
	}
	manager.clients = make(map[string]*UserClients)
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
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache)
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
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache)
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
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache)
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
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache)
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "no tools found")
	require.Nil(t, cache.GetTools("empty"))
}

func TestRemoteToolListChangedInvalidatesCacheAndClientTools(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2", "tool_3")
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()

	client, err := NewClient(context.Background(), "user-id", ServerConfig{
		Name:    "paged",
		BaseURL: httpServer.URL,
		Enabled: true,
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NotEmpty(t, client.Tools())
	require.NotNil(t, cache.GetTools("paged"))

	addTestMCPTool(server, "new_tool")

	require.Eventually(t, func() bool {
		return len(client.Tools()) == 0 && cache.GetTools("paged") == nil
	}, 5*time.Second, 10*time.Millisecond)
}

func TestRemoteToolListChangedNextGetToolsForUserRediscoversTools(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2")
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()
	manager := &ClientManager{
		config: Config{
			Servers: []ServerConfig{{
				Name:    "paged",
				BaseURL: httpServer.URL,
				Enabled: true,
			}},
		},
		log:          newTestLogService(),
		clients:      make(map[string]*UserClients),
		activity:     make(map[string]time.Time),
		oauthManager: newTestOAuthManager(),
		httpClient:   httpServer.Client(),
		toolsCache:   cache,
	}
	t.Cleanup(func() { cleanupTestClientManager(manager) })

	var tools []llm.Tool
	var mcpErrors *Errors
	require.Eventually(t, func() bool {
		tools, mcpErrors = manager.GetToolsForUser("user-id")
		if mcpErrors != nil || len(cache.GetTools("paged")) != 2 {
			return false
		}
		toolNames := make(map[string]bool, len(tools))
		for _, tool := range tools {
			toolNames[tool.Name] = true
		}
		return len(tools) == 2 && toolNames["paged__tool_1"] && toolNames["paged__tool_2"]
	}, 5*time.Second, 10*time.Millisecond)
	require.Nil(t, mcpErrors)
	requireToolNames(t, tools, "paged__tool_1", "paged__tool_2")
	require.Len(t, cache.GetTools("paged"), 2)

	addTestMCPTool(server, "new_tool")

	require.Eventually(t, func() bool {
		manager.clientsMu.RLock()
		userClient := manager.clients["user-id"]
		manager.clientsMu.RUnlock()
		if userClient == nil {
			return false
		}
		client := userClient.clients["paged"]
		return client != nil && len(client.Tools()) == 0 && cache.GetTools("paged") == nil
	}, 5*time.Second, 10*time.Millisecond)

	tools, mcpErrors = manager.GetToolsForUser("user-id")
	require.Nil(t, mcpErrors)
	requireToolNames(t, tools, "paged__new_tool", "paged__tool_1", "paged__tool_2")
	require.Len(t, cache.GetTools("paged"), 3)
}

func TestToolListChangedDuringRediscoveryKeepsClientDirty(t *testing.T) {
	listBlocked := make(chan struct{})
	releaseList := make(chan struct{})
	var blocked atomic.Bool
	server := newTestMCPServer(0, "tool_1")
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != testListToolsMethod || !blocked.CompareAndSwap(false, true) {
				return result, err
			}

			close(listBlocked)
			select {
			case <-releaseList:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return result, nil
		}
	})
	session := connectInMemoryTestSession(t, server)
	cache := newTestToolsCache()
	client := &Client{
		session:    session,
		config:     ServerConfig{Name: "server", BaseURL: "https://example.com"},
		tools:      make(map[string]*mcp.Tool),
		toolsDirty: true,
		userID:     "user-id",
		log:        newTestLogService(),
		toolsCache: cache,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.ensureDiscoveredTools(context.Background())
	}()

	select {
	case <-listBlocked:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for rediscovery to enter tools/list")
	}

	client.invalidateDiscoveredTools(context.Background(), cache, "server", true)
	close(releaseList)

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for rediscovery to finish")
	}

	client.toolsMu.RLock()
	require.True(t, client.toolsDirty)
	client.toolsMu.RUnlock()
	require.Empty(t, client.Tools())
	require.Nil(t, cache.GetTools("server"))

	addTestMCPTool(server, "tool_2")
	require.NoError(t, client.ensureDiscoveredTools(context.Background()))

	client.toolsMu.RLock()
	require.False(t, client.toolsDirty)
	client.toolsMu.RUnlock()
	require.Contains(t, client.Tools(), "tool_1")
	require.Contains(t, client.Tools(), "tool_2")
	require.Len(t, cache.GetTools("server"), 2)
}

func TestRemoteToolListChangedWithNilCacheClearsClientTools(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2", "tool_3")
	httpServer := startStreamableMCPServer(t, server)

	client, err := NewClient(context.Background(), "user-id", ServerConfig{
		Name:    "paged",
		BaseURL: httpServer.URL,
		Enabled: true,
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NotEmpty(t, client.Tools())

	addTestMCPTool(server, "new_tool")

	require.Eventually(t, func() bool {
		return len(client.Tools()) == 0
	}, 5*time.Second, 10*time.Millisecond)
}

func TestRemoteToolListChangedForStaticOAuthSkipsSharedCacheInvalidation(t *testing.T) {
	server := newTestMCPServer(2, "server_tool")
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()
	require.NoError(t, cache.SetTools("oauth-server", "OAuth Server", httpServer.URL, map[string]*mcp.Tool{
		"cached_tool": {
			Name:        "cached_tool",
			Description: "Cached tool",
			InputSchema: map[string]any{"type": "object"},
		},
	}, time.Now()))

	client, err := NewClient(context.Background(), "user-id", ServerConfig{
		Name:         "oauth-server",
		BaseURL:      httpServer.URL,
		Enabled:      true,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.Contains(t, client.Tools(), "server_tool")
	require.NoError(t, cache.SetTools("oauth-server", "OAuth Server", httpServer.URL, map[string]*mcp.Tool{
		"cached_after_connect": {
			Name:        "cached_after_connect",
			Description: "Cached after connect",
			InputSchema: map[string]any{"type": "object"},
		},
	}, time.Now()))
	require.NotNil(t, cache.GetTools("oauth-server"))

	addTestMCPTool(server, "new_tool")

	require.Eventually(t, func() bool {
		return len(client.Tools()) == 0
	}, 5*time.Second, 10*time.Millisecond)
	require.NotNil(t, cache.GetTools("oauth-server"))
}

func TestRemoteToolListChangedNotificationStormIsIdempotent(t *testing.T) {
	server := newTestMCPServer(2, "tool_1")
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()

	client, err := NewClient(context.Background(), "user-id", ServerConfig{
		Name:    "storm",
		BaseURL: httpServer.URL,
		Enabled: true,
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	addTestMCPTool(server, "storm_tool")
	server.RemoveTools("storm_tool")
	addTestMCPTool(server, "storm_tool")

	require.Eventually(t, func() bool {
		return len(client.Tools()) == 0 && cache.GetTools("storm") == nil
	}, 5*time.Second, 10*time.Millisecond)
}

func TestEmbeddedCreateClientDiscoversPaginatedTools(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2", "tool_3", "tool_4", "tool_5")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	embeddedClient := NewEmbeddedServerClient(&fakeEmbeddedMCPServer{ctx: ctx, server: server}, newTestLogService(), nil)
	client, err := embeddedClient.CreateClient(context.Background(), "user-id", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.Len(t, client.Tools(), 5)
	for _, toolName := range []string{"tool_1", "tool_2", "tool_3", "tool_4", "tool_5"} {
		require.Contains(t, client.Tools(), toolName)
	}
}

func TestEmbeddedToolListChangedInvalidatesCacheAndClientTools(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2", "tool_3")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := newTestToolsCache()
	require.NoError(t, cache.SetTools(EmbeddedClientKey, EmbeddedServerName, EmbeddedClientKey, map[string]*mcp.Tool{
		"cached_tool": {
			Name:        "cached_tool",
			Description: "Cached tool",
			InputSchema: map[string]any{"type": "object"},
		},
	}, time.Now()))

	embeddedClient := NewEmbeddedServerClientWithCache(&fakeEmbeddedMCPServer{ctx: ctx, server: server}, newTestLogService(), nil, cache)
	client, err := embeddedClient.CreateClient(context.Background(), "user-id", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NotEmpty(t, client.Tools())
	require.NotNil(t, cache.GetTools(EmbeddedClientKey))

	addTestMCPTool(server, "new_tool")

	require.Eventually(t, func() bool {
		return len(client.Tools()) == 0 && cache.GetTools(EmbeddedClientKey) == nil
	}, 5*time.Second, 10*time.Millisecond)
}

func TestEmbeddedToolListChangedNextGetToolsForUserRediscoversTools(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := newTestToolsCache()
	pluginAPI := newTestPluginAPIForEmbeddedManager("user-id", "session-id")
	embeddedClient := NewEmbeddedServerClientWithCache(&fakeEmbeddedMCPServer{ctx: ctx, server: server}, pluginAPI.Log, pluginAPI, cache)
	manager := &ClientManager{
		config: Config{
			EmbeddedServer: EmbeddedServerConfig{Enabled: true},
		},
		log:            pluginAPI.Log,
		pluginAPI:      pluginAPI,
		clients:        make(map[string]*UserClients),
		activity:       make(map[string]time.Time),
		embeddedClient: embeddedClient,
		toolsCache:     cache,
	}
	t.Cleanup(func() { cleanupTestClientManager(manager) })

	tools, mcpErrors := manager.GetToolsForUser("user-id")
	require.Nil(t, mcpErrors)
	requireToolNames(t, tools, "mattermost__tool_1", "mattermost__tool_2")

	addTestMCPTool(server, "new_tool")

	require.Eventually(t, func() bool {
		manager.clientsMu.RLock()
		userClient := manager.clients["user-id"]
		manager.clientsMu.RUnlock()
		if userClient == nil {
			return false
		}
		client := userClient.clients[EmbeddedClientKey]
		return client != nil && len(client.Tools()) == 0
	}, 5*time.Second, 10*time.Millisecond)

	tools, mcpErrors = manager.GetToolsForUser("user-id")
	require.Nil(t, mcpErrors)
	requireToolNames(t, tools, "mattermost__new_tool", "mattermost__tool_1", "mattermost__tool_2")
	require.Len(t, cache.GetTools(EmbeddedClientKey), 3)
}

func TestEmbeddedReconnectKeepsPaginatedDiscovery(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2", "tool_3")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pluginAPI := newTestPluginAPIWithSession("session-id")

	embeddedClient := NewEmbeddedServerClient(&fakeEmbeddedMCPServer{ctx: ctx, server: server}, pluginAPI.Log, pluginAPI)
	client, err := embeddedClient.CreateClient(context.Background(), "test-user", "session-id")
	require.NoError(t, err)
	require.Len(t, client.Tools(), 3)
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, client.session.Close())
	result, err := client.CallTool(context.Background(), "tool_1", map[string]any{})
	require.NoError(t, err)
	require.Contains(t, result, "tool_1 ok")
	require.Len(t, client.Tools(), 3)

	addTestMCPTool(server, "new_tool")
	require.Eventually(t, func() bool {
		return len(client.Tools()) == 0
	}, 5*time.Second, 10*time.Millisecond)
}

func TestClientToolsReturnsCopyAndSurvivesConcurrentInvalidation(t *testing.T) {
	client := &Client{
		config: ServerConfig{Name: "server", BaseURL: "https://example.com"},
		tools: map[string]*mcp.Tool{
			"tool_1": {Name: "tool_1"},
		},
		userID: "user-id",
		log:    newTestLogService(),
	}

	tools := client.Tools()
	delete(tools, "tool_1")
	require.Contains(t, client.Tools(), "tool_1")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = client.Tools()
		}
	}()
	for i := 0; i < 100; i++ {
		client.invalidateDiscoveredTools(context.Background(), nil, "server", false)
	}
	<-done
	require.Empty(t, client.Tools())
}

func TestExtractOAuthMetadataURL(t *testing.T) {
	tests := []struct {
		name      string
		errMsg    string
		wantURL   string
		wantFound bool
	}{
		{
			name:      "nil error",
			errMsg:    "",
			wantURL:   "",
			wantFound: false,
		},
		{
			name:      "unrelated error",
			errMsg:    "connection refused",
			wantURL:   "",
			wantFound: false,
		},
		{
			name:      "metadata URL without wrapped error",
			errMsg:    "OAuth authentication needed for resource at https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/",
			wantURL:   "https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/",
			wantFound: true,
		},
		{
			name:      "metadata URL with wrapped error",
			errMsg:    "OAuth authentication needed for resource at https://example.com/.well-known/oauth-protected-resource: Got error: token refresh failed",
			wantURL:   "https://example.com/.well-known/oauth-protected-resource",
			wantFound: true,
		},
		{
			name:      "metadata URL embedded in longer error chain",
			errMsg:    "failed to connect: OAuth authentication needed for resource at https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/",
			wantURL:   "https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/",
			wantFound: true,
		},
		{
			name:      "empty metadata URL",
			errMsg:    "OAuth authentication needed for resource at ",
			wantURL:   "",
			wantFound: false,
		},
		{
			name:      "URL with port",
			errMsg:    "OAuth authentication needed for resource at https://example.com:8443/.well-known/oauth-protected-resource",
			wantURL:   "https://example.com:8443/.well-known/oauth-protected-resource",
			wantFound: true,
		},
		{
			name:      "URL with port and wrapped error",
			errMsg:    "OAuth authentication needed for resource at https://example.com:8443/.well-known/oauth-protected-resource: Got error: something failed",
			wantURL:   "https://example.com:8443/.well-known/oauth-protected-resource",
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = fmt.Errorf("%s", tt.errMsg)
			}
			gotURL, gotFound := extractOAuthMetadataURL(err)
			require.Equal(t, tt.wantFound, gotFound)
			require.Equal(t, tt.wantURL, gotURL)
		})
	}
}

func TestClientOAuthNeededError(t *testing.T) {
	client := &Client{
		config: ServerConfig{
			Name: "OAuth Server",
		},
		oauthManager: &OAuthManager{
			callbackURL: "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback",
		},
	}

	tests := []struct {
		name string
		err  error
	}{
		{
			name: "mcp unauthorized error",
			err: &mcpUnauthorized{
				metadataURL: "https://oauth.example.com/.well-known/oauth-protected-resource",
			},
		},
		{
			name: "string matched oauth error",
			err:  fmt.Errorf("OAuth authentication needed for resource at https://oauth.example.com/.well-known/oauth-protected-resource"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.oauthNeededError(tt.err)
			require.Error(t, err)

			var oauthErr *OAuthNeededError
			require.ErrorAs(t, err, &oauthErr)
			authURL, parseErr := url.Parse(oauthErr.AuthURL())
			require.NoError(t, parseErr)
			require.Equal(t, "https://mattermost.example.com", authURL.Scheme+"://"+authURL.Host)
			require.Equal(t, "/plugins/mattermost-ai/mcp/oauth/OAuth%20Server/start", authURL.EscapedPath())
			require.Equal(t, "https://oauth.example.com/.well-known/oauth-protected-resource", authURL.Query().Get("resource_metadata"))
		})
	}
}

// TestNilCacheHandling verifies that nil cache is handled gracefully in the cache code
func TestNilCacheHandling(t *testing.T) {
	// This test documents that the cache code handles nil properly
	// The actual NewClient function checks if toolsCache is nil before using it
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	// Verify cache can be created and used
	require.NotNil(t, cache)

	// Test that GetTools returns nil for non-existent server (not a panic)
	tools := cache.GetTools("nonexistent")
	require.Nil(t, tools)
}

func TestShouldUseSharedToolsCache(t *testing.T) {
	tests := []struct {
		name         string
		serverConfig ServerConfig
		expected     bool
	}{
		{
			name: "server without static oauth creds uses shared cache",
			serverConfig: ServerConfig{
				Name:    "no-oauth",
				BaseURL: "https://example.com",
			},
			expected: true,
		},
		{
			name: "server with static oauth creds skips shared cache",
			serverConfig: ServerConfig{
				Name:         "static-oauth",
				BaseURL:      "https://example.com",
				ClientID:     "client-id",
				ClientSecret: "client-secret",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, shouldUseSharedToolsCache(tt.serverConfig))
		})
	}
}

func TestInvalidateSharedToolsCacheForOAuthDiscovery(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "oauth-server"
	tools := map[string]*mcp.Tool{
		"search": {
			Name:        "search",
			Description: "Searches data",
		},
	}

	err := cache.SetTools(serverID, "OAuth Server", "https://example.com", tools, time.Now())
	require.NoError(t, err)
	require.NotNil(t, cache.GetTools(serverID))

	invalidateSharedToolsCacheForOAuthDiscovery(cache, log, "user-id", serverID, ServerConfig{
		Name:         serverID,
		BaseURL:      "https://example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, false)

	require.Nil(t, cache.GetTools(serverID))
}

func TestInvalidateSharedToolsCacheForOAuthDiscoveryKeepsCacheWithStoredToken(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "oauth-server"
	tools := map[string]*mcp.Tool{
		"search": {
			Name:        "search",
			Description: "Searches data",
		},
	}

	err := cache.SetTools(serverID, "OAuth Server", "https://example.com", tools, time.Now())
	require.NoError(t, err)

	invalidateSharedToolsCacheForOAuthDiscovery(cache, log, "user-id", serverID, ServerConfig{
		Name:         serverID,
		BaseURL:      "https://example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, true)

	require.NotNil(t, cache.GetTools(serverID))
}
