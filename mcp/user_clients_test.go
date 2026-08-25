// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
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
	srv := gomcp.NewServer(&gomcp.Implementation{Name: "fake", Version: "1.0"}, nil)
	type echoIn struct {
		Message string `json:"message"`
	}
	type echoOut struct {
		Echo string `json:"echo"`
	}
	for i := 0; i < toolCount; i++ {
		name := fmt.Sprintf("%s_%d", prefix, i)
		gomcp.AddTool(srv, &gomcp.Tool{Name: name, Description: "test"}, func(_ context.Context, _ *gomcp.CallToolRequest, in echoIn) (*gomcp.CallToolResult, echoOut, error) {
			return nil, echoOut{Echo: in.Message}, nil
		})
	}
	h := gomcp.NewStreamableHTTPHandler(
		func(*http.Request) *gomcp.Server { return srv },
		&gomcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
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

func TestUserClientsGetToolsNamespacesDuplicateBareNames(t *testing.T) {
	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			"github": testClientWithTools("GitHub", "https://api.githubcopilot.com", "search"),
			"jira":   testClientWithTools("Jira", "https://mcp.atlassian.com", "search"),
		},
	}

	tools := userClients.GetTools(context.Background())

	requireToolNames(t, tools, "github__search", "jira__search")
}

func TestUserClientsGetToolsResolverUsesBareToolName(t *testing.T) {
	server := newTestMCPServer(0, "search")
	session := connectInMemoryTestSession(t, server)
	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			"jira": {
				session: session,
				config:  ServerConfig{Name: "Jira", BaseURL: "https://mcp.atlassian.com", Enabled: true},
				tools: map[string]*gomcp.Tool{
					"search": {
						Name:        "search",
						Description: "Search Jira",
					},
				},
			},
		},
	}

	tools := userClients.GetTools(context.Background())
	requireToolNames(t, tools, "jira__search")

	result, err := tools[0].Resolver(context.Background(), &llm.Context{}, func(args any) error {
		*(args.(*map[string]any)) = map[string]any{}
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, "search ok\n", result)
}

func TestUserClientsGetToolsEmbeddedToolNamesUseMattermostSlug(t *testing.T) {
	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			EmbeddedClientKey: testClientWithTools(EmbeddedClientKey, EmbeddedClientKey, "search_users"),
		},
	}

	tools := userClients.GetTools(context.Background())

	requireToolNames(t, tools, "mattermost__search_users")
}

func TestUserClientsGetToolsDeterministicSlugCollision(t *testing.T) {
	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			"server-a": testClientWithTools("Jira!", "https://a.example.com", "search"),
			"server-b": testClientWithTools("Jira", "https://b.example.com", "search"),
		},
	}
	expectedDedupedName := "jira_" + shortSlugHash("https://b.example.com") + "__search"

	first := userClients.GetTools(context.Background())
	second := userClients.GetTools(context.Background())

	requireToolNames(t, first, "jira__search", expectedDedupedName)
	requireToolNames(t, second, "jira__search", expectedDedupedName)
}

func TestUserClientsGetToolsUsesCachedCatalog(t *testing.T) {
	server := newTestMCPServer(0, "old_tool")
	session := connectInMemoryTestSession(t, server)
	addTestMCPTool(server, "new_tool")
	client := &Client{
		session: session,
		config:  ServerConfig{Name: "Jira", BaseURL: "https://mcp.atlassian.com", Enabled: true},
		tools: map[string]*gomcp.Tool{
			"old_tool": {
				Name:        "old_tool",
				Description: "Old tool",
			},
		},
		userID: "user-id",
		log:    newTestLogService(),
	}
	userClients := &UserClients{
		userID: "user-id",
		log:    newTestLogService(),
		clients: map[string]*Client{
			"jira": client,
		},
	}

	tools := userClients.GetTools(context.Background())

	requireToolNames(t, tools, "jira__old_tool")
}

