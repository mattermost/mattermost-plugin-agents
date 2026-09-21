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
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
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
		{Type: conversation.BlockTypeToolUse, ID: "tc_01", Name: "get_weather", Input: toolUseInput, Status: conversation.StatusPending, Shared: new(false)},
	})
	unsharedToolResultBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeToolResult, ToolUseID: "tc_01", Content: "72F, sunny", Status: conversation.StatusSuccess, Shared: new(false)},
	})
	sharedToolBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: "Let me check the weather."},
		{Type: conversation.BlockTypeToolUse, ID: "tc_02", Name: "get_weather", Input: toolUseInput, Status: conversation.StatusSuccess, Shared: new(true)},
	})
	sharedToolResultBlocks := mustMarshalBlocks(t, []conversation.ContentBlock{
		{Type: conversation.BlockTypeToolResult, ToolUseID: "tc_02", Content: "72F, sunny", Status: conversation.StatusSuccess, Shared: new(true)},
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

// Text stored in the seeded turns and carried by the posts they are anchored
// to. The stored values are what the assertions look for in the response body.
const (
	storedUserTurnText      = "stored original text"
	storedAssistantTurnText = "assistant reply text"
	currentUserPostMessage  = "current text"
	flaggedPostMessage      = "message carried by the post flagged deleted"
	edgePostMessage         = "help me"
	edgeMentionedAgent      = "@agent "
	textlessTurnPostMessage = "the message the post now carries"
)

// IDs of the posts the seeded turns are anchored to, and of the files the edge
// conversation's turn carries.
const (
	anchoredUserPostID      = "userpost1234567890123456a"
	anchoredAssistantPostID = "botpost01234567890123456a"
	edgeAnchorPostID        = "edgepost1234567890123456a"
	edgeAnchorFileID        = "edgefile1234567890123456a"
	edgeAnchorImageID       = "edgeimg01234567890123456a"
	edgeConversationID      = "conv-edge"
)

// seedAnchoredConversation stores a channel-bound conversation owned by
// testUserID with a user turn and an assistant turn, both anchored to posts.
func seedAnchoredConversation(t *testing.T, e *TestEnvironment, conversationID string) {
	t.Helper()

	channelID := testChannelID
	userPostID := anchoredUserPostID
	assistantPostID := anchoredAssistantPostID

	e.conversationStore.conversations[conversationID] = &store.Conversation{
		ID:        conversationID,
		UserID:    testUserID,
		BotID:     testBotUserID,
		ChannelID: &channelID,
		Title:     "Anchored Conversation",
		Operation: "conversation",
	}
	e.conversationStore.turns[conversationID] = []store.Turn{
		{
			ID:             "turn-user-1",
			ConversationID: conversationID,
			PostID:         &userPostID,
			Role:           "user",
			Content: mustMarshalBlocks(t, []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: storedUserTurnText},
			}),
			Sequence: 1,
		},
		{
			ID:             "turn-assistant-1",
			ConversationID: conversationID,
			PostID:         &assistantPostID,
			Role:           "assistant",
			Content: mustMarshalBlocks(t, []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: storedAssistantTurnText},
			}),
			Sequence: 2,
		},
	}
}

// seedUnanchoredConversation stores a channel-bound conversation owned by
// testUserID whose turns carry no post anchor.
func seedUnanchoredConversation(t *testing.T, e *TestEnvironment, conversationID string) {
	t.Helper()

	channelID := testChannelID

	e.conversationStore.conversations[conversationID] = &store.Conversation{
		ID:        conversationID,
		UserID:    testUserID,
		BotID:     testBotUserID,
		ChannelID: &channelID,
		Title:     "Unanchored Conversation",
		Operation: "conversation",
	}
	e.conversationStore.turns[conversationID] = []store.Turn{
		{
			ID:             "turn-user-1",
			ConversationID: conversationID,
			Role:           "user",
			Content: mustMarshalBlocks(t, []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: storedUserTurnText},
			}),
			Sequence: 1,
		},
		{
			ID:             "turn-assistant-1",
			ConversationID: conversationID,
			Role:           "assistant",
			Content: mustMarshalBlocks(t, []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: storedAssistantTurnText},
			}),
			Sequence: 2,
		},
	}
}

