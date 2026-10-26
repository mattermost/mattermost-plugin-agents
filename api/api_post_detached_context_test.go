// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// streamingContextRecorder records the context each run is given to stream
// with, which is what the real streaming service derives its cancellable
// run context from.
type streamingContextRecorder struct {
	recordingStreamingService

	mu   sync.Mutex
	ctxs []context.Context
}

func (s *streamingContextRecorder) GetStreamingContext(ctx context.Context, _ string) (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctxs = append(s.ctxs, ctx)
	return ctx, nil
}

func (s *streamingContextRecorder) recorded() []context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]context.Context(nil), s.ctxs...)
}

// TestResumeRoutesSurviveRequestCancellation pins that regenerate and the tool
// approval routes hand their run a context that outlives the HTTP request.
// Core cancels the plugin request context as soon as the handler returns,
// while the follow-up stream keeps running in a goroutine; a run derived from
// the raw request context is canceled immediately and ends as if it had been
// stopped. The run must still see the requester's session and the request's
// audit record, which the services read from the context.
func TestResumeRoutesSurviveRequestCancellation(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID    = "post12345678901234567890ab"
		threadID  = "thread45678901234567890abc"
		convID    = "conv12345678901234567890ab"
		sessionID = "session45678901234567890ab"
	)
	dmChannel := &model.Channel{Id: "dmchan345678901234567890ab", Type: model.ChannelTypeDirect, Name: testBotUserID + "__" + testUserID}
	openChannel := &model.Channel{Id: testChannelID, Type: model.ChannelTypeOpen, TeamId: "teamid"}

	toolUse := func(status string) []conversation.ContentBlock {
		return []conversation.ContentBlock{{
			Type:   conversation.BlockTypeToolUse,
			ID:     "tool-use-1",
			Name:   "jira__get_issue",
			Input:  json.RawMessage(`{"issue_key":"MM-1"}`),
			Status: status,
			Shared: new(true),
		}}
	}
	toolResult := []conversation.ContentBlock{{
		Type:      conversation.BlockTypeToolResult,
		ToolUseID: "tool-use-1",
		Content:   "issue details",
		Status:    conversation.StatusSuccess,
		Shared:    new(true),
	}}

	tests := []struct {
		name     string
		endpoint string
		body     string
		channel  *model.Channel
		// turns seeds the conversation's turns, after a leading user turn.
		turns         func(t *testing.T) []store.Turn
		operation     string
		postProps     map[string]any
		wantAuditKeys []string
	}{
		{
			name:      "regenerate",
			endpoint:  "regenerate",
			channel:   dmChannel,
			operation: llm.OperationThreadAnalysis,
			postProps: map[string]any{
				conversations.ThreadIDProp:     threadID,
				conversations.AnalysisTypeProp: "summarize_thread",
			},
		},
		{
			name:      "tool_call continuation",
			endpoint:  "tool_call",
			body:      `{"accepted_tool_ids":["not-a-pending-tool"]}`,
			channel:   dmChannel,
			operation: "conversation",
			turns: func(t *testing.T) []store.Turn {
				return []store.Turn{assistantToolTurn(t, postID, toolUse(conversation.StatusPending))}
			},
			wantAuditKeys: []string{"accepted_tools", "rejected_tools"},
		},
		{
			name:      "tool_result continuation",
			endpoint:  "tool_result",
			body:      `{"accepted_tool_ids":["tool-use-1"]}`,
			channel:   openChannel,
			operation: "conversation",
			turns: func(t *testing.T) []store.Turn {
				return []store.Turn{
					assistantToolTurn(t, postID, toolUse(conversation.StatusSuccess)),
					toolResultTurn(t, toolResult),
				}
			},
			wantAuditKeys: []string{"accepted_tools", "rejected_tools"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			records := e.CaptureAuditRecords()

			streamingService := &streamingContextRecorder{}
			e.api.streamingService = streamingService
			convStore := e.useConversations()
			e.config.enableChannelMentionToolCalling = true
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(&model.License{SkuShortName: "advanced"})

			e.setBots(bots.NewBot(
				llm.BotConfig{ID: testBotUserID, Name: "thebot", DisplayName: "The Bot", UserAccessLevel: llm.UserAccessLevelAll, ChannelAccessLevel: llm.ChannelAccessLevelAll},
				llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
				&model.Bot{UserId: testBotUserID, Username: "thebot"},
				NewFakeLLM("follow-up response"),
			))

			rootID := postID
			conv := &store.Conversation{ID: convID, UserID: testUserID, BotID: testBotUserID, Operation: tc.operation}
			if tc.operation == llm.OperationThreadAnalysis {
				// Analysis answers are regenerable only with a root post; a
				// root post on a channel conversation would make the
				// follow-up re-read the live thread.
				conv.RootPostID = &rootID
			}
			require.NoError(t, convStore.CreateConversation(conv))
			userContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "look up MM-1"}})
			require.NoError(t, err)
			require.NoError(t, convStore.CreateTurnAutoSequence(&store.Turn{ID: "user-turn", ConversationID: convID, Role: "user", Content: userContent}))
			if tc.turns != nil {
				for _, turn := range tc.turns(t) {
					turn.ConversationID = convID
					require.NoError(t, convStore.CreateTurnAutoSequence(&turn))
				}
			}

			post := &model.Post{Id: postID, UserId: testBotUserID, ChannelId: tc.channel.Id}
			post.AddProp(streaming.ConversationIDProp, convID)
			for key, value := range tc.postProps {
				post.AddProp(key, value)
			}

			e.mockAPI.On("GetPost", postID).Return(post, nil)
			e.mockAPI.On("GetPost", threadID).Return(&model.Post{Id: threadID, ChannelId: testChannelID, UserId: testOtherUserID, Message: "hello"}, nil).Maybe()
			e.mockAPI.On("GetPostThread", threadID).Return(&model.PostList{
				Order: []string{threadID},
				Posts: map[string]*model.Post{threadID: {Id: threadID, ChannelId: testChannelID, UserId: testOtherUserID, Message: "hello"}},
			}, nil).Maybe()
			e.mockAPI.On("GetChannel", tc.channel.Id).Return(tc.channel, nil)
			e.mockAPI.On("GetChannel", testChannelID).Return(openChannel, nil).Maybe()
			e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil).Maybe()
			e.mockAPI.On("HasPermissionToChannel", testUserID, tc.channel.Id, model.PermissionReadChannel).Return(true)
			e.mockAPI.On("HasPermissionToChannel", testUserID, testChannelID, model.PermissionReadChannel).Return(true).Maybe()
			e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Username: "requester", Locale: "en"}, nil).Maybe()
			e.mockAPI.On("KVGet", mock.Anything).Return([]byte(nil), (*model.AppError)(nil)).Maybe()
			e.mockAPI.On("GetUser", testOtherUserID).Return(&model.User{Id: testOtherUserID, Username: "author"}, nil).Maybe()

			reqCtx, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			req := httptest.NewRequestWithContext(reqCtx, http.MethodPost, "/post/"+postID+"/"+tc.endpoint, strings.NewReader(tc.body))
			req.Header.Add("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, recorder, req)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())

			// What core does once the handler has returned.
			cancelRequest()

			runCtxs := streamingService.recorded()
			require.Len(t, runCtxs, 1, "the route must start exactly one run")
			runCtx := runCtxs[0]
			require.NoError(t, runCtx.Err(), "canceling the request must not cancel the run it started")
			assert.Equal(t, sessionID, auth.SessionIDFromContext(runCtx), "the run must keep the requester's session for file policy checks")

			if len(tc.wantAuditKeys) > 0 {
				require.Len(t, *records, 1)
				for _, key := range tc.wantAuditKeys {
					assert.Contains(t, (*records)[0].EventData.Parameters, key, "the service must enrich the request's audit record")
				}
				assert.NotNil(t, audit.RecordFromContext(runCtx), "the run keeps the request's audit record")
			}
		})
	}
}

func assistantToolTurn(t *testing.T, postID string, blocks []conversation.ContentBlock) store.Turn {
	t.Helper()
	content, err := json.Marshal(blocks)
	require.NoError(t, err)
	return store.Turn{ID: "assistant-turn", Role: "assistant", PostID: &postID, Content: content}
}

func toolResultTurn(t *testing.T, blocks []conversation.ContentBlock) store.Turn {
	t.Helper()
	content, err := json.Marshal(blocks)
	require.NoError(t, err)
	return store.Turn{ID: "result-turn", Role: "tool_result", Content: content}
}
