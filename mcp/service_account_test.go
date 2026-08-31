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

	bag := newServiceAccountClients("bot-1", newTestLogService(), liveHTTP.Client(), newTestToolsCache())
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

// The two auth modes must pool separately even for the same user ID.
func TestClientManagerModeIsolationPooling(t *testing.T) {
	pluginAPI := newTestPluginAPIForEmbeddedManager("bot-1", "session-1")
	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, pluginAPI.Log, pluginAPI, newTestOAuthManager(), nil, http.DefaultClient, nil)
	t.Cleanup(m.Close)

	_, userErrors := m.GetToolsForUser(context.Background(), "bot-1")
	require.Nil(t, userErrors)
	_, saErrors := m.GetToolsForServiceAccount(context.Background(), "bot-1", "user-1")
	require.Nil(t, saErrors)

	require.Len(t, m.clients, 2)
	userBag := m.clients[clientKey{userID: "bot-1"}]
	saBag := m.clients[clientKey{userID: "bot-1", serviceAccount: true}]
	require.NotNil(t, userBag)
	require.NotNil(t, saBag)
	require.NotSame(t, userBag, saBag)

	require.False(t, userBag.serviceAccount)
	require.NotNil(t, userBag.oauthManager)
	require.True(t, saBag.serviceAccount)
	require.Nil(t, saBag.oauthManager, "service account bags must have no OAuth machinery")
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

	tools, mcpErrors := m.GetToolsForServiceAccount(context.Background(), botUserID, invokingUserID)
	require.Nil(t, mcpErrors)
	requireToolNames(t, tools, "mattermost__search_users")
	require.Equal(t, []string{invokingUserID}, embeddedServer.recordedUserIDs())

	local := m.localClients[invokingUserID]
	require.NotNil(t, local)
	embedded := localClientByID(local, EmbeddedClientKey)
	require.NotNil(t, embedded)
	require.Equal(t, invokingUserID, embedded.userID)
	require.Equal(t, "user-a-session", embedded.sessionID)
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

	tools, mcpErrors := m.GetToolsForServiceAccount(context.Background(), "bot-1", "user-a")
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

	_, mcpErrors := m.GetToolsForServiceAccount(context.Background(), "bot-1", "user-a")
	require.Nil(t, mcpErrors)

	saBag := m.clients[clientKey{userID: "bot-1", serviceAccount: true}]
	require.NotNil(t, saBag)
	require.ElementsMatch(t, []string{"sa-server"}, clientBagServerIDs(saBag))

	_, userRemoteExists := m.clients[clientKey{userID: "user-a"}]
	require.False(t, userRemoteExists, "SA catalog must not create a per-user remotes bag")
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

	_, errA := m.GetToolsForServiceAccount(context.Background(), "bot-1", "user-a")
	require.Nil(t, errA)
	_, errB := m.GetToolsForServiceAccount(context.Background(), "bot-1", "user-b")
	require.Nil(t, errB)

	clientA := localClientByID(m.localClients["user-a"], EmbeddedClientKey)
	clientB := localClientByID(m.localClients["user-b"], EmbeddedClientKey)
	require.NotNil(t, clientA)
	require.NotNil(t, clientB)
	require.NotSame(t, clientA, clientB)
	require.Equal(t, "user-a", clientA.userID)
	require.Equal(t, "user-b", clientB.userID)
	require.Equal(t, "session-a", clientA.sessionID)
	require.Equal(t, "session-b", clientB.sessionID)
	require.NotEqual(t, clientA.sessionID, clientB.sessionID)
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

	tools, mcpErrors := m.GetToolsForServiceAccount(context.Background(), "bot-1", "user-a")
	require.Nil(t, mcpErrors, "excluded remotes are not failures and must not be dialed as the user")
	requireToolNames(t, tools, "sa_server__sa_tool")

	saBag := m.clients[clientKey{userID: "bot-1", serviceAccount: true}]
	require.NotNil(t, saBag)
	require.ElementsMatch(t, []string{"sa-server"}, clientBagServerIDs(saBag))

	_, userRemoteExists := m.clients[clientKey{userID: "user-a"}]
	require.False(t, userRemoteExists)
}

func TestServerAvailableForServiceAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		server ServerConfig
		want   bool
	}{
		{
			name:   "embedded server is not a remote SA target",
			server: ServerConfig{BaseURL: EmbeddedClientKey},
			want:   false,
		},
		{
			name:   "plugin server is not a remote SA target",
			server: ServerConfig{BaseURL: "plugin://com.example.mcp"},
			want:   false,
		},
		{
			name:   "remote server without SA headers is excluded",
			server: ServerConfig{BaseURL: "https://mcp.example.com", Name: "n8n"},
			want:   false,
		},
		{
			name: "remote server with SA headers is available",
			server: ServerConfig{
				BaseURL:               "https://mcp.example.com",
				ServiceAccountHeaders: testServiceAccountHeaders(),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ServerAvailableForServiceAccount(tt.server))
		})
	}
}

func TestIsLocalServerOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		origin string
		want   bool
	}{
		{origin: EmbeddedClientKey, want: true},
		{origin: "plugin://com.example.mcp", want: true},
		{origin: "https://mcp.example.com", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			require.Equal(t, tt.want, IsLocalServerOrigin(tt.origin))
		})
	}
}

type recordingEmbeddedMCPServer struct {
	fakeEmbeddedMCPServer
	mu      sync.Mutex
	userIDs []string
}

func (f *recordingEmbeddedMCPServer) CreateClientTransport(userID, sessionID string, pluginAPI *pluginapi.Client) (*gomcp.InMemoryTransport, error) {
	f.mu.Lock()
	f.userIDs = append(f.userIDs, userID)
	f.mu.Unlock()
	return f.fakeEmbeddedMCPServer.CreateClientTransport(userID, sessionID, pluginAPI)
}

func (f *recordingEmbeddedMCPServer) recordedUserIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.userIDs...)
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

func clientBagServerIDs(bag *UserClients) []string {
	if bag == nil {
		return nil
	}
	ids := make([]string, 0)
	for _, entry := range bag.snapshotClients() {
		ids = append(ids, entry.serverID)
	}
	return ids
}

func localClientByID(bag *UserClients, serverID string) *Client {
	if bag == nil {
		return nil
	}
	for _, entry := range bag.snapshotClients() {
		if entry.serverID == serverID {
			return entry.client
		}
	}
	return nil
}

func cachedToolNames(tools map[string]*gomcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	return names
}