// armAnchoredConversation arms the post lookups and the channel permission the
// conversation seeded by seedAnchoredConversation needs. A nil userPost reports
// the post the user turn is anchored to as not found; the post the assistant
// turn is anchored to always carries the message that turn was stored with.
func armAnchoredConversation(e *TestEnvironment, requesterID string, userPost *model.Post) {
	if userPost == nil {
		e.mockAPI.On("GetPost", anchoredUserPostID).Return(nil, &model.AppError{
			Id:         "app.post.get.app_error",
			StatusCode: http.StatusNotFound,
		}).Maybe()
	} else {
		e.mockAPI.On("GetPost", anchoredUserPostID).Return(userPost, nil).Maybe()
	}
	e.mockAPI.On("GetPost", anchoredAssistantPostID).Return(&model.Post{
		Id:        anchoredAssistantPostID,
		UserId:    testBotUserID,
		ChannelId: testChannelID,
		Message:   storedAssistantTurnText,
	}, nil).Maybe()
	allowAnyPostLookup(e.mockAPI)
	e.mockAPI.On("HasPermissionToChannel", requesterID, testChannelID, model.PermissionReadChannel).Return(true)
}

// setupEdgeTurn stores a channel-bound conversation owned by testUserID whose
// single user turn holds blocks and is anchored to edgeAnchorPostID, and arms
// the lookup of that post. A nil anchored post is reported as not found.
func setupEdgeTurn(t *testing.T, e *TestEnvironment, blocks []conversation.ContentBlock, anchored *model.Post) {
	t.Helper()

	channelID := testChannelID
	postID := edgeAnchorPostID

	e.conversationStore.conversations[edgeConversationID] = &store.Conversation{
		ID:        edgeConversationID,
		UserID:    testUserID,
		BotID:     testBotUserID,
		ChannelID: &channelID,
		Title:     "Edge Conversation",
		Operation: "conversation",
	}
	e.conversationStore.turns[edgeConversationID] = []store.Turn{{
		ID:             "turn-edge-1",
		ConversationID: edgeConversationID,
		PostID:         &postID,
		Role:           "user",
		Content:        mustMarshalBlocks(t, blocks),
		Sequence:       1,
	}}

	if anchored == nil {
		e.mockAPI.On("GetPost", edgeAnchorPostID).Return(nil, &model.AppError{
			Id:         "app.post.get.app_error",
			StatusCode: http.StatusNotFound,
		}).Maybe()
	} else {
		e.mockAPI.On("GetPost", edgeAnchorPostID).Return(anchored, nil).Maybe()
	}
	allowAnyPostLookup(e.mockAPI)
	e.mockAPI.On("HasPermissionToChannel", testOtherUserID, channelID, model.PermissionReadChannel).Return(true)
}

// edgeAnchorPost builds the post the edge conversation's turn is anchored to,
// carrying message and living in the conversation's channel.
func edgeAnchorPost(message string) *model.Post {
	return &model.Post{
		Id:        edgeAnchorPostID,
		UserId:    testUserID,
		ChannelId: testChannelID,
		Message:   message,
	}
}

// allowAnyPostLookup registers a fallback post lookup reporting the post as
// not found. Testify matches the earliest registered expectation, so this only
// applies to IDs a test did not register explicitly.
func allowAnyPostLookup(mockAPI *plugintest.API) {
	mockAPI.On("GetPost", mock.Anything).Return(nil, &model.AppError{
		Id:         "app.post.get.app_error",
		StatusCode: http.StatusNotFound,
	}).Maybe()
}

// turnTextByRole decodes the response and returns the concatenated text of
// every text block on the first turn with the given role.
func turnTextByRole(t *testing.T, body []byte, role string) string {
	t.Helper()

	var response ConversationResponse
	require.NoError(t, json.Unmarshal(body, &response))

	for _, turn := range response.Turns {
		if turn.Role != role {
			continue
		}
		var blocks []conversation.ContentBlock
		require.NoError(t, json.Unmarshal(turn.Content, &blocks))
		text := ""
		for _, block := range blocks {
			if block.Type == conversation.BlockTypeText {
				text += block.Text
			}
		}
		return text
	}
	return ""
}

// turnBlocks decodes the content blocks of the single turn in the response.
func turnBlocks(t *testing.T, body []byte) []conversation.ContentBlock {
	t.Helper()

	var response ConversationResponse
	require.NoError(t, json.Unmarshal(body, &response))
	require.Len(t, response.Turns, 1)

	var blocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(response.Turns[0].Content, &blocks))
	return blocks
}

// blocksOfType returns the blocks with the given type.
func blocksOfType(blocks []conversation.ContentBlock, blockType string) []conversation.ContentBlock {
	var result []conversation.ContentBlock
	for _, block := range blocks {
		if block.Type == blockType {
			result = append(result, block)
		}
	}
	return result
}

