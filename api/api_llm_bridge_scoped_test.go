// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const scopedBridgeOrigin = "https://jira.example.com"

type scopedBridgeConfig struct{ channelToolCalling bool }

func (scopedBridgeConfig) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

func (c scopedBridgeConfig) EnableChannelMentionToolCalling() bool { return c.channelToolCalling }

type scopedBridgeMinter struct {
	mu      sync.Mutex
	minted  []string
	revoked []string
}

func (m *scopedBridgeMinter) CreateScopedSession(userID string, scope *model.SessionScope, expiresAt int64) (*model.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := &model.Session{Id: model.NewId(), UserId: userID, ExpiresAt: expiresAt}
	m.minted = append(m.minted, session.Id)
	return session, nil
}

func (m *scopedBridgeMinter) RevokeSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked = append(m.revoked, sessionID)
	return nil
}

// scopedBridgeCatalog mints through a real ClientManager and serves one tool.
type scopedBridgeCatalog struct {
	*mcp.ClientManager
	scopedRequests   int
	unscopedRequests int
}

func (p *scopedBridgeCatalog) GetToolsWithSelection(_ context.Context, req mcp.CatalogRequest, _ mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
	if req.Scoped != nil {
		p.scopedRequests++
	} else {
		p.unscopedRequests++
	}
	return []llm.Tool{bridgeMCPTool("jira", "get_issue", scopedBridgeOrigin)}, nil
}

func TestBridgeScopedAgentCompletion(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	publicChannel := &model.Channel{Id: model.NewId(), TeamId: model.NewId(), Type: model.ChannelTypeOpen}
	unreadableChannel := &model.Channel{Id: model.NewId(), TeamId: publicChannel.TeamId, Type: model.ChannelTypePrivate}
	dmChannel := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect}

	tests := []struct {
		name          string
		channelID     string
		allowedTools  []string
		serverVersion string
		wantStatus    int
		wantErr       string
		wantScoped    bool
		wantUnscoped  bool
	}{
		{
			name:         "no channel_id",
			allowedTools: []string{"jira__get_issue"},
			wantStatus:   http.StatusBadRequest,
			wantErr:      "channel_id is required",
		},
		{
			name:       "no channel_id without tools",
			wantStatus: http.StatusBadRequest,
			wantErr:    "channel_id is required",
		},
		{
			name:         "channel request runs on a scoped session",
			channelID:    publicChannel.Id,
			allowedTools: []string{"jira__get_issue"},
			wantScoped:   true,
		},
		{
			name:         "DM request is unchanged",
			channelID:    dmChannel.Id,
			allowedTools: []string{"jira__get_issue"},
			wantUnscoped: true,
		},
		{
			name:          "server without scoped sessions refuses tools",
			channelID:     publicChannel.Id,
			allowedTools:  []string{"jira__get_issue"},
			serverVersion: "12.1.0",
			wantStatus:    http.StatusForbidden,
			wantErr:       "scoped channel mode is not available",
		},
		{
			name:         "private channel the user cannot read",
			channelID:    unreadableChannel.Id,
			allowedTools: []string{"jira__get_issue"},
			wantStatus:   http.StatusForbidden,
			wantErr:      "user_id cannot read channel_id",
		},
		{
			name:      "channel request without tools mints nothing",
			channelID: publicChannel.Id,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			serverVersion := tt.serverVersion
			if serverVersion == "" {
				serverVersion = scopedsession.MinServerVersion
			}
			e.mockAPI.On("GetServerVersion").Return(serverVersion).Maybe()
			e.mockAPI.On("GetChannel", publicChannel.Id).Return(publicChannel, nil).Maybe()
			e.mockAPI.On("GetChannel", dmChannel.Id).Return(dmChannel, nil).Maybe()
			e.mockAPI.On("GetChannel", unreadableChannel.Id).Return(unreadableChannel, nil).Maybe()
			e.mockAPI.On("HasPermissionToChannel", testUserID, unreadableChannel.Id, model.PermissionReadChannel).Return(false).Maybe()
			e.mockAPI.On("GetTeam", mock.Anything).Return(&model.Team{Id: publicChannel.TeamId}, nil).Maybe()
			e.mockAPI.On("HasPermissionToChannel", testUserID, mock.Anything, mock.Anything).Return(true).Maybe()
			e.mockAPI.On("KVGet", mock.Anything).Return(nil, nil).Maybe()

			minter := &scopedBridgeMinter{}
			manager := mcp.NewClientManager(mcp.Config{IdleTimeoutMinutes: 30}, e.client.Log, e.client, nil, nil, http.DefaultClient, nil, mcp.RemoteMCPAlwaysAllowed)
			t.Cleanup(manager.Close)
			manager.SetScopedSessions(minter)
			catalog := &scopedBridgeCatalog{ClientManager: manager}
			e.api.contextBuilder = llmcontext.NewLLMContextBuilder(e.client, &testLLMContextToolProvider{}, catalog, scopedBridgeConfig{channelToolCalling: true})

			e.setupTestBot(llm.BotConfig{
				Name:                  "testbot",
				DisplayName:           "Test Bot",
				UserAccessLevel:       llm.UserAccessLevelAll,
				ChannelAccessLevel:    llm.ChannelAccessLevelAll,
				UseServiceAccountAuth: true,
				ScopedChannelMode:     true,
				EnabledMCPTools:       []llm.EnabledMCPTool{{ServerOrigin: scopedBridgeOrigin, ToolName: "get_issue"}},
			})

			req := bridgeclient.CompletionRequest{
				Posts:        []bridgeclient.Post{{Role: "user", Message: "Find the issue"}},
				UserID:       testUserID,
				ChannelID:    tt.channelID,
				AllowedTools: tt.allowedTools,
			}
			plan, status, err := e.api.prepareAgentBridgeCompletion(context.Background(), testBotUserID, req, llm.OperationBridgeAgent, llm.SubTypeNoStream)
			if tt.wantStatus != 0 {
				require.Error(t, err)
				assert.Equal(t, tt.wantStatus, status)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Empty(t, minter.minted)
				assert.Zero(t, catalog.unscopedRequests, "a refused request never builds the requester's catalog")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantScoped, plan.request.Context.ToolsScoped)
			assert.Equal(t, tt.wantScoped, catalog.scopedRequests > 0)
			assert.Equal(t, tt.wantUnscoped, catalog.unscopedRequests > 0)

			plan.request.Context.ReleaseScopedSession()
			if tt.wantScoped {
				require.Len(t, minter.minted, 1)
				assert.Equal(t, minter.minted, minter.revoked, "the session ends with the request")
			} else {
				assert.Empty(t, minter.minted)
			}
		})
	}
}
