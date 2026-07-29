// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
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

	// The string form Client.oauthNeededError pattern-matches on (the MCP SDK drops error chains).
	testOAuthNeededErrorPrefix = "OAuth authentication needed for resource at "
	testOAuthShapedFailure     = testOAuthNeededErrorPrefix + "https://upstream.example.com/.well-known/oauth-protected-resource"
	testOAuthCallbackURL       = "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback"
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

func newServiceAccountTestClients(t *testing.T, botUserID string, httpClient *http.Client) *UserClients {
	t.Helper()
	return NewServiceAccountClients(botUserID, newTestLogService(), httpClient, newTestToolsCache())
}

// kvWriteRecorder records KV keys written by an OAuthManager while delegating everything else.
type kvWriteRecorder struct {
	mmapi.Client
	mu   sync.Mutex
	keys []string
}

func (r *kvWriteRecorder) KVSetWithExpiry(key string, value any, ttl time.Duration) error {
	r.mu.Lock()
	r.keys = append(r.keys, key)
	r.mu.Unlock()
	return r.Client.KVSetWithExpiry(key, value, ttl)
}

func (r *kvWriteRecorder) writtenKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.keys...)
}

func newRecordingOAuthManager() (*OAuthManager, *kvWriteRecorder) {
	recorder := &kvWriteRecorder{Client: mmapi.NewClient(newTestPluginAPIWithSession(""))}
	return NewOAuthManager(recorder, testOAuthCallbackURL, http.DefaultClient, nil), recorder
}

// startOAuthShapedDiscoveryFailureMCPServer fails tool discovery with testOAuthNeededErrorPrefix text.
func startOAuthShapedDiscoveryFailureMCPServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := newTestMCPServer(0, "sa_tool")
	server.AddReceivingMiddleware(func(next gomcp.MethodHandler) gomcp.MethodHandler {
		return func(ctx context.Context, method string, req gomcp.Request) (gomcp.Result, error) {
			if method == testListToolsMethod {
				return nil, errors.New(testOAuthShapedFailure)
			}
			return next(ctx, method, req)
		}
	})
	return startStreamableMCPServer(t, server)
}

// startOAuthShapedToolFailureMCPServer discovers fine but its only tool fails with testOAuthNeededErrorPrefix text.
func startOAuthShapedToolFailureMCPServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := gomcp.NewServer(&gomcp.Implementation{Name: "oauth-shaped-failure-server", Version: "1.0"}, nil)
	server.AddTool(&gomcp.Tool{
		Name:        "sa_failing_tool",
		Description: "Always fails with OAuth-shaped text",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		return &gomcp.CallToolResult{
			IsError: true,
			Content: []gomcp.Content{&gomcp.TextContent{Text: testOAuthShapedFailure}},
		}, nil
	})
	return startStreamableMCPServer(t, server)
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