// getConversationBody issues GET /conversations/{path} as userID and returns
// the body of the response.
func getConversationBody(t *testing.T, e *TestEnvironment, path, userID string) []byte {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/conversations/"+path, nil)
	request.Header.Add("Mattermost-User-ID", userID)
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)

	resp := recorder.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return body
}

// TestGetConversationTurnTextReflectsAnchoredPost covers the text a turn is
// served with: for a request from someone other than the conversation owner,
// the text of a turn anchored to a post is the message that post currently
// carries in the conversation's channel.
func TestGetConversationTurnTextReflectsAnchoredPost(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	// rereadAnchorPost is the post the re-read case returns from its lookup;
	// its second request runs after the message has been changed.
	rereadAnchorPost := edgeAnchorPost(edgePostMessage)

	tests := []struct {
		name           string
		userID         string
		conversationID string
		// requestPath, when set, replaces the conversation ID in the request
		// path with an alternate spelling of the same ID.
		requestPath string
		setup       func(t *testing.T, e *TestEnvironment)
		validate    func(t *testing.T, body []byte)
		// secondRequest, when set, re-arms the anchored post lookup and the
		// case issues the same request again, validated by validateSecond.
		secondRequest  func(e *TestEnvironment)
		validateSecond func(t *testing.T, body []byte)
	}{
		{
			name:           "anchored post message differs from stored user turn text",
			userID:         testOtherUserID,
			conversationID: "conv-anchor-changed",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedAnchoredConversation(t, e, "conv-anchor-changed")
				armAnchoredConversation(e, testOtherUserID, &model.Post{
					Id:        anchoredUserPostID,
					UserId:    testUserID,
					ChannelId: testChannelID,
					Message:   currentUserPostMessage,
				})
			},
			validate: func(t *testing.T, body []byte) {
				assert.NotContains(t, string(body), storedUserTurnText,
					"user turn text must match the current content of the post it is anchored to")
			},
		},
		{
			name:           "anchored post lookup reports not found",
			userID:         testOtherUserID,
			conversationID: "conv-anchor-missing",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedAnchoredConversation(t, e, "conv-anchor-missing")
				armAnchoredConversation(e, testOtherUserID, nil)
			},
			validate: func(t *testing.T, body []byte) {
				assert.NotContains(t, string(body), storedUserTurnText,
					"user turn text requires a retrievable anchored post")
			},
		},
		{
			name:           "anchored post is flagged deleted",
			userID:         testOtherUserID,
			conversationID: "conv-anchor-deleted",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedAnchoredConversation(t, e, "conv-anchor-deleted")
				armAnchoredConversation(e, testOtherUserID, &model.Post{
					Id:        anchoredUserPostID,
					UserId:    testUserID,
					ChannelId: testChannelID,
					Message:   flaggedPostMessage,
					DeleteAt:  model.GetMillis(),
				})
			},
			validate: func(t *testing.T, body []byte) {
				assert.NotContains(t, string(body), storedUserTurnText,
					"user turn text requires a live anchored post")
				assert.NotContains(t, string(body), flaggedPostMessage,
					"a post flagged deleted supplies no text to the response")
			},
		},
		{
			name:           "anchored post message equals stored user turn text",
			userID:         testOtherUserID,
			conversationID: "conv-anchor-match",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedAnchoredConversation(t, e, "conv-anchor-match")
				armAnchoredConversation(e, testOtherUserID, &model.Post{
					Id:        anchoredUserPostID,
					UserId:    testUserID,
					ChannelId: testChannelID,
					Message:   storedUserTurnText,
				})
			},
			validate: func(t *testing.T, body []byte) {
				assert.Contains(t, string(body), storedUserTurnText,
					"user turn text is returned when it matches the anchored post")
				assert.Equal(t, storedUserTurnText, turnTextByRole(t, body, "user"))
				assert.Equal(t, storedAssistantTurnText, turnTextByRole(t, body, "assistant"),
					"assistant turn text is returned alongside the user turn")
			},
		},
		{
			name:           "owner request succeeds when anchored post message differs",
			userID:         testUserID,
			conversationID: "conv-anchor-owner",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedAnchoredConversation(t, e, "conv-anchor-owner")
				armAnchoredConversation(e, testUserID, &model.Post{
					Id:        anchoredUserPostID,
					UserId:    testUserID,
					ChannelId: testChannelID,
					Message:   currentUserPostMessage,
				})
			},
			validate: func(t *testing.T, body []byte) {
				var response ConversationResponse
				require.NoError(t, json.Unmarshal(body, &response))
				assert.Equal(t, "conv-anchor-owner", response.ID)
				assert.Len(t, response.Turns, 2)
			},
		},
		{
			name:           "turns without a post anchor are returned as stored",
			userID:         testOtherUserID,
			conversationID: "conv-no-anchor",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedUnanchoredConversation(t, e, "conv-no-anchor")
				allowAnyPostLookup(e.mockAPI)
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, testChannelID, model.PermissionReadChannel).Return(true)
			},
			validate: func(t *testing.T, body []byte) {
				assert.Equal(t, storedUserTurnText, turnTextByRole(t, body, "user"))
				assert.Equal(t, storedAssistantTurnText, turnTextByRole(t, body, "assistant"))
			},
		},
		{
			name:           "stored text carries the agent mention the plugin prepends",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeText, Text: edgeMentionedAgent + edgePostMessage},
				}, edgeAnchorPost(edgePostMessage))
			},
			validate: func(t *testing.T, body []byte) {
				assert.Equal(t, edgePostMessage, conversation.TextContent(turnBlocks(t, body)),
					"the text served for the turn is the message a channel member can read on the post itself")
			},
		},
		{
			name:           "attachments outlive the text of a turn whose post changed",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeText, Text: "stale question"},
					{Type: conversation.BlockTypeImage, FileID: edgeAnchorImageID, Filename: "shot.png", MimeType: "image/png"},
					{Type: conversation.BlockTypeFile, FileID: edgeAnchorFileID, Filename: "notes.txt", MimeType: "text/plain"},
					{Type: conversation.BlockTypeToolUse, ID: "tc1", Name: "search", Shared: new(true), Input: json.RawMessage(`{"q":"x"}`)},
				}, edgeAnchorPost(edgePostMessage))
			},
			validate: func(t *testing.T, body []byte) {
				blocks := turnBlocks(t, body)
				assert.NotContains(t, conversation.TextContent(blocks), "stale question")

				images := blocksOfType(blocks, conversation.BlockTypeImage)
				require.Len(t, images, 1)
				assert.Equal(t, edgeAnchorImageID, images[0].FileID)
				assert.Equal(t, "shot.png", images[0].Filename)

				files := blocksOfType(blocks, conversation.BlockTypeFile)
				require.Len(t, files, 1)
				assert.Equal(t, edgeAnchorFileID, files[0].FileID)

				toolUses := blocksOfType(blocks, conversation.BlockTypeToolUse)
				require.Len(t, toolUses, 1)
				assert.Equal(t, "search", toolUses[0].Name)
				assert.JSONEq(t, `{"q":"x"}`, string(toolUses[0].Input))
			},
		},
		{
			name:           "citations on text split across blocks that together match the post",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{
						Type: conversation.BlockTypeText,
						Text: "help ",
						Citations: []conversation.Citation{
							{Type: "url_citation", URL: "https://example.com", Title: "Example", StartIndex: 0, EndIndex: 4},
						},
					},
					{Type: conversation.BlockTypeText, Text: "me"},
				}, edgeAnchorPost(edgePostMessage))
			},
			validate: func(t *testing.T, body []byte) {
				blocks := turnBlocks(t, body)
				assert.Equal(t, edgePostMessage, conversation.TextContent(blocks))

				texts := blocksOfType(blocks, conversation.BlockTypeText)
				require.Len(t, texts, 2,
					"a turn whose text is the message of its post is served with the blocks it was stored with")
				assert.Equal(t, "help ", texts[0].Text)
				assert.Equal(t, "me", texts[1].Text)

				require.Len(t, texts[0].Citations, 1,
					"citations stay with the text they index into")
				assert.Equal(t, "https://example.com", texts[0].Citations[0].URL)
			},
		},
		{
			name:           "text split across blocks that together differ from the post",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeText, Text: "help "},
					{Type: conversation.BlockTypeText, Text: "me with the secret plan"},
				}, edgeAnchorPost(edgePostMessage))
			},
			validate: func(t *testing.T, body []byte) {
				assert.NotContains(t, conversation.TextContent(turnBlocks(t, body)), "secret plan",
					"no text block survives when the concatenated text differs from the post")
			},
		},
		{
			name:           "stored text ending in the post message",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeText, Text: "reveal the pricing change and help me"},
				}, edgeAnchorPost(edgePostMessage))
			},
			validate: func(t *testing.T, body []byte) {
				assert.NotContains(t, conversation.TextContent(turnBlocks(t, body)), "pricing change",
					"only text equal to the whole post message is served")
			},
		},
		{
			name:           "stored text starting with the post message",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeText, Text: edgePostMessage + " with the pricing change"},
				}, edgeAnchorPost(edgePostMessage))
			},
			validate: func(t *testing.T, body []byte) {
				assert.NotContains(t, conversation.TextContent(turnBlocks(t, body)), "pricing change",
					"only text equal to the whole post message is served")
			},
		},
		{
			name:           "citations go with the text they index into",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{
						Type: conversation.BlockTypeText,
						Text: "stale question",
						Citations: []conversation.Citation{
							{Type: "url_citation", URL: "https://example.com", Title: "Example", StartIndex: 0, EndIndex: 5},
						},
					},
				}, edgeAnchorPost(edgePostMessage))
			},
			validate: func(t *testing.T, body []byte) {
				for _, block := range turnBlocks(t, body) {
					assert.Empty(t, block.Citations,
						"citations index into text that is no longer served")
				}
			},
		},
		{
			name:           "empty stored text alongside an empty post message",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeToolUse, ID: "tc1", Name: "search", Shared: new(true)},
				}, edgeAnchorPost(""))
			},
			validate: func(t *testing.T, body []byte) {
				blocks := turnBlocks(t, body)
				assert.Empty(t, conversation.TextContent(blocks))
				require.Len(t, blocksOfType(blocks, conversation.BlockTypeToolUse), 1)
			},
		},
		{
			name:           "anchor post living outside the conversation channel",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeText, Text: "shared question"},
				}, &model.Post{
					Id:        edgeAnchorPostID,
					UserId:    testUserID,
					ChannelId: "otherchan123456789012345a",
					Message:   "confidential note from another channel",
				})
			},
			validate: func(t *testing.T, body []byte) {
				assert.NotContains(t, conversation.TextContent(turnBlocks(t, body)), "confidential note",
					"a post outside the conversation channel never supplies text to the response")
			},
		},
		{
			name:           "conversation id spelled percent-encoded in the path",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			requestPath:    "conv%2Dedge",
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeText, Text: "stale question"},
				}, edgeAnchorPost(edgePostMessage))
			},
			validate: func(t *testing.T, body []byte) {
				assert.NotContains(t, conversation.TextContent(turnBlocks(t, body)), "stale question")
			},
		},
		{
			name:           "stored text holding a bidi control character equal to the post",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeText, Text: "help\u202eme"},
				}, edgeAnchorPost("help\u202eme"))
			},
			validate: func(t *testing.T, body []byte) {
				assert.Equal(t, "help\u202eme", conversation.TextContent(turnBlocks(t, body)),
					"the text served is the text compared against the post")
			},
		},
		{
			name:           "a later request resolves the anchored post again",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				rereadAnchorPost.Message = edgePostMessage
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeText, Text: edgePostMessage},
				}, rereadAnchorPost)
			},
			validate: func(t *testing.T, body []byte) {
				require.Equal(t, edgePostMessage, conversation.TextContent(turnBlocks(t, body)))
			},
			secondRequest: func(e *TestEnvironment) {
				rereadAnchorPost.Message = "a different question"
			},
			validateSecond: func(t *testing.T, body []byte) {
				assert.NotContains(t, conversation.TextContent(turnBlocks(t, body)), edgePostMessage,
					"a later request resolves the anchored post again rather than reusing the earlier read")
			},
		},
		{
			name:           "anchored turn holding no text is not given the post message",
			userID:         testOtherUserID,
			conversationID: edgeConversationID,
			setup: func(t *testing.T, e *TestEnvironment) {
				setupEdgeTurn(t, e, []conversation.ContentBlock{
					{Type: conversation.BlockTypeToolUse, ID: "tc1", Name: "search", Shared: new(true)},
				}, edgeAnchorPost(textlessTurnPostMessage))
			},
			validate: func(t *testing.T, body []byte) {
				blocks := turnBlocks(t, body)
				assert.Empty(t, blocksOfType(blocks, conversation.BlockTypeText),
					"a turn stored without a text block is served without one")
				assert.NotContains(t, string(body), textlessTurnPostMessage)
				require.Len(t, blocksOfType(blocks, conversation.BlockTypeToolUse), 1,
					"the blocks the turn does hold are carried over")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			tt.setup(t, e)

			path := tt.conversationID
			if tt.requestPath != "" {
				path = tt.requestPath
			}

			tt.validate(t, getConversationBody(t, e, path, tt.userID))

			if tt.secondRequest != nil {
				tt.secondRequest(e)
				tt.validateSecond(t, getConversationBody(t, e, path, tt.userID))
			}
		})
	}
}
