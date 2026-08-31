// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const (
	testSAHeaderName  = "X-Service-Account-Token"
	testSAHeaderValue = "service-account-pat"
	testAdminHeader   = "X-Admin"
)

func testServiceAccountHeaders() map[string]string {
	return map[string]string{testSAHeaderName: testSAHeaderValue}
}

// requestHeaderRecorder captures the headers of every request reaching a test MCP server.
type requestHeaderRecorder struct {
	mu      sync.Mutex
	headers []http.Header
}

func (r *requestHeaderRecorder) record(h http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.headers = append(r.headers, h)
}

func (r *requestHeaderRecorder) snapshot() []http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]http.Header(nil), r.headers...)
}

func startRecordingStreamableMCPServer(t *testing.T, server *gomcp.Server) (*httptest.Server, *requestHeaderRecorder) {
	t.Helper()

	recorder := &requestHeaderRecorder{}
	handler := gomcp.NewStreamableHTTPHandler(func(*http.Request) *gomcp.Server {
		return server
	}, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r.Header.Clone())
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	return httpServer, recorder
}

// deadServerURL returns a local URL guaranteed to refuse connections.
func deadServerURL(t *testing.T) string {
	t.Helper()

	httpServer := httptest.NewServer(http.NotFoundHandler())
	url := httpServer.URL
	httpServer.Close()
	return url
}