// A blank header row must never reach the transport: net/http rejects the whole request.
func TestNewClientServiceAccountIgnoresBlankConfiguredHeaders(t *testing.T) {
	server := newTestMCPServer(0, "sa_tool")
	httpServer, recorder := startRecordingStreamableMCPServer(t, server)

	client, err := newClient(context.Background(), "bot-1", ServerConfig{
		Name:    "sa-server",
		BaseURL: httpServer.URL,
		Enabled: true,
		ServiceAccountHeaders: map[string]string{
			"":               "",
			testSAHeaderName: testSAHeaderValue,
		},
	}, clientParams{
		log:            newTestLogService(),
		httpClient:     httpServer.Client(),
		toolsCache:     newTestToolsCache(),
		serviceAccount: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	recorded := recorder.snapshot()
	require.NotEmpty(t, recorded)
	for _, headers := range recorded {
		require.Equal(t, testSAHeaderValue, headers.Get(testSAHeaderName))
	}
}

func TestNewClientServiceAccountUnauthorizedIsGenericError(t *testing.T) {
	testCases := []struct {
		name             string
		wwwAuthenticate  string
		serviceAccount   bool
		expectOAuthError bool
	}{
		{
			name:            "service account mode with WWW-Authenticate",
			wwwAuthenticate: `Bearer resource_metadata="https://sa.example.com/.well-known/oauth-protected-resource"`,
			serviceAccount:  true,
		},
		{
			name:           "service account mode without WWW-Authenticate",
			serviceAccount: true,
		},
		{
			name:             "user mode still surfaces an OAuth-needed error",
			wwwAuthenticate:  `Bearer resource_metadata="https://sa.example.com/.well-known/oauth-protected-resource"`,
			expectOAuthError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.wwwAuthenticate != "" {
					w.Header().Set("WWW-Authenticate", tc.wwwAuthenticate)
				}
				w.WriteHeader(http.StatusUnauthorized)
			}))
			t.Cleanup(httpServer.Close)

			params := clientParams{
				log:            newTestLogService(),
				httpClient:     httpServer.Client(),
				toolsCache:     newTestToolsCache(),
				serviceAccount: tc.serviceAccount,
			}
			if !tc.serviceAccount {
				params.oauthManager = newTestOAuthManager()
			}

			client, err := newClient(context.Background(), "bot-1", ServerConfig{
				Name:                  "sa-server",
				BaseURL:               httpServer.URL,
				Enabled:               true,
				ServiceAccountHeaders: testServiceAccountHeaders(),
			}, params)

			require.Error(t, err)
			require.Nil(t, client)

			var oauthErr *OAuthNeededError
			require.Equal(t, tc.expectOAuthError, errors.As(err, &oauthErr),
				"service account 401s must never ask the user to connect an account")
		})
	}
}

func TestServiceAccountConnectToRemoteServersFailClosed(t *testing.T) {
	liveHTTP := startStreamableMCPServer(t, newTestMCPServer(0, "sa_tool"))
	deadURL := deadServerURL(t)

	saConfigured := ServerConfig{
		Name:                  "sa-server",
		BaseURL:               liveHTTP.URL,
		Enabled:               true,
		ServiceAccountHeaders: testServiceAccountHeaders(),
	}
	withoutSA := ServerConfig{Name: "no-sa-server", BaseURL: deadURL, Enabled: true}
	saConfiguredDead := ServerConfig{
		Name:                  "sa-dead-server",
		BaseURL:               deadURL,
		Enabled:               true,
		ServiceAccountHeaders: testServiceAccountHeaders(),
	}

	testCases := []struct {
		name              string
		serviceAccount    bool
		servers           []ServerConfig
		expectedServerIDs []string
		expectConnectErr  bool
	}{
		{
			name:              "only the service-account-configured server is dialed",
			serviceAccount:    true,
			servers:           []ServerConfig{saConfigured, withoutSA},
			expectedServerIDs: []string{"sa-server"},
		},
		{
			name:           "no service-account-configured servers yields an empty bag",
			serviceAccount: true,
			servers:        []ServerConfig{withoutSA},
		},
		{
			name:             "a failing service-account server reports a generic error",
			serviceAccount:   true,
			servers:          []ServerConfig{saConfiguredDead},
			expectConnectErr: true,
		},
		{
			name:             "user mode still dials servers without service account headers",
			servers:          []ServerConfig{withoutSA},
			expectConnectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var bag *UserClients
			if tc.serviceAccount {
				bag = newServiceAccountTestClients(t, "bot-1", liveHTTP.Client())
			} else {
				bag = NewUserClients("bot-1", newTestLogService(), nil, liveHTTP.Client(), newTestToolsCache())
			}
			t.Cleanup(bag.Close)

			mcpErrors := bag.ConnectToRemoteServers(context.Background(), tc.servers, false)

			connectedIDs := make([]string, 0, len(tc.expectedServerIDs))
			for _, entry := range bag.snapshotClients() {
				connectedIDs = append(connectedIDs, entry.serverID)
			}
			require.ElementsMatch(t, tc.expectedServerIDs, connectedIDs)

			if !tc.expectConnectErr {
				require.Nil(t, mcpErrors, "servers excluded by fail-closed filtering are not failures")
				return
			}
			require.NotNil(t, mcpErrors)
			require.NotEmpty(t, mcpErrors.Errors)
			require.Empty(t, mcpErrors.ToolAuthErrors, "service account mode never produces auth prompts")
		})
	}
}

