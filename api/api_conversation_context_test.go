// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestHandleGetConversationContext pins the auth + response contract for the
// per-thread composition endpoint. It must mirror handleGetConversation's
// auth (channel-member or DM owner), and the body must carry a Composition
// the webapp can render directly.
func TestHandleGetConversationContext(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	channelID := testChannelID

	textBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: "What is the weather?"},
	})
	assistantBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: "Let me check."},
	})

	tests := []struct {
		name           string
		userID         string
		conversationID string
		setup          func(e *TestEnvironment)
		expectedStatus int
		validate       func(t *testing.T, resp *http.Response)
	}{
		{
			name:           "owner in DM sees composition",
			userID:         testUserID,
			conversationID: "conv-dm",
			setup: func(e *TestEnvironment) {
				dmChannelID := "dmchan123456789012345678"
				e.conversationStore.conversations["conv-dm"] = &store.Conversation{
					ID:           "conv-dm",
					UserID:       testUserID,
					BotID:        testBotUserID,
					ChannelID:    &dmChannelID,
					SystemPrompt: "you are a helpful assistant",
					Operation:    "conversation",
				}
				e.conversationStore.turns["conv-dm"] = []store.Turn{
					{ID: "turn-1", ConversationID: "conv-dm", Role: "user", Content: textBlocks, Sequence: 1},
					{ID: "turn-2", ConversationID: "conv-dm", Role: "assistant", Content: assistantBlocks, Sequence: 2},
				}
				e.mockAPI.On("HasPermissionToChannel", testUserID, dmChannelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				var c llm.Composition
				err := json.NewDecoder(resp.Body).Decode(&c)
				require.NoError(t, err)
				require.NotEmpty(t, c.Components, "expected at least one composition row")

				bySource := map[llm.CompositionSource]bool{}
				for _, comp := range c.Components {
					bySource[comp.Source] = true
				}
				assert.True(t, bySource[llm.SourceSystem], "system prompt must appear in the breakdown")
				assert.True(t, bySource[llm.SourceHistory], "user/assistant text must appear in the breakdown")

				assert.NotEmpty(t, c.TotalSource, "TotalSource must be set so callers know how trustworthy Total is")
			},
		},
		{
			name:           "non-channel-member gets 403",
			userID:         testOtherUserID,
			conversationID: "conv-chan-blocked",
			setup: func(e *TestEnvironment) {
				e.conversationStore.conversations["conv-chan-blocked"] = &store.Conversation{
					ID:        "conv-chan-blocked",
					UserID:    testUserID,
					BotID:     testBotUserID,
					ChannelID: &channelID,
					Operation: "conversation",
				}
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, channelID, model.PermissionReadChannel).Return(false)
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "threadless conversation rejected for non-owner",
			userID:         testOtherUserID,
			conversationID: "conv-threadless-blocked",
			setup: func(e *TestEnvironment) {
				e.conversationStore.conversations["conv-threadless-blocked"] = &store.Conversation{
					ID:        "conv-threadless-blocked",
					UserID:    testUserID,
					BotID:     testBotUserID,
					ChannelID: nil,
					Operation: "conversation",
				}
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "threadless owner sees composition",
			userID:         testUserID,
			conversationID: "conv-threadless-ok",
			setup: func(e *TestEnvironment) {
				e.conversationStore.conversations["conv-threadless-ok"] = &store.Conversation{
					ID:           "conv-threadless-ok",
					UserID:       testUserID,
					BotID:        testBotUserID,
					ChannelID:    nil,
					SystemPrompt: "background agent prompt",
					Operation:    "conversation",
				}
				e.conversationStore.turns["conv-threadless-ok"] = []store.Turn{
					{ID: "turn-1", ConversationID: "conv-threadless-ok", Role: "user", Content: textBlocks, Sequence: 1},
				}
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				var c llm.Composition
				err := json.NewDecoder(resp.Body).Decode(&c)
				require.NoError(t, err)
				assert.NotEmpty(t, c.Components)
			},
		},
		{
			name:           "nonexistent conversation returns 404",
			userID:         testUserID,
			conversationID: "no-such-conv",
			setup:          func(e *TestEnvironment) {},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "unauthenticated request returns 401",
			userID:         "",
			conversationID: "conv-x",
			setup:          func(e *TestEnvironment) {},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			tt.setup(e)
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			request := httptest.NewRequest(http.MethodGet, "/conversations/"+tt.conversationID+"/context", nil)
			if tt.userID != "" {
				request.Header.Add("Mattermost-User-ID", tt.userID)
			}
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)
			resp := recorder.Result()
			require.Equal(t, tt.expectedStatus, resp.StatusCode)

			if tt.validate != nil {
				tt.validate(t, resp)
			}
		})
	}
}
