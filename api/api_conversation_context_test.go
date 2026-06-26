// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestHandleGetConversationContext_TotalSource pins each total_source value
// (counted / estimated) to its trigger in the count-tokens fallback chain.
func TestHandleGetConversationContext_TotalSource(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	channelID := testChannelID
	textBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: "What is the weather?"},
	})

	setupConv := func(e *TestEnvironment, convID string) {
		e.conversationStore.conversations[convID] = &store.Conversation{
			ID:           convID,
			UserID:       testUserID,
			BotID:        testBotUserID,
			ChannelID:    &channelID,
			SystemPrompt: "you are a helpful assistant",
			Operation:    "conversation",
		}
		e.conversationStore.turns[convID] = []store.Turn{
			{ID: "turn-1", ConversationID: convID, Role: "user", Content: textBlocks, Sequence: 1},
		}
		e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)
		// buildContextForConversation looks these up to populate Tools.
		e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID}, nil).Maybe()
		e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil).Maybe()
		e.mockAPI.On("GetTeam", mock.AnythingOfType("string")).Return(&model.Team{}, nil).Maybe()
	}

	doGet := func(t *testing.T, e *TestEnvironment, convID string) llm.Composition {
		request := httptest.NewRequest(http.MethodGet, "/conversations/"+convID+"/context", nil)
		request.Header.Add("Mattermost-User-ID", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, request)
		resp := recorder.Result()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var c llm.Composition
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&c))
		return c
	}

	bindFakeLLM := func(t *testing.T, e *TestEnvironment, fake *FakeLLM) {
		t.Helper()
		mmBot := &model.Bot{UserId: testBotUserID, Username: "ai", DisplayName: "AI"}
		bot := bots.NewBot(llm.BotConfig{Name: "ai", DisplayName: "AI"}, llm.ServiceConfig{}, mmBot, fake)
		e.bots.SetBotsForTesting([]*bots.Bot{bot})
	}

	tests := []struct {
		name                string
		convID              string
		fake                *FakeLLM
		expectedTotalSource string
		// assertComposition runs the per-case checks beyond total_source.
		assertComposition func(t *testing.T, c llm.Composition)
	}{
		{
			name:                "counted when provider supports CountTokens",
			convID:              "conv-counted",
			fake:                &FakeLLM{TokenCount: 4242, TokenLimit: 200000},
			expectedTotalSource: "counted",
			assertComposition: func(t *testing.T, c llm.Composition) {
				assert.Equal(t, 4242, c.Total)
				assert.Equal(t, 200000, c.InputTokenLimit)
			},
		},
		{
			name:                "estimated when provider lacks CountTokens",
			convID:              "conv-unsupported",
			fake:                &FakeLLM{CountTokensError: llm.ErrUnsupportedTokenCount, TokenLimit: 200000},
			expectedTotalSource: "estimated",
			assertComposition: func(t *testing.T, c llm.Composition) {
				assert.Greater(t, c.Total, 0, "estimator must still produce a non-zero total")
			},
		},
		{
			// This is the path we currently swallow silently — covered here so
			// any later change that surfaces the error in the response shape
			// (or logs it loudly) has a baseline to compare against.
			name:                "estimated when CountTokens errors (e.g. provider rejected request)",
			convID:              "conv-rejected",
			fake:                &FakeLLM{CountTokensError: errors.New("bifrost count tokens error: messages must alternate roles"), TokenLimit: 200000},
			expectedTotalSource: "estimated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			setupConv(e, tt.convID)
			bindFakeLLM(t, e, tt.fake)

			c := doGet(t, e, tt.convID)
			assert.Equal(t, tt.expectedTotalSource, c.TotalSource,
				"total_source must reflect whether the provider's CountTokens produced the total — "+
					"otherwise the webapp surfaces the wrong trustworthiness caveat")
			if tt.assertComposition != nil {
				tt.assertComposition(t, c)
			}
		})
	}
}

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
			// buildContextForConversation looks these up to populate Tools.
			e.mockAPI.On("GetUser", mock.AnythingOfType("string")).Return(&model.User{Id: tt.userID}, nil).Maybe()
			e.mockAPI.On("GetChannel", mock.AnythingOfType("string")).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil).Maybe()
			e.mockAPI.On("GetTeam", mock.AnythingOfType("string")).Return(&model.Team{}, nil).Maybe()

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
