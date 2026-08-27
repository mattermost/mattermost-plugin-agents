// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
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

type sessionLookupPluginAPI struct {
	plugintest.API
	sessionByID map[string]*model.Session
}

func (f *sessionLookupPluginAPI) LogDebug(string, ...interface{}) {}

func (f *sessionLookupPluginAPI) LogInfo(string, ...interface{}) {}

func (f *sessionLookupPluginAPI) LogWarn(string, ...interface{}) {}

func (f *sessionLookupPluginAPI) LogError(string, ...interface{}) {}

func (f *sessionLookupPluginAPI) GetSession(sessionID string) (*model.Session, *model.AppError) {
	if f.sessionByID == nil {
		return nil, nil
	}
	return f.sessionByID[sessionID], nil
}

func TestConnectToEmbeddedServerIfAvailable_ReconnectsWhenSessionChanges(t *testing.T) {
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)

	fakeAPI := &sessionLookupPluginAPI{
		sessionByID: map[string]*model.Session{
			"old-session": {Id: "old-session", UserId: "alice", Token: "old-token"},
			"new-session": {Id: "new-session", UserId: "alice", Token: "new-token"},
		},
	}
	pluginAPI := pluginapi.NewClient(fakeAPI, nil)
	embeddedClient := NewEmbeddedServerClient(&sessionEchoEmbeddedMCPServer{ctx: runCtx}, pluginAPI.Log, pluginAPI)
	uc := NewUserClients("alice", pluginAPI.Log, nil, nil, nil)
	cfg := EmbeddedServerConfig{Enabled: true}

	require.NoError(t, uc.ConnectToEmbeddedServerIfAvailable("old-session", embeddedClient, cfg))
	require.Equal(t, "old-session", callSessionIdentityTool(t, uc.GetTools()))

	require.NoError(t, uc.ConnectToEmbeddedServerIfAvailable("new-session", embeddedClient, cfg))
	require.Equal(t, "new-session", callSessionIdentityTool(t, uc.GetTools()))
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
		if args.String(0) != buildEmbeddedSessionKey(userID) {
			return
		}
		data, _ := args.Get(1).([]byte)
		storedSessionID = string(data)
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
	)
	t.Cleanup(manager.Close)

	tools, mcpErrors := manager.GetToolsForUser(userID)
	require.Nil(t, mcpErrors)
	require.Equal(t, oldSessionID, callSessionIdentityTool(t, tools))

	delete(sessions, oldSessionID)

	tools, mcpErrors = manager.GetToolsForUser(userID)
	require.Nil(t, mcpErrors)
	require.Equal(t, newSessionID, storedSessionID)
	require.Equal(t, newSessionID, callSessionIdentityTool(t, tools))
}

func callSessionIdentityTool(t *testing.T, tools []llm.Tool) string {
	t.Helper()
	require.Len(t, tools, 1)
	require.Equal(t, "session_identity", tools[0].Name)
	result, err := tools[0].Resolver(&llm.Context{}, func(args any) error {
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