// SA connect failures must stay generic even when the error text matches Client.oauthNeededError.
func TestServiceAccountConnectNeverSurfacesOAuthPrompt(t *testing.T) {
	testCases := []struct {
		name                string
		serviceAccount      bool
		expectToolAuthError bool
	}{
		{
			name:           "service account mode records a generic connect error",
			serviceAccount: true,
		},
		{
			name:                "user mode still classifies the prefix as OAuth-needed",
			expectToolAuthError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			httpServer := startOAuthShapedDiscoveryFailureMCPServer(t)

			var bag *UserClients
			if tc.serviceAccount {
				bag = newServiceAccountTestClients(t, "bot-1", httpServer.Client())
			} else {
				bag = NewUserClients("bot-1", newTestLogService(), newTestOAuthManager(), httpServer.Client(), newTestToolsCache())
			}
			t.Cleanup(bag.Close)

			mcpErrors := bag.ConnectToRemoteServers(context.Background(), []ServerConfig{{
				Name:                  "sa-server",
				BaseURL:               httpServer.URL,
				Enabled:               true,
				ServiceAccountHeaders: testServiceAccountHeaders(),
			}}, false)
			require.NotNil(t, mcpErrors)

			if tc.expectToolAuthError {
				require.Empty(t, mcpErrors.Errors)
				require.Len(t, mcpErrors.ToolAuthErrors, 1)
				require.NotEmpty(t, mcpErrors.ToolAuthErrors[0].AuthURL)
				return
			}

			require.Empty(t, mcpErrors.ToolAuthErrors, "service account mode must never ask the user to connect an account")
			require.Len(t, mcpErrors.Errors, 1)
			require.Contains(t, mcpErrors.Errors[0].Error(), testOAuthNeededErrorPrefix,
				"the upstream text still reaches the caller, just not as an auth prompt")
		})
	}
}

func TestServiceAccountToolCallNeverWritesOAuthNeededState(t *testing.T) {
	testCases := []struct {
		name           string
		newBag         func(t *testing.T, httpServer *httptest.Server, oauthManager *OAuthManager) *UserClients
		expectedKVKeys []string
	}{
		{
			name: "service account bag writes nothing",
			newBag: func(t *testing.T, httpServer *httptest.Server, _ *OAuthManager) *UserClients {
				return newServiceAccountTestClients(t, "bot-1", httpServer.Client())
			},
		},
		{
			// SA bags never have an OAuth manager in production; this pins the guard, not the nil.
			name: "service account bag with an OAuth manager still writes nothing",
			newBag: func(t *testing.T, httpServer *httptest.Server, oauthManager *OAuthManager) *UserClients {
				bag := newServiceAccountTestClients(t, "bot-1", httpServer.Client())
				bag.oauthManager = oauthManager
				return bag
			},
		},
		{
			name: "user mode records the oauth-needed state",
			newBag: func(_ *testing.T, httpServer *httptest.Server, oauthManager *OAuthManager) *UserClients {
				return NewUserClients("bot-1", newTestLogService(), oauthManager, httpServer.Client(), newTestToolsCache())
			},
			expectedKVKeys: []string{"mcp_oauth_needed_v1_bot-1_sa-server"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			httpServer := startOAuthShapedToolFailureMCPServer(t)
			oauthManager, kvRecorder := newRecordingOAuthManager()

			bag := tc.newBag(t, httpServer, oauthManager)
			t.Cleanup(bag.Close)

			require.Nil(t, bag.ConnectToRemoteServers(context.Background(), []ServerConfig{{
				Name:                  "sa-server",
				BaseURL:               httpServer.URL,
				Enabled:               true,
				ServiceAccountHeaders: testServiceAccountHeaders(),
			}}, false))

			tools := bag.GetTools(context.Background())
			require.Len(t, tools, 1)

			_, err := tools[0].Resolver(context.Background(), &llm.Context{}, func(args any) error {
				*(args.(*map[string]any)) = map[string]any{}
				return nil
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), testOAuthShapedFailure)

			require.Equal(t, tc.expectedKVKeys, kvRecorder.writtenKeys())
		})
	}
}