// connectPluginServer is the single-server shorthand the plugin-connect tests
// need; production always goes through a batch of tasks.
func connectPluginServer(uc *UserClients, cfg PluginServerConfig, sourcePluginAPI mmapi.Client) *Errors {
	ctx := context.Background()
	return uc.ensureConnections(ctx, []connectTask{uc.pluginConnectTask(ctx, cfg, sourcePluginAPI)})
}

// connectEmbeddedServer mirrors the manager's embedded scheduling decision: it
// only dials when the user's embedded session differs from the cached one.
func connectEmbeddedServer(uc *UserClients, sessionID string, embeddedClient *EmbeddedServerClient) *Errors {
	if !uc.needsEmbeddedReconnect(sessionID) {
		return nil
	}
	ctx := context.Background()
	return uc.ensureConnections(ctx, []connectTask{uc.embeddedConnectTask(ctx, sessionID, embeddedClient)})
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

	require.Nil(t, connectPluginServer(uc, cfg, mockAPI))

	originKey := "plugin://" + cfg.PluginID
	require.True(t, uc.hasClient(originKey))
	snapshot := uc.snapshotClients()
	require.Len(t, snapshot, 1)
	require.Equal(t, originKey, snapshot[0].client.config.BaseURL)
	require.Len(t, snapshot[0].client.Tools(), 2)
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

	require.Nil(t, connectPluginServer(uc, cfg, mockAPI))
	firstClient := uc.snapshotClients()[0].client

	// Second call must not re-dial; tearing down the target proves it.
	target.Close()
	require.Nil(t, connectPluginServer(uc, cfg, mockAPI))

	snapshot := uc.snapshotClients()
	require.Len(t, snapshot, 1)
	require.Same(t, firstClient, snapshot[0].client)
}

// A plugin server that is briefly unreachable must be retried on the next
// request rather than remembered as failed until cache invalidation.
func TestConnectToPluginServer_FailuresAreRetryable(t *testing.T) {
	target := newFakePluginMCPServer(t, 1)
	t.Cleanup(target.Close)

	var failing atomic.Bool
	failing.Store(true)
	mockAPI := &fakePluginHTTPClient{
		pluginHTTP: func(req *http.Request) *http.Response {
			rec := httptest.NewRecorder()
			if failing.Load() {
				rec.WriteHeader(http.StatusInternalServerError)
				return rec.Result()
			}
			target.Config.Handler.ServeHTTP(rec, req)
			return rec.Result()
		},
	}

	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)
	uc := NewUserClients("alice", client.Log, nil, nil, nil)

	cfg := PluginServerConfig{PluginID: "com.example.flaky", Name: "Flaky", Path: "/mcp", Enabled: true}

	errs := connectPluginServer(uc, cfg, mockAPI)
	require.NotNil(t, errs)
	require.NotEmpty(t, errs.Errors)
	require.Empty(t, uc.snapshotClients())

	failing.Store(false)
	require.Nil(t, connectPluginServer(uc, cfg, mockAPI))
	require.Len(t, uc.snapshotClients(), 1)
}

func TestConnectToPluginServer_NilAPI(t *testing.T) {
	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)
	uc := NewUserClients("alice", client.Log, nil, nil, nil)

	errs := connectPluginServer(uc, PluginServerConfig{PluginID: "x", Path: "/mcp"}, nil)
	require.NotNil(t, errs)
	require.NotEmpty(t, errs.Errors)
}

func TestConnectToEmbeddedServer_Idempotent(t *testing.T) {
	server := newTestMCPServer(0, "tool_1")
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)

	pluginAPI := newTestPluginAPIForEmbeddedManager("alice", "session-id")
	embeddedClient := NewEmbeddedServerClient(&fakeEmbeddedMCPServer{ctx: runCtx, server: server}, pluginAPI.Log, pluginAPI)
	uc := NewUserClients("alice", pluginAPI.Log, nil, nil, nil)

	require.Nil(t, connectEmbeddedServer(uc, "session-id", embeddedClient))
	firstSnapshot := uc.snapshotClients()
	require.Len(t, firstSnapshot, 1)
	firstClient := firstSnapshot[0].client

	// Stop the embedded server so a second dial would fail if Connect re-created a client.
	cancelRun()
	require.Nil(t, connectEmbeddedServer(uc, "session-id", embeddedClient))

	secondSnapshot := uc.snapshotClients()
	require.Len(t, secondSnapshot, 1)
	require.Same(t, firstClient, secondSnapshot[0].client)
}