func TestNewClientServiceAccountSendsStaticHeaders(t *testing.T) {
	server := newTestMCPServer(0, "sa_tool")
	httpServer, recorder := startRecordingStreamableMCPServer(t, server)

	client, err := newClient(context.Background(), "bot-1", ServerConfig{
		Name:                  "sa-server",
		BaseURL:               httpServer.URL,
		Enabled:               true,
		Headers:               map[string]string{testAdminHeader: "admin-value"},
		ServiceAccountHeaders: testServiceAccountHeaders(),
	}, clientParams{
		log:            newTestLogService(),
		httpClient:     httpServer.Client(),
		toolsCache:     newTestToolsCache(),
		serviceAccount: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	recorded := recorder.snapshot()
	require.NotEmpty(t, recorded, "the service account connect must have reached the server")
	for _, headers := range recorded {
		require.Equal(t, testSAHeaderValue, headers.Get(testSAHeaderName))
		require.Equal(t, "admin-value", headers.Get(testAdminHeader))
		require.Equal(t, "bot-1", headers.Get(MMUserIDHeader), "X-Mattermost-UserID must be the acting bot user ID")
		require.Empty(t, headers.Get("Authorization"), "service account mode must not attach an OAuth token")
	}
}

// Servers without service account headers are excluded, never dialed with anyone else's credentials.
func TestServiceAccountConnectToRemoteServersFailClosed(t *testing.T) {
	liveHTTP := startStreamableMCPServer(t, newTestMCPServer(0, "sa_tool"))

	bag := newRemoteClients("bot-1", true, newTestLogService(), nil, liveHTTP.Client(), newTestToolsCache())
	t.Cleanup(bag.Close)

	mcpErrors := bag.ConnectToRemoteServers(context.Background(), []ServerConfig{
		{
			Name:                  "sa-server",
			BaseURL:               liveHTTP.URL,
			Enabled:               true,
			ServiceAccountHeaders: testServiceAccountHeaders(),
		},
		// A refused URL: any dial attempt would surface as a connect error below.
		{Name: "no-sa-server", BaseURL: deadServerURL(t), Enabled: true},
	}, false)

	connectedIDs := make([]string, 0, 1)
	for _, entry := range bag.snapshotClients() {
		connectedIDs = append(connectedIDs, entry.serverID)
	}
	require.ElementsMatch(t, []string{"sa-server"}, connectedIDs)
	require.Nil(t, mcpErrors, "servers excluded by fail-closed filtering are not failures")
}

// The two auth modes keep separate tools cache entries for the same server.
func TestServiceAccountToolsCacheIsolation(t *testing.T) {
	const serverName = "sa-server"
	const sentinelTool = "sentinel_tool"
	saCacheID := serviceAccountToolsCacheID(serverName)

	httpServer := startStreamableMCPServer(t, newTestMCPServer(0, "live_tool"))
	cache := newTestToolsCache()
	require.NoError(t, cache.SetTools(serverName, serverName, httpServer.URL, map[string]*gomcp.Tool{
		sentinelTool: {Name: sentinelTool, Description: "Stale cached tool"},
	}, time.Now()))

	client, err := newClient(context.Background(), "bot-1", ServerConfig{
		Name:                  serverName,
		BaseURL:               httpServer.URL,
		Enabled:               true,
		ServiceAccountHeaders: testServiceAccountHeaders(),
	}, clientParams{
		log:            newTestLogService(),
		httpClient:     httpServer.Client(),
		toolsCache:     cache,
		serviceAccount: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.ElementsMatch(t, []string{"live_tool"}, cachedToolNames(client.Tools()))
	require.ElementsMatch(t, []string{sentinelTool}, cachedToolNames(cache.GetTools(serverName)))
	require.ElementsMatch(t, []string{"live_tool"}, cachedToolNames(cache.GetTools(saCacheID)))
}

func TestServiceAccountUsesSharedCacheDespiteStaticOAuthCreds(t *testing.T) {
	var listCalls atomic.Int32
	server := newStaticToolListMCPServer(0, "sa_tool")
	server.AddReceivingMiddleware(func(next gomcp.MethodHandler) gomcp.MethodHandler {
		return func(ctx context.Context, method string, req gomcp.Request) (gomcp.Result, error) {
			if method == listToolsMethod {
				listCalls.Add(1)
			}
			return next(ctx, method, req)
		}
	})
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()

	// Static OAuth creds disable the shared cache in user mode, but SA creds are identical per connection.
	serverConfig := ServerConfig{
		Name:                  "sa-server",
		BaseURL:               httpServer.URL,
		Enabled:               true,
		ClientID:              "client-id",
		ClientSecret:          "client-secret",
		ServiceAccountHeaders: testServiceAccountHeaders(),
	}
	params := clientParams{
		log:            newTestLogService(),
		httpClient:     httpServer.Client(),
		toolsCache:     cache,
		serviceAccount: true,
	}

	first, err := newClient(context.Background(), "bot-1", serverConfig, params)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	require.Equal(t, int32(1), listCalls.Load())
	require.ElementsMatch(t, []string{"sa_tool"}, cachedToolNames(cache.GetTools(serviceAccountToolsCacheID(serverConfig.Name))))

	second, err := newClient(context.Background(), "bot-1", serverConfig, params)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	require.Equal(t, int32(1), listCalls.Load(), "the second service account connect must be served from the cache")
	require.ElementsMatch(t, []string{"sa_tool"}, cachedToolNames(second.Tools()))
}

// The two auth modes must connect separately even for the same remote-owner ID.
func TestClientManagerModeIsolationPooling(t *testing.T) {
	server := newTestMCPServer(0, "shared_tool")
	httpServer, recorder := startRecordingStreamableMCPServer(t, server)
	pluginAPI := newTestPluginAPIForEmbeddedManager("bot-1", "session-1")
	m := NewClientManager(Config{
		IdleTimeoutMinutes: 30,
		Servers: []ServerConfig{{
			Name:                  "shared-server",
			BaseURL:               httpServer.URL,
			Enabled:               true,
			ServiceAccountHeaders: testServiceAccountHeaders(),
		}},
	}, pluginAPI.Log, pluginAPI, newTestOAuthManager(), nil, httpServer.Client(), nil)
	t.Cleanup(m.Close)

	userTools, userErrors := m.GetTools(context.Background(), mustUserCatalogRequest(t, "bot-1"))
	require.Nil(t, userErrors)
	requireToolNames(t, userTools, "shared_server__shared_tool")

	saTools, saErrors := m.GetTools(context.Background(), mustSACatalogRequest(t, "bot-1", "user-1"))
	require.Nil(t, saErrors)
	requireToolNames(t, saTools, "shared_server__shared_tool")

	var sawUser, sawSA bool
	for _, headers := range recorder.snapshot() {
		if headers.Get(testSAHeaderName) == testSAHeaderValue {
			sawSA = true
			require.Equal(t, "bot-1", headers.Get(MMUserIDHeader))
			require.Empty(t, headers.Get("Authorization"))
			continue
		}
		if headers.Get(MMUserIDHeader) == "bot-1" {
			sawUser = true
			require.Empty(t, headers.Get(testSAHeaderName))
		}
	}
	require.True(t, sawUser, "user-mode remotes must connect without service-account headers")
	require.True(t, sawSA, "service-account remotes must connect with service-account headers")
}

func TestClientManagerServiceAccountEmbeddedSessionAsInvoker(t *testing.T) {
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)

	const botUserID = "bot-1"
	const invokingUserID = "user-a"
	pluginAPI := newTestPluginAPIForEmbeddedUsers(map[string]string{
		botUserID:      "bot-session",
		invokingUserID: "user-a-session",
	})
	embeddedServer := &recordingEmbeddedMCPServer{
		fakeEmbeddedMCPServer: fakeEmbeddedMCPServer{ctx: runCtx, server: newTestMCPServer(0, "search_users")},
	}
	m := NewClientManager(Config{
		IdleTimeoutMinutes: 30,
		EmbeddedServer: EmbeddedServerConfig{
			Enabled:     true,
			ToolConfigs: []ToolConfig{{Name: "search_users", Policy: ToolPolicyAsk, Enabled: true}},
		},
	}, pluginAPI.Log, pluginAPI, nil, embeddedServer, http.DefaultClient, nil)
	t.Cleanup(m.Close)

	tools, mcpErrors := m.GetTools(context.Background(), mustSACatalogRequest(t, botUserID, invokingUserID))
	require.Nil(t, mcpErrors)
	requireToolNames(t, tools, "mattermost__search_users")
	require.Equal(t, []string{invokingUserID}, embeddedServer.recordedUserIDs())
	require.Equal(t, []string{"user-a-session"}, embeddedServer.recordedSessionIDs())
}

func TestClientManagerServiceAccountPluginServerGetsInvokerUserIDHeader(t *testing.T) {
	target := newFakePluginMCPServer(t, 1)
	t.Cleanup(target.Close)

	var mu sync.Mutex
	var recordedUserIDs []string
	mockAPI := &fakePluginHTTPClient{
		pluginHTTP: func(req *http.Request) *http.Response {
			mu.Lock()
			recordedUserIDs = append(recordedUserIDs, req.Header.Get(MMUserIDHeader))
			mu.Unlock()

			rec := httptest.NewRecorder()
			target.Config.Handler.ServeHTTP(rec, req)
			return rec.Result()
		},
	}

	pluginTestAPI := &plugintest.API{}
	setupClientManagerTestAPI(t, pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, mockAPI)
	t.Cleanup(m.Close)
	m.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.mcp", Name: "Example", Path: "/mcp", Enabled: true})

	tools, mcpErrors := m.GetTools(context.Background(), mustSACatalogRequest(t, "bot-1", "user-a"))
	require.Nil(t, mcpErrors)
	require.Len(t, tools, 1)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, recordedUserIDs)
	for _, userID := range recordedUserIDs {
		require.Equal(t, "user-a", userID, "plugin MCP servers must see the invoking user ID")
	}
}

func TestClientManagerServiceAccountRemoteBagExcludesLocalServers(t *testing.T) {
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)

	saHTTP := startStreamableMCPServer(t, newTestMCPServer(0, "sa_tool"))
	pluginAPI := newTestPluginAPIForEmbeddedUsers(map[string]string{"user-a": "user-a-session"})
	embeddedServer := &fakeEmbeddedMCPServer{ctx: runCtx, server: newTestMCPServer(0, "search_users")}

	target := newFakePluginMCPServer(t, 1)
	t.Cleanup(target.Close)
	mockAPI := &fakePluginHTTPClient{
		pluginHTTP: func(req *http.Request) *http.Response {
			rec := httptest.NewRecorder()
			target.Config.Handler.ServeHTTP(rec, req)
			return rec.Result()
		},
	}

	m := NewClientManager(Config{
		IdleTimeoutMinutes: 30,
		Servers: []ServerConfig{{
			Name:                  "sa-server",
			BaseURL:               saHTTP.URL,
			Enabled:               true,
			ServiceAccountHeaders: testServiceAccountHeaders(),
		}},
		EmbeddedServer: EmbeddedServerConfig{
			Enabled:     true,
			ToolConfigs: []ToolConfig{{Name: "search_users", Policy: ToolPolicyAsk, Enabled: true}},
		},
	}, pluginAPI.Log, pluginAPI, nil, embeddedServer, saHTTP.Client(), mockAPI)
	t.Cleanup(m.Close)
	m.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.mcp", Name: "Example", Path: "/mcp", Enabled: true})

	tools, mcpErrors := m.GetTools(context.Background(), mustSACatalogRequest(t, "bot-1", "user-a"))
	require.Nil(t, mcpErrors)
	requireToolNames(t, tools, "sa_server__sa_tool", "mattermost__search_users", "example__test_tool_0")
}

func TestClientManagerServiceAccountInvokersDoNotShareEmbeddedSession(t *testing.T) {
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)

	pluginAPI := newTestPluginAPIForEmbeddedUsers(map[string]string{
		"user-a": "session-a",
		"user-b": "session-b",
	})
	embeddedServer := &recordingEmbeddedMCPServer{
		fakeEmbeddedMCPServer: fakeEmbeddedMCPServer{ctx: runCtx, server: newTestMCPServer(0, "search_users")},
	}
	m := NewClientManager(Config{
		IdleTimeoutMinutes: 30,
		EmbeddedServer: EmbeddedServerConfig{
			Enabled:     true,
			ToolConfigs: []ToolConfig{{Name: "search_users", Policy: ToolPolicyAsk, Enabled: true}},
		},
	}, pluginAPI.Log, pluginAPI, nil, embeddedServer, http.DefaultClient, nil)
	t.Cleanup(m.Close)

	_, errA := m.GetTools(context.Background(), mustSACatalogRequest(t, "bot-1", "user-a"))
	require.Nil(t, errA)
	_, errB := m.GetTools(context.Background(), mustSACatalogRequest(t, "bot-1", "user-b"))
	require.Nil(t, errB)

	require.Equal(t, []string{"user-a", "user-b"}, embeddedServer.recordedUserIDs())
	require.Equal(t, []string{"session-a", "session-b"}, embeddedServer.recordedSessionIDs())
}

