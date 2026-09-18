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

// Text stored in the seeded turns. The user-turn text is the value the
// assertions look for in the raw response body.
const (
	storedUserTurnText      = "stored original text"
	storedAssistantTurnText = "assistant reply text"
	currentUserPostMessage  = "current text"
	flaggedPostMessage      = "message carried by the post flagged deleted"
)

// Post IDs the seeded turns are anchored to.
const (
	anchoredUserPostID      = "userpost1234567890123456a"
	anchoredAssistantPostID = "botpost01234567890123456a"
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

func TestGetConversationTurnTextReflectsAnchoredPost(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	channelID := testChannelID

	tests := []struct {
		name           string
		userID         string
		conversationID string
		setup          func(t *testing.T, e *TestEnvironment)
		expectedStatus int
		validate       func(t *testing.T, body []byte)
	}{
		{
			name:           "anchored post message differs from stored user turn text",
			userID:         testOtherUserID,
			conversationID: "conv-anchor-changed",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedAnchoredConversation(t, e, "conv-anchor-changed")
				e.mockAPI.On("GetPost", anchoredUserPostID).Return(&model.Post{
					Id:        anchoredUserPostID,
					UserId:    testUserID,
					ChannelId: channelID,
					Message:   currentUserPostMessage,
				}, nil).Maybe()
				e.mockAPI.On("GetPost", anchoredAssistantPostID).Return(&model.Post{
					Id:        anchoredAssistantPostID,
					UserId:    testBotUserID,
					ChannelId: channelID,
					Message:   storedAssistantTurnText,
				}, nil).Maybe()
				allowAnyPostLookup(e.mockAPI)
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
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
				e.mockAPI.On("GetPost", anchoredUserPostID).Return(nil, &model.AppError{
					Id:         "app.post.get.app_error",
					StatusCode: http.StatusNotFound,
				}).Maybe()
				e.mockAPI.On("GetPost", anchoredAssistantPostID).Return(&model.Post{
					Id:        anchoredAssistantPostID,
					UserId:    testBotUserID,
					ChannelId: channelID,
					Message:   storedAssistantTurnText,
				}, nil).Maybe()
				allowAnyPostLookup(e.mockAPI)
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
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
				e.mockAPI.On("GetPost", anchoredUserPostID).Return(&model.Post{
					Id:        anchoredUserPostID,
					UserId:    testUserID,
					ChannelId: channelID,
					Message:   flaggedPostMessage,
					DeleteAt:  model.GetMillis(),
				}, nil).Maybe()
				e.mockAPI.On("GetPost", anchoredAssistantPostID).Return(&model.Post{
					Id:        anchoredAssistantPostID,
					UserId:    testBotUserID,
					ChannelId: channelID,
					Message:   storedAssistantTurnText,
				}, nil).Maybe()
				allowAnyPostLookup(e.mockAPI)
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
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
				e.mockAPI.On("GetPost", anchoredUserPostID).Return(&model.Post{
					Id:        anchoredUserPostID,
					UserId:    testUserID,
					ChannelId: channelID,
					Message:   storedUserTurnText,
				}, nil).Maybe()
				e.mockAPI.On("GetPost", anchoredAssistantPostID).Return(&model.Post{
					Id:        anchoredAssistantPostID,
					UserId:    testBotUserID,
					ChannelId: channelID,
					Message:   storedAssistantTurnText,
				}, nil).Maybe()
				allowAnyPostLookup(e.mockAPI)
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
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
				e.mockAPI.On("GetPost", anchoredUserPostID).Return(&model.Post{
					Id:        anchoredUserPostID,
					UserId:    testUserID,
					ChannelId: channelID,
					Message:   currentUserPostMessage,
				}, nil).Maybe()
				e.mockAPI.On("GetPost", anchoredAssistantPostID).Return(&model.Post{
					Id:        anchoredAssistantPostID,
					UserId:    testBotUserID,
					ChannelId: channelID,
					Message:   storedAssistantTurnText,
				}, nil).Maybe()
				allowAnyPostLookup(e.mockAPI)
				e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
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
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, body []byte) {
				assert.Equal(t, storedUserTurnText, turnTextByRole(t, body, "user"))
				assert.Equal(t, storedAssistantTurnText, turnTextByRole(t, body, "assistant"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			tt.setup(t, e)

			request := httptest.NewRequest(http.MethodGet, "/conversations/"+tt.conversationID, nil)
			request.Header.Add("Mattermost-User-ID", tt.userID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			resp := recorder.Result()
			require.Equal(t, tt.expectedStatus, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())

			if tt.validate != nil {
				tt.validate(t, body)
			}
		})
	}
}