func TestConnectToEmbeddedServer_ReconnectsWhenSessionChanges(t *testing.T) {
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)

	fakeAPI := &fixedPluginAPI{
		sessionByID: map[string]*model.Session{
			"old-session": {Id: "old-session", UserId: "alice", Token: "old-token"},
			"new-session": {Id: "new-session", UserId: "alice", Token: "new-token"},
		},
	}
	pluginAPI := pluginapi.NewClient(fakeAPI, nil)
	embeddedClient := NewEmbeddedServerClient(&sessionEchoEmbeddedMCPServer{ctx: runCtx}, pluginAPI.Log, pluginAPI)
	uc := NewUserClients("alice", pluginAPI.Log, nil, nil, nil)

	require.Nil(t, connectEmbeddedServer(uc, "old-session", embeddedClient))
	require.Equal(t, "old-session", callSessionIdentityTool(t, uc.GetTools(context.Background())))

	require.Nil(t, connectEmbeddedServer(uc, "new-session", embeddedClient))
	require.Equal(t, "new-session", callSessionIdentityTool(t, uc.GetTools(context.Background())))
}

func TestClientManagerGetToolsForUser_ReconnectsAfterStoredSessionRevoked(t *testing.T) {
	const (
		userID       = "alice"
		oldSessionID = "old-session"
		newSessionID = "new-session"
	)

	storedSessionID := oldSessionID
	sessions := map[string]*model.Session{
		oldSessionID: {Id: oldSessionID, UserId: userID, Token: "old-token"},
	}
	fakeAPI := &plugintest.API{}
	setupTestLogger(fakeAPI)
	fakeAPI.On("KVGet", mock.AnythingOfType("string")).Return(func(key string) []byte {
		if key == buildEmbeddedSessionKey(userID) {
			return []byte(storedSessionID)
		}
		return nil
	}, (*model.AppError)(nil))
	fakeAPI.On("KVDelete", mock.AnythingOfType("string")).Run(func(args mock.Arguments) {
		if args.String(0) == buildEmbeddedSessionKey(userID) {
			storedSessionID = ""
		}
	}).Return((*model.AppError)(nil))
	fakeAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("model.PluginKVSetOptions")).Run(func(args mock.Arguments) {
		if args.String(0) == buildEmbeddedSessionKey(userID) {
			storedSessionID = string(args.Get(1).([]byte))
		}
	}).Return(true, (*model.AppError)(nil))
	fakeAPI.On("GetUser", userID).Return(&model.User{Id: userID, Roles: "system_user"}, (*model.AppError)(nil))
	fakeAPI.On("GetSession", mock.AnythingOfType("string")).Return(
		func(sessionID string) *model.Session {
			return sessions[sessionID]
		},
		func(sessionID string) *model.AppError {
			if sessions[sessionID] == nil {
				return model.NewAppError("GetSessionById", "api.context.session_expired.app_error", nil, "", http.StatusUnauthorized)
			}
			return nil
		},
	)
	fakeAPI.On("CreateSession", mock.AnythingOfType("*model.Session")).Return(func(session *model.Session) *model.Session {
		session.Id = newSessionID
		session.Token = "new-token"
		sessions[newSessionID] = session
		return session
	}, (*model.AppError)(nil))
	defaultConfig := &model.Config{}
	defaultConfig.SetDefaults()
	fakeAPI.On("GetConfig").Return(defaultConfig)

	pluginAPI := pluginapi.NewClient(fakeAPI, nil)
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)
	manager := NewClientManager(
		Config{
			EmbeddedServer: EmbeddedServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{{
					Name:    "session_identity",
					Policy:  "auto_run_in_dm",
					Enabled: true,
				}},
			},
		},
		pluginAPI.Log,
		pluginAPI,
		nil,
		&sessionEchoEmbeddedMCPServer{ctx: runCtx},
		http.DefaultClient,
		nil,
	)
	t.Cleanup(manager.Close)

	tools, mcpErrors := manager.GetToolsForUser(context.Background(), userID, ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, oldSessionID, callSessionIdentityTool(t, tools))

	delete(sessions, oldSessionID)

	tools, mcpErrors = manager.GetToolsForUser(context.Background(), userID, ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, newSessionID, storedSessionID)
	require.Equal(t, newSessionID, callSessionIdentityTool(t, tools))
}