func TestClientManagerServiceAccountCatalogExcludesNonSARemotes(t *testing.T) {
	saHTTP := startStreamableMCPServer(t, newTestMCPServer(0, "sa_tool"))
	pluginAPI := newTestPluginAPIForEmbeddedManager("user-a", "session-a")
	m := NewClientManager(Config{
		IdleTimeoutMinutes: 30,
		Servers: []ServerConfig{
			{
				Name:                  "sa-server",
				BaseURL:               saHTTP.URL,
				Enabled:               true,
				ServiceAccountHeaders: testServiceAccountHeaders(),
			},
			{Name: "no-sa-server", BaseURL: deadServerURL(t), Enabled: true},
		},
	}, pluginAPI.Log, pluginAPI, newTestOAuthManager(), nil, saHTTP.Client(), nil)
	t.Cleanup(m.Close)

	tools, mcpErrors := m.GetTools(context.Background(), mustSACatalogRequest(t, "bot-1", "user-a"))
	require.Nil(t, mcpErrors, "excluded remotes are not failures and must not be dialed as the user")
	requireToolNames(t, tools, "sa_server__sa_tool")
}

// A remote named "Mattermost" slugs to the same prefix as the embedded server.
// Namespacing must run once across both bags so both tools survive.
func TestCollectCatalogCrossBagToolNameCollision(t *testing.T) {
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)

	remoteHTTP := startStreamableMCPServer(t, newTestMCPServer(0, "search_users"))
	pluginAPI := newTestPluginAPIForEmbeddedUsers(map[string]string{"user-a": "user-a-session"})
	embeddedServer := &fakeEmbeddedMCPServer{ctx: runCtx, server: newTestMCPServer(0, "search_users")}

	target := newFakePluginMCPServer(t, 1)
	t.Cleanup(target.Close)
	mockAPI := &fakePluginHTTPClient{
		pluginHTTP: func(req *http.Request) *http.Response {
			rec := httptest.NewRecorder()
			target.Config.Handler.ServeHTTP(rec, req)
			return rec.Result()
		},
	}

	m := NewClientManager(Config{
		IdleTimeoutMinutes: 30,
		Servers: []ServerConfig{{
			Name:                  "Mattermost",
			BaseURL:               remoteHTTP.URL,
			Enabled:               true,
			ServiceAccountHeaders: testServiceAccountHeaders(),
		}},
		EmbeddedServer: EmbeddedServerConfig{
			Enabled:     true,
			ToolConfigs: []ToolConfig{{Name: "search_users", Policy: ToolPolicyAsk, Enabled: true}},
		},
	}, pluginAPI.Log, pluginAPI, newTestOAuthManager(), embeddedServer, remoteHTTP.Client(), mockAPI)
	t.Cleanup(m.Close)
	m.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.mcp", Name: "Mattermost", Path: "/mcp", Enabled: true})

	tests := []struct {
		name string
		req  CatalogRequest
	}{
		{name: "user catalog", req: mustUserCatalogRequest(t, "user-a")},
		{name: "service account catalog", req: mustSACatalogRequest(t, "bot-1", "user-a")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, mcpErrors := m.GetTools(context.Background(), tt.req)
			require.Nil(t, mcpErrors)
			require.GreaterOrEqual(t, len(tools), 3, "remote, embedded, and plugin tools must all be present")

			seen := make(map[string]struct{}, len(tools))
			for _, tool := range tools {
				_, exists := seen[tool.Name]
				require.False(t, exists, "duplicate runtime tool name %q after cross-bag namespacing", tool.Name)
				seen[tool.Name] = struct{}{}
			}
		})
	}
}

