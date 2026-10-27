// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"
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
	// tools, when set, replaces the single default tool.
	tools []llm.Tool
}

func (p *scopedBridgeCatalog) GetToolsWithSelection(_ context.Context, req mcp.CatalogRequest, _ mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
	if req.Scoped != nil {
		p.scopedRequests++
	} else {
		p.unscopedRequests++
	}
	if p.tools != nil {
		return p.tools, nil
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
		name         string
		channelID    string
		allowedTools []string
		noScoped     bool
		wantStatus   int
		wantErr      string
		wantScoped   bool
		wantUnscoped bool
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
			name:         "server without scoped sessions refuses tools",
			channelID:    publicChannel.Id,
			allowedTools: []string{"jira__get_issue"},
			noScoped:     true,
			wantStatus:   http.StatusForbidden,
			wantErr:      "scoped channel mode is not available",
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
			if !tt.noScoped {
				manager.SetScopedSessions(minter)
			}
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

// A scoped Bridge run whose session is revoked while it executes a tool runs
// nothing further unasked: the next allowlisted call is handed back to the
// caller, whether the response is streamed or not.
func TestBridgeScopedRunStopsAutoRunningWhenRevoked(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	respondStream := func(a *API, c *gin.Context, plan *bridgeCompletionPlan) {
		a.streamLLMResponse(c, plan.bot.LLM(), plan.bot.GetConfig().EffectiveMaxToolTurns(), plan.request, plan.shouldExecute, plan.opts...)
	}
	respondNoStream := func(a *API, c *gin.Context, plan *bridgeCompletionPlan) {
		a.handleNonStreamingLLMResponse(c, plan.bot.LLM(), plan.bot.GetConfig().EffectiveMaxToolTurns(), plan.request, plan.shouldExecute, plan.opts...)
	}

	tests := []struct {
		name         string
		respond      func(*API, *gin.Context, *bridgeCompletionPlan)
		wantReturned bool
		subType      string
	}{
		{name: "streaming", respond: respondStream, wantReturned: true, subType: llm.SubTypeStreaming},
		{name: "non-streaming", respond: respondNoStream, subType: llm.SubTypeNoStream},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			channel := &model.Channel{Id: model.NewId(), TeamId: model.NewId(), Type: model.ChannelTypeOpen}
			e.mockAPI.On("GetChannel", channel.Id).Return(channel, nil).Maybe()
			e.mockAPI.On("GetTeam", mock.Anything).Return(&model.Team{Id: channel.TeamId}, nil).Maybe()
			e.mockAPI.On("HasPermissionToChannel", testUserID, mock.Anything, mock.Anything).Return(true).Maybe()
			e.mockAPI.On("KVGet", mock.Anything).Return(nil, nil).Maybe()

			manager := mcp.NewClientManager(mcp.Config{IdleTimeoutMinutes: 30}, e.client.Log, e.client, nil, nil, http.DefaultClient, nil, mcp.RemoteMCPAlwaysAllowed)
			t.Cleanup(manager.Close)
			manager.SetScopedSessions(&scopedBridgeMinter{})

			var firstRuns, secondRuns atomic.Int32
			first := bridgeMCPTool("jira", "get_issue", scopedBridgeOrigin)
			first.Resolver = func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
				firstRuns.Add(1)
				manager.RevokeScopedSessionsWhere(func(*mcp.ScopedRun) bool { return true })
				return "issue", nil
			}
			second := bridgeMCPTool("jira", "get_comments", scopedBridgeOrigin)
			second.Resolver = func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
				secondRuns.Add(1)
				return "comments", nil
			}
			catalog := &scopedBridgeCatalog{ClientManager: manager, tools: []llm.Tool{first, second}}
			e.api.contextBuilder = llmcontext.NewLLMContextBuilder(e.client, &testLLMContextToolProvider{}, catalog, scopedBridgeConfig{channelToolCalling: true})

			e.setupTestBot(llm.BotConfig{
				Name:                  "testbot",
				DisplayName:           "Test Bot",
				UserAccessLevel:       llm.UserAccessLevelAll,
				ChannelAccessLevel:    llm.ChannelAccessLevelAll,
				UseServiceAccountAuth: true,
				ScopedChannelMode:     true,
				EnabledMCPTools: []llm.EnabledMCPTool{
					{ServerOrigin: scopedBridgeOrigin, ToolName: "get_issue"},
					{ServerOrigin: scopedBridgeOrigin, ToolName: "get_comments"},
				},
			})
			fakeLLM := NewFakeLLM("")
			fakeLLM.StreamEventSequence = [][]llm.TextStreamEvent{
				{{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{ID: "call-1", Name: "jira__get_issue", Arguments: json.RawMessage(`{}`)}}}, {Type: llm.EventTypeEnd}},
				{{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{ID: "call-2", Name: "jira__get_comments", Arguments: json.RawMessage(`{}`)}}}, {Type: llm.EventTypeEnd}},
				{{Type: llm.EventTypeText, Value: "done"}, {Type: llm.EventTypeEnd}},
			}
			for _, bot := range e.bots.GetAllBots() {
				bot.SetLLMForTest(fakeLLM)
			}

			plan, status, err := e.api.prepareAgentBridgeCompletion(context.Background(), testBotUserID, bridgeclient.CompletionRequest{
				Posts:        []bridgeclient.Post{{Role: "user", Message: "Find the issue and its comments"}},
				UserID:       testUserID,
				ChannelID:    channel.Id,
				AllowedTools: []string{"jira__get_issue", "jira__get_comments"},
			}, llm.OperationBridgeAgent, tt.subType)
			require.NoError(t, err, "status %d", status)
			t.Cleanup(plan.request.Context.ReleaseScopedSession)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			tt.respond(e.api, c, plan)

			assert.EqualValues(t, 1, firstRuns.Load())
			assert.Zero(t, secondRuns.Load(), "a call after the revocation never runs")

			var returned []llm.ToolCall
			scanner := bufio.NewScanner(strings.NewReader(recorder.Body.String()))
			for scanner.Scan() {
				payload, ok := strings.CutPrefix(scanner.Text(), "data: ")
				if !ok {
					continue
				}
				var event struct {
					Type  llm.EventType
					Value json.RawMessage
				}
				require.NoError(t, json.Unmarshal([]byte(payload), &event))
				if event.Type != llm.EventTypeToolCalls {
					continue
				}
				var calls []llm.ToolCall
				require.NoError(t, json.Unmarshal(event.Value, &calls))
				for _, call := range calls {
					if call.ID == "call-2" && !call.WouldAutoExecute {
						returned = append(returned, call)
					}
				}
			}
			if tt.wantReturned {
				assert.Len(t, returned, 1, "the call is handed back to the caller")
			}
		})
	}
}