func TestServiceAccountToolsCacheIsolation(t *testing.T) {
	const serverName = "sa-server"
	const sentinelTool = "sentinel_tool"
	saCacheID := serviceAccountToolsCacheID(serverName)

	testCases := []struct {
		name                   string
		serviceAccount         bool
		seedCacheID            string
		expectedClientTools    []string
		expectedUserCacheTools []string
		expectedSACacheTools   []string
	}{
		{
			name:                 "service account connect populates only the namespaced entry",
			serviceAccount:       true,
			expectedClientTools:  []string{"live_tool"},
			expectedSACacheTools: []string{"live_tool"},
		},
		{
			name:                   "service account connect ignores the user-mode entry",
			serviceAccount:         true,
			seedCacheID:            serverName,
			expectedClientTools:    []string{"live_tool"},
			expectedUserCacheTools: []string{sentinelTool},
			expectedSACacheTools:   []string{"live_tool"},
		},
		{
			name:                   "user connect ignores the namespaced entry",
			seedCacheID:            saCacheID,
			expectedClientTools:    []string{"live_tool"},
			expectedUserCacheTools: []string{"live_tool"},
			expectedSACacheTools:   []string{sentinelTool},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			httpServer := startStreamableMCPServer(t, newTestMCPServer(0, "live_tool"))
			cache := newTestToolsCache()
			if tc.seedCacheID != "" {
				require.NoError(t, cache.SetTools(tc.seedCacheID, serverName, httpServer.URL, map[string]*gomcp.Tool{
					sentinelTool: {Name: sentinelTool, Description: "Stale cached tool"},
				}, time.Now()))
			}

			client, err := newClient(context.Background(), "bot-1", ServerConfig{
				Name:                  serverName,
				BaseURL:               httpServer.URL,
				Enabled:               true,
				ServiceAccountHeaders: testServiceAccountHeaders(),
			}, clientParams{
				log:            newTestLogService(),
				httpClient:     httpServer.Client(),
				toolsCache:     cache,
				serviceAccount: tc.serviceAccount,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			require.ElementsMatch(t, tc.expectedClientTools, cachedToolNames(client.Tools()))
			require.ElementsMatch(t, tc.expectedUserCacheTools, cachedToolNames(cache.GetTools(serverName)))
			require.ElementsMatch(t, tc.expectedSACacheTools, cachedToolNames(cache.GetTools(saCacheID)))
		})
	}
}

func TestServiceAccountUsesSharedCacheDespiteStaticOAuthCreds(t *testing.T) {
	var listCalls atomic.Int32
	server := newStaticToolListMCPServer(0, "sa_tool")
	server.AddReceivingMiddleware(func(next gomcp.MethodHandler) gomcp.MethodHandler {
		return func(ctx context.Context, method string, req gomcp.Request) (gomcp.Result, error) {
			if method == testListToolsMethod {
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
	_, saErrors := m.GetToolsForServiceAccount(context.Background(), "bot-1")
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

func TestClientManagerServiceAccountFailClosedCatalog(t *testing.T) {
	alphaHTTP := startStreamableMCPServer(t, newTestMCPServer(0, "alpha_tool"))
	betaHTTP := startStreamableMCPServer(t, newTestMCPServer(0, "beta_tool"))

	pluginAPI := newTestPluginAPIForEmbeddedManager("bot-1", "session-1")
	m := NewClientManager(Config{
		IdleTimeoutMinutes: 30,
		Servers: []ServerConfig{
			{
				Name:                  "alpha",
				BaseURL:               alphaHTTP.URL,
				Enabled:               true,
				ServiceAccountHeaders: testServiceAccountHeaders(),
			},
			{Name: "beta", BaseURL: betaHTTP.URL, Enabled: true},
		},
	}, pluginAPI.Log, pluginAPI, nil, nil, alphaHTTP.Client(), nil)
	t.Cleanup(m.Close)

	saTools, saErrors := m.GetToolsForServiceAccount(context.Background(), "bot-1")
	require.Nil(t, saErrors)
	requireToolNames(t, saTools, "alpha__alpha_tool")

	userTools, userErrors := m.GetToolsForUser(context.Background(), "bot-1")
	require.Nil(t, userErrors)
	requireToolNames(t, userTools, "alpha__alpha_tool", "beta__beta_tool")
}

func TestClientManagerCloseIdleClients(t *testing.T) {
	now := time.Now()
	staleOAuth := clientKey{userID: "user-1"}
	staleSA := clientKey{userID: "user-1", serviceAccount: true}
	freshOAuth := clientKey{userID: "user-2"}
	freshSA := clientKey{userID: "user-2", serviceAccount: true}

	testCases := []struct {
		name         string
		expectedKeys []clientKey
		sweepAt      time.Time
	}{
		{
			name:         "closes stale bags in both auth modes",
			sweepAt:      now,
			expectedKeys: []clientKey{freshOAuth, freshSA},
		},
		{
			name:         "keeps every bag when nothing is idle yet",
			sweepAt:      now.Add(-time.Hour),
			expectedKeys: []clientKey{staleOAuth, staleSA, freshOAuth, freshSA},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := &ClientManager{
				log:           newTestLogService(),
				clientTimeout: 30 * time.Minute,
				clients: map[clientKey]*UserClients{
					staleOAuth: {clients: map[string]*Client{}},
					staleSA:    {clients: map[string]*Client{}},
					freshOAuth: {clients: map[string]*Client{}},
					freshSA:    {clients: map[string]*Client{}},
				},
				activity: map[clientKey]time.Time{
					staleOAuth: now.Add(-time.Hour),
					staleSA:    now.Add(-time.Hour),
					freshOAuth: now.Add(-time.Minute),
					freshSA:    now.Add(-time.Minute),
				},
			}

			manager.closeIdleClients(tc.sweepAt)

			require.Len(t, manager.clients, len(tc.expectedKeys))
			for _, key := range tc.expectedKeys {
				require.Contains(t, manager.clients, key)
			}
		})
	}
}

func TestClientManagerServiceAccountEmbeddedSessionAsBot(t *testing.T) {
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)

	pluginAPI := newTestPluginAPIForEmbeddedUser(&model.User{Id: "bot-1", Roles: "system_user", IsBot: true}, "session-1")
	embeddedServer := &fakeEmbeddedMCPServer{ctx: runCtx, server: newTestMCPServer(0, "search_users")}
	m := NewClientManager(Config{
		IdleTimeoutMinutes: 30,
		EmbeddedServer: EmbeddedServerConfig{
			Enabled:     true,
			ToolConfigs: []ToolConfig{{Name: "search_users", Policy: ToolPolicyAsk, Enabled: true}},
		},
	}, pluginAPI.Log, pluginAPI, nil, embeddedServer, http.DefaultClient, nil)
	t.Cleanup(m.Close)

	tools, mcpErrors := m.GetToolsForServiceAccount(context.Background(), "bot-1")
	require.Nil(t, mcpErrors)
	requireToolNames(t, tools, "mattermost__search_users")
}

func TestClientManagerServiceAccountPluginServerGetsBotUserIDHeader(t *testing.T) {
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
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, mockAPI)
	t.Cleanup(m.Close)
	m.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.mcp", Name: "Example", Path: "/mcp", Enabled: true})

	tools, mcpErrors := m.GetToolsForServiceAccount(context.Background(), "bot-1")
	require.Nil(t, mcpErrors)
	require.Len(t, tools, 1)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, recordedUserIDs)
	for _, userID := range recordedUserIDs {
		require.Equal(t, "bot-1", userID, "plugin MCP servers must see the acting bot user ID")
	}
}

func cachedToolNames(tools map[string]*gomcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	return names
}