func callSessionIdentityTool(t *testing.T, tools []llm.Tool) string {
	t.Helper()
	requireToolNames(t, tools, "mattermost__session_identity")
	result, err := tools[0].Resolver(context.Background(), &llm.Context{}, func(args any) error {
		*(args.(*map[string]any)) = map[string]any{}
		return nil
	})
	require.NoError(t, err)
	return strings.TrimSpace(result)
}

type sessionEchoEmbeddedMCPServer struct {
	ctx context.Context
}

func (s *sessionEchoEmbeddedMCPServer) CreateClientTransport(_ string, sessionID string, _ *pluginapi.Client) (*gomcp.InMemoryTransport, error) {
	server := gomcp.NewServer(&gomcp.Implementation{Name: "session-echo", Version: "1.0"}, nil)
	server.AddTool(&gomcp.Tool{
		Name:        "session_identity",
		Description: "Returns the session used by this connection",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		return &gomcp.CallToolResult{
			Content: []gomcp.Content{&gomcp.TextContent{Text: sessionID}},
		}, nil
	})
	serverTransport, clientTransport := gomcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(s.ctx, serverTransport)
	}()
	return clientTransport, nil
}

func TestUserClientsGetToolsResolverUsesResolverContext(t *testing.T) {
	callCtx, cancel := context.WithCancel(context.Background())
	cancel()

	server := newTestMCPServer(0, "search")
	session := connectInMemoryTestSession(t, server)
	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			"jira": {
				session: session,
				config:  ServerConfig{Name: "Jira", BaseURL: "https://mcp.atlassian.com", Enabled: true},
				tools: map[string]*gomcp.Tool{
					"search": {
						Name:        "search",
						Description: "Search Jira",
					},
				},
			},
		},
	}

	tools := userClients.GetTools(context.Background())
	require.Len(t, tools, 1)

	_, err := tools[0].Resolver(callCtx, &llm.Context{}, func(args any) error {
		*(args.(*map[string]any)) = map[string]any{}
		return nil
	})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestUserClientsGetToolsResolverWorksWithEmptyLLMContext(t *testing.T) {
	server := newTestMCPServer(0, "search")
	session := connectInMemoryTestSession(t, server)
	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			"jira": {
				session: session,
				config:  ServerConfig{Name: "Jira", BaseURL: "https://mcp.atlassian.com", Enabled: true},
				tools: map[string]*gomcp.Tool{
					"search": {
						Name:        "search",
						Description: "Search Jira",
					},
				},
			},
		},
	}

	tools := userClients.GetTools(context.Background())
	require.Len(t, tools, 1)

	result, err := tools[0].Resolver(context.Background(), &llm.Context{}, func(args any) error {
		*(args.(*map[string]any)) = map[string]any{}
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, "search ok\n", result)
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

func testClientWithTools(name, baseURL string, toolNames ...string) *Client {
	tools := make(map[string]*gomcp.Tool, len(toolNames))
	for _, toolName := range toolNames {
		tools[toolName] = &gomcp.Tool{
			Name:        toolName,
			Description: "Test tool " + toolName,
		}
	}
	return &Client{
		config: ServerConfig{
			Name:    name,
			BaseURL: baseURL,
			Enabled: true,
		},
		tools: tools,
	}
}
