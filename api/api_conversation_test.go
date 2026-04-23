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
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandleGetConversation(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	channelID := testChannelID

	// Build shared content blocks used across tests
	toolUseInput := json.RawMessage(`{"city":"NYC"}`)
	unsharedToolBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: "Let me check the weather."},
		{Type: conversation.BlockTypeToolUse, ID: "tc_01", Name: "get_weather", Input: toolUseInput, Status: conversation.StatusPending, Shared: conversation.BoolPtr(false)},
	})
	unsharedToolResultBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeToolResult, ToolUseID: "tc_01", Content: "72F, sunny", Status: conversation.StatusSuccess, Shared: conversation.BoolPtr(false)},
	})
	sharedToolBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: "Let me check the weather."},
		{Type: conversation.BlockTypeToolUse, ID: "tc_02", Name: "get_weather", Input: toolUseInput, Status: conversation.StatusSuccess, Shared: conversation.BoolPtr(true)},
	})
	sharedToolResultBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeToolResult, ToolUseID: "tc_02", Content: "72F, sunny", Status: conversation.StatusSuccess, Shared: conversation.BoolPtr(true)},
	})
	textOnlyBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: "What is the weather in NYC?"},
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
			name:           "owner in DM gets full unredacted content",
			userID:         testUserID,
			conversationID: "conv-dm-owner",
			setup: func(e *TestEnvironment) {
				dmChannelID := "dmchan123456789012345678"
				e.conversationStore.conversations["conv-dm-owner"] = &store.Conversation{
					ID:        "conv-dm-owner",
					UserID:    testUserID,
					BotID:     testBotUserID,
					ChannelID: &dmChannelID,
					Title:     "Weather Chat",
					Operation: "conversation",
				}
				postID := "post01234567890123456789"
				e.conversationStore.turns["conv-dm-owner"] = []store.Turn{
					{ID: "turn-1", ConversationID: "conv-dm-owner", Role: "user", Content: textOnlyBlocks, Sequence: 1},
					{ID: "turn-2", ConversationID: "conv-dm-owner", PostID: &postID, Role: "assistant", Content: unsharedToolBlocks, TokensIn: 100, TokensOut: 50, Sequence: 2},
					{ID: "turn-3", ConversationID: "conv-dm-owner", Role: "tool_result", Content: unsharedToolResultBlocks, Sequence: 3},
				}
				e.mockAPI.On("HasPermissionToChannel", testUserID, dmChannelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				var response ConversationResponse
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				require.Len(t, response.Turns, 3)

				// Owner should see full tool_use input
				var assistantBlocks []conversation.ContentBlock
				err = json.Unmarshal(response.Turns[1].Content, &assistantBlocks)
				require.NoError(t, err)
				require.Len(t, assistantBlocks, 2)
				assert.NotNil(t, assistantBlocks[1].Input, "owner should see tool_use input")
				assert.JSONEq(t, `{"city":"NYC"}`, string(assistantBlocks[1].Input))

				// Owner should see full tool_result content
				var resultBlocks []conversation.ContentBlock
				err = json.Unmarshal(response.Turns[2].Content, &resultBlocks)
				require.NoError(t, err)
				require.Len(t, resultBlocks, 1)
				assert.Equal(t, "72F, sunny", resultBlocks[0].Content, "owner should see tool_result content")
			},
		},
		{
			name:           "owner in channel gets full unredacted content",
			userID:         testUserID,
			conversationID: "conv-chan-owner",
			setup: func(e *TestEnvironment) {
				e.conversationStore.conversations["conv-chan-owner"] = &store.Conversation{
					ID:        "conv-chan-owner",
					UserID:    testUserID,
					BotID:     testBotUserID,
					ChannelID: &channelID,
					Title:     "Channel Weather Chat",
					Operation: "conversation",
				}
				e.conversationStore.turns["conv-chan-owner"] = []store.Turn{
					{ID: "turn-1", ConversationID: "conv-chan-owner", Role: "user", Content: textOnlyBlocks, Sequence: 1},
					{ID: "turn-2", ConversationID: "conv-chan-owner", Role: "assistant", Content: unsharedToolBlocks, Sequence: 2},
				}
				e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				var response ConversationResponse
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				require.Len(t, response.Turns, 2)

				var assistantBlocks []conversation.ContentBlock
				err = json.Unmarshal(response.Turns[1].Content, &assistantBlocks)
				require.NoError(t, err)
				assert.NotNil(t, assistantBlocks[1].Input, "owner should see tool_use input in channel")
			},
		},
		{
			name:           "non-owner in channel gets filtered content",
			userID:         testOtherUserID,
			conversationID: "conv-chan-nonowner",
			setup: func(e *TestEnvironment) {
				e.conversationStore.conversations["conv-chan-nonowner"] = &store.Conversation{
					ID:        "conv-chan-nonowner",
					UserID:    testUserID,
					BotID:     testBotUserID,
					ChannelID: &channelID,
					Title:     "Channel Weather Chat",
					Operation: "conversation",
				}
				e.conversationStore.turns["conv-chan-nonowner"] = []store.Turn{
					{ID: "turn-1", ConversationID: "conv-chan-nonowner", Role: "user", Content: textOnlyBlocks, Sequence: 1},
					{ID: "turn-2", ConversationID: "conv-chan-nonowner", Role: "assistant", Content: unsharedToolBlocks, Sequence: 2},
					{ID: "turn-3", ConversationID: "conv-chan-nonowner", Role: "tool_result", Content: unsharedToolResultBlocks, Sequence: 3},
				}
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				var response ConversationResponse
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				require.Len(t, response.Turns, 3)

				// Text block should be untouched
				var userBlocks []conversation.ContentBlock
				err = json.Unmarshal(response.Turns[0].Content, &userBlocks)
				require.NoError(t, err)
				assert.Equal(t, "What is the weather in NYC?", userBlocks[0].Text)

				// Non-owner should not see tool_use input
				var assistantBlocks []conversation.ContentBlock
				err = json.Unmarshal(response.Turns[1].Content, &assistantBlocks)
				require.NoError(t, err)
				require.Len(t, assistantBlocks, 2)
				assert.Equal(t, "Let me check the weather.", assistantBlocks[0].Text, "text block should be untouched")
				assert.Nil(t, assistantBlocks[1].Input, "non-owner should not see tool_use input")

				// Non-owner should not see tool_result content
				var resultBlocks []conversation.ContentBlock
				err = json.Unmarshal(response.Turns[2].Content, &resultBlocks)
				require.NoError(t, err)
				require.Len(t, resultBlocks, 1)
				assert.Equal(t, "", resultBlocks[0].Content, "non-owner should not see tool_result content")
			},
		},
		{
			name:           "non-owner sees shared tool blocks in full",
			userID:         testOtherUserID,
			conversationID: "conv-chan-shared",
			setup: func(e *TestEnvironment) {
				e.conversationStore.conversations["conv-chan-shared"] = &store.Conversation{
					ID:        "conv-chan-shared",
					UserID:    testUserID,
					BotID:     testBotUserID,
					ChannelID: &channelID,
					Title:     "Shared Tools",
					Operation: "conversation",
				}
				e.conversationStore.turns["conv-chan-shared"] = []store.Turn{
					{ID: "turn-1", ConversationID: "conv-chan-shared", Role: "assistant", Content: sharedToolBlocks, Sequence: 1},
					{ID: "turn-2", ConversationID: "conv-chan-shared", Role: "tool_result", Content: sharedToolResultBlocks, Sequence: 2},
				}
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				var response ConversationResponse
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				require.Len(t, response.Turns, 2)

				// Shared tool_use input should be visible
				var assistantBlocks []conversation.ContentBlock
				err = json.Unmarshal(response.Turns[0].Content, &assistantBlocks)
				require.NoError(t, err)
				assert.NotNil(t, assistantBlocks[1].Input, "shared tool_use input should be visible to non-owner")
				assert.JSONEq(t, `{"city":"NYC"}`, string(assistantBlocks[1].Input))

				// Shared tool_result content should be visible
				var resultBlocks []conversation.ContentBlock
				err = json.Unmarshal(response.Turns[1].Content, &resultBlocks)
				require.NoError(t, err)
				assert.Equal(t, "72F, sunny", resultBlocks[0].Content, "shared tool_result content should be visible to non-owner")
			},
		},
		{
			name:           "non-channel-member gets 403",
			userID:         testOtherUserID,
			conversationID: "conv-chan-noaccess",
			setup: func(e *TestEnvironment) {
				e.conversationStore.conversations["conv-chan-noaccess"] = &store.Conversation{
					ID:        "conv-chan-noaccess",
					UserID:    testUserID,
					BotID:     testBotUserID,
					ChannelID: &channelID,
					Title:     "No Access",
					Operation: "conversation",
				}
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, channelID, model.PermissionReadChannel).Return(false)
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "nonexistent conversation returns 404",
			userID:         testUserID,
			conversationID: "nonexistent",
			setup:          func(e *TestEnvironment) {},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "unauthenticated request returns 401",
			userID:         "",
			conversationID: "conv-unauth",
			setup:          func(e *TestEnvironment) {},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "threadless conversation accessible only by owner",
			userID:         testUserID,
			conversationID: "conv-threadless-owner",
			setup: func(e *TestEnvironment) {
				e.conversationStore.conversations["conv-threadless-owner"] = &store.Conversation{
					ID:        "conv-threadless-owner",
					UserID:    testUserID,
					BotID:     testBotUserID,
					ChannelID: nil,
					Title:     "Background Agent",
					Operation: "conversation",
				}
				e.conversationStore.turns["conv-threadless-owner"] = []store.Turn{
					{ID: "turn-1", ConversationID: "conv-threadless-owner", Role: "user", Content: textOnlyBlocks, Sequence: 1},
				}
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				var response ConversationResponse
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				assert.Equal(t, "conv-threadless-owner", response.ID)
				require.Len(t, response.Turns, 1)
			},
		},
		{
			name:           "threadless conversation rejected for non-owner",
			userID:         testOtherUserID,
			conversationID: "conv-threadless-reject",
			setup: func(e *TestEnvironment) {
				e.conversationStore.conversations["conv-threadless-reject"] = &store.Conversation{
					ID:        "conv-threadless-reject",
					UserID:    testUserID,
					BotID:     testBotUserID,
					ChannelID: nil,
					Title:     "Background Agent",
					Operation: "conversation",
				}
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "conversation with no turns returns empty turns array",
			userID:         testUserID,
			conversationID: "conv-no-turns",
			setup: func(e *TestEnvironment) {
				e.conversationStore.conversations["conv-no-turns"] = &store.Conversation{
					ID:        "conv-no-turns",
					UserID:    testUserID,
					BotID:     testBotUserID,
					ChannelID: &channelID,
					Title:     "Empty",
					Operation: "conversation",
				}
				e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				var response ConversationResponse
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				require.NotNil(t, response.Turns, "turns should not be null")
				assert.Len(t, response.Turns, 0, "turns should be empty array")
			},
		},
		{
			name:           "response JSON shape matches spec",
			userID:         testUserID,
			conversationID: "conv-json-shape",
			setup: func(e *TestEnvironment) {
				rootPostID := "root12345678901234567890"
				postID := "post12345678901234567890"
				e.conversationStore.conversations["conv-json-shape"] = &store.Conversation{
					ID:         "conv-json-shape",
					UserID:     testUserID,
					BotID:      testBotUserID,
					ChannelID:  &channelID,
					RootPostID: &rootPostID,
					Title:      "Shape Test",
					Operation:  "conversation",
				}
				e.conversationStore.turns["conv-json-shape"] = []store.Turn{
					{ID: "turn-1", ConversationID: "conv-json-shape", Role: "user", Content: textOnlyBlocks, Sequence: 1},
					{ID: "turn-2", ConversationID: "conv-json-shape", PostID: &postID, Role: "assistant", Content: textOnlyBlocks, TokensIn: 1500, TokensOut: 200, Sequence: 2},
				}
				e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				// Decode into raw map to verify exact JSON field names
				var raw map[string]json.RawMessage
				err := json.NewDecoder(resp.Body).Decode(&raw)
				require.NoError(t, err)

				// Conversation-level fields
				for _, key := range []string{"id", "user_id", "bot_id", "channel_id", "root_post_id", "title", "operation", "turns"} {
					_, ok := raw[key]
					assert.True(t, ok, "response should contain field %q", key)
				}

				// Parse turns
				var turns []map[string]json.RawMessage
				err = json.Unmarshal(raw["turns"], &turns)
				require.NoError(t, err)
				require.Len(t, turns, 2)

				// Turn-level fields
				for _, key := range []string{"id", "post_id", "role", "content", "tokens_in", "tokens_out", "sequence"} {
					_, ok := turns[1][key]
					assert.True(t, ok, "turn should contain field %q", key)
				}

				// Verify specific values through typed decode
				var response ConversationResponse
				bodyBytes, _ := json.Marshal(raw)
				err = json.Unmarshal(bodyBytes, &response)
				require.NoError(t, err)
				assert.Equal(t, "conv-json-shape", response.ID)
				assert.Equal(t, testUserID, response.UserID)
				assert.Equal(t, testBotUserID, response.BotID)
				require.NotNil(t, response.ChannelID)
				assert.Equal(t, channelID, *response.ChannelID)
				require.NotNil(t, response.RootPostID)
				assert.Equal(t, "root12345678901234567890", *response.RootPostID)
				assert.Equal(t, "Shape Test", response.Title)
				assert.Equal(t, "conversation", response.Operation)
				assert.Equal(t, int64(1500), response.Turns[1].TokensIn)
				assert.Equal(t, int64(200), response.Turns[1].TokensOut)
				assert.Equal(t, 2, response.Turns[1].Sequence)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			tt.setup(e)
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			request := httptest.NewRequest(http.MethodGet, "/conversations/"+tt.conversationID, nil)
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

// mustMarshalBlocks marshals content blocks to JSON and fails the test on error.
func mustMarshalBlocks(t *testing.T, blocks []conversation.ContentBlock) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(blocks)
	require.NoError(t, err)
	return data
}
