// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
)

// TestToolApprovalClickOnFirstVisiblePendingCard pins that a click sent the
// moment the requester's client receives a pending tool_call event resolves
// against the stored turn instead of being rejected as stale.
func TestToolApprovalClickOnFirstVisiblePendingCard(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID    = "post12345678901234567890ab"
		channelID = "chan12345678901234567890ab"
		convID    = "conv12345678901234567890ab"
	)

	approvalCall := llm.ToolCall{ID: "tool-use-1", Name: "create_post", Arguments: []byte(`{}`), Status: llm.ToolCallStatusPending}
	questionCall := llm.ToolCall{
		ID:              "tool-use-1",
		Name:            mmtools.AskUserQuestionToolName,
		Arguments:       []byte(`{"question":"Which color?","options":[{"label":"Blue"},{"label":"Red"}]}`),
		Status:          llm.ToolCallStatusPending,
		UserInteraction: llm.UserInteractionSelect,
	}

	autoRunCall := llm.ToolCall{ID: "tool-use-0", Name: "read_channel", Arguments: []byte(`{}`), Status: llm.ToolCallStatusPending, WouldAutoExecute: true}
	autoRunDone := autoRunCall
	autoRunDone.Status = llm.ToolCallStatusAutoApproved
	autoRunDone.Result = "ok"

	tests := []struct {
		name        string
		priorRounds [][]llm.ToolCall
		toolCall    llm.ToolCall
		body        string
	}{
		{name: "accept", toolCall: approvalCall, body: `{"accepted_tool_ids":["tool-use-1"]}`},
		{name: "reject", toolCall: approvalCall, body: `{"accepted_tool_ids":[]}`},
		{name: "accept after an auto-run round", priorRounds: [][]llm.ToolCall{{autoRunCall}, {autoRunDone}}, toolCall: approvalCall, body: `{"accepted_tool_ids":["tool-use-1"]}`},
		{name: "ask-user answer", toolCall: questionCall, body: `{"accepted_tool_ids":["tool-use-1"],"tool_answers":{"tool-use-1":{"selected":["Blue"]}}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			streamService := streaming.NewMMPostStreamService(mmapi.NewClient(e.client), nil)
			e.api.streamingService = streamService
			convStore := e.useConversations()
			streamService.SetTurnStore(convStore)

			e.setBots(bots.NewBot(
				llm.BotConfig{ID: testBotUserID, Name: "bot", UserAccessLevel: llm.UserAccessLevelAll, ChannelAccessLevel: llm.ChannelAccessLevelAll},
				llm.ServiceConfig{},
				&model.Bot{UserId: testBotUserID, Username: "bot"},
				&FakeLLM{Response: "done"},
			))
			require.NoError(t, convStore.CreateConversation(&store.Conversation{ID: convID, UserID: testUserID, BotID: testBotUserID, Operation: "conversation"}))

			post := &model.Post{Id: postID, UserId: testBotUserID, ChannelId: channelID}
			post.AddProp(streaming.ConversationIDProp, convID)
			e.mockAPI.On("GetPost", postID).Return(func(string) (*model.Post, *model.AppError) { return post.Clone(), nil }).Maybe()
			e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{
				Id:   channelID,
				Name: testBotUserID + "__" + testUserID,
				Type: model.ChannelTypeDirect,
			}, nil).Maybe()
			e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true).Maybe()
			e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Locale: "en"}, nil).Maybe()
			e.mockAPI.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
			e.mockAPI.On("UpdatePost", mock.Anything).Return(func(p *model.Post) (*model.Post, *model.AppError) { return p.Clone(), nil }).Maybe()

			var clicked atomic.Bool
			clickStatus := 0
			clickBody := ""
			resumeEnded := make(chan struct{})
			var ends atomic.Int32
			e.mockAPI.On("PublishWebSocketEvent", "postupdate", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				payload, _ := args.Get(1).(map[string]any)
				broadcast, _ := args.Get(2).(*model.WebsocketBroadcast)
				if payload["control"] == streaming.PostStreamingControlEnd && ends.Add(1) == 2 {
					close(resumeEnded)
				}
				if payload["control"] != "tool_call" || broadcast == nil || broadcast.UserId != testUserID {
					return
				}
				var calls []llm.ToolCall
				if raw, _ := payload["tool_call"].(string); json.Unmarshal([]byte(raw), &calls) != nil {
					return
				}
				// Only a card with a call awaiting the user has controls to click.
				if !slices.ContainsFunc(calls, func(tc llm.ToolCall) bool {
					return tc.Status == llm.ToolCallStatusPending && !tc.WouldAutoExecute
				}) {
					return
				}
				if !clicked.CompareAndSwap(false, true) {
					return
				}
				req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/tool_call", strings.NewReader(test.body))
				req.Header.Add("Mattermost-User-Id", testUserID)
				rec := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, rec, req)
				clickStatus = rec.Code
				clickBody = rec.Body.String()
			}).Maybe()

			events := make(chan llm.TextStreamEvent, len(test.priorRounds)+2)
			for _, round := range test.priorRounds {
				phase := llm.ToolCallPhaseExecuting
				if round[0].Status != llm.ToolCallStatusPending {
					phase = llm.ToolCallPhaseResolved
				}
				events <- llm.TextStreamEvent{Type: llm.EventTypeToolCallBatch, Value: llm.ToolCallBatch{Phase: phase, Calls: round}}
			}
			events <- llm.TextStreamEvent{Type: llm.EventTypeToolCallBatch, Value: llm.ToolCallBatch{Phase: llm.ToolCallPhaseAwaitingDecision, Calls: []llm.ToolCall{test.toolCall}}}
			events <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
			close(events)

			streamService.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: events}, post, "en", testUserID)

			require.NotZero(t, clickStatus, "the requester never received the pending tool_call event")
			require.Equal(t, http.StatusOK, clickStatus, clickBody)
			select {
			case <-resumeEnded:
			case <-time.After(10 * time.Second):
				require.Fail(t, "the resumed run never finished")
			}
		})
	}
}