type recordingEmbeddedMCPServer struct {
	fakeEmbeddedMCPServer
	mu         sync.Mutex
	userIDs    []string
	sessionIDs []string
}

func (f *recordingEmbeddedMCPServer) CreateClientTransport(userID, sessionID string, pluginAPI *pluginapi.Client) (*gomcp.InMemoryTransport, error) {
	f.mu.Lock()
	f.userIDs = append(f.userIDs, userID)
	f.sessionIDs = append(f.sessionIDs, sessionID)
	f.mu.Unlock()
	return f.fakeEmbeddedMCPServer.CreateClientTransport(userID, sessionID, pluginAPI)
}

func (f *recordingEmbeddedMCPServer) recordedUserIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.userIDs...)
}

func (f *recordingEmbeddedMCPServer) recordedSessionIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sessionIDs...)
}

func mustUserCatalogRequest(t *testing.T, userID string) CatalogRequest {
	t.Helper()
	req, err := NewUserCatalogRequest(userID)
	require.NoError(t, err)
	return req
}

func mustSACatalogRequest(t *testing.T, remoteOwnerID, invokingUserID string) CatalogRequest {
	t.Helper()
	req, err := NewServiceAccountCatalogRequest(remoteOwnerID, invokingUserID)
	require.NoError(t, err)
	return req
}

// newTestPluginAPIForEmbeddedUsers maps each user ID to a pre-minted session ID.
func newTestPluginAPIForEmbeddedUsers(users map[string]string) *pluginapi.Client {
	sessionByID := make(map[string]*model.Session, len(users))
	userByID := make(map[string]*model.User, len(users))
	kv := make(map[string][]byte, len(users))
	for userID, sessionID := range users {
		userByID[userID] = &model.User{Id: userID, Roles: "system_user"}
		sessionByID[sessionID] = &model.Session{
			Id:        sessionID,
			UserId:    userID,
			Token:     "test-token",
			ExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
		}
		kv[buildEmbeddedSessionKey(userID)] = []byte(sessionID)
	}
	fakeAPI := &fixedPluginAPI{
		kvGet: func(key string) ([]byte, *model.AppError) {
			return kv[key], nil
		},
		sessionByID: sessionByID,
		userByID:    userByID,
	}
	return pluginapi.NewClient(fakeAPI, nil)
}

func cachedToolNames(tools map[string]*gomcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	return names
}
