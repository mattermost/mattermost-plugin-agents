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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Post ID and message used by the edge-case conversation.
const (
	edgeAnchorPostID   = "edgepost1234567890123456a"
	edgeAnchorFileID   = "edgefile1234567890123456a"
	edgeAnchorImageID  = "edgeimg01234567890123456a"
	edgePostMessage    = "help me"
	edgeMentionedAgent = "@agent "
)

// seedEdgeConversation stores a channel-bound conversation owned by testUserID
// whose single user turn is anchored to edgeAnchorPostID.
func seedEdgeConversation(e *TestEnvironment, conversationID, title string, blocks []conversation.ContentBlock) {
	channelID := testChannelID
	postID := edgeAnchorPostID

	content, _ := json.Marshal(blocks)
	e.conversationStore.conversations[conversationID] = &store.Conversation{
		ID:        conversationID,
		UserID:    testUserID,
		BotID:     testBotUserID,
		ChannelID: &channelID,
		Title:     title,
		Operation: "conversation",
	}
	e.conversationStore.turns[conversationID] = []store.Turn{{
		ID:             "turn-edge-1",
		ConversationID: conversationID,
		PostID:         &postID,
		Role:           "user",
		Content:        content,
		Sequence:       1,
	}}
}

// turnBlocks decodes the content blocks of the first turn in the response.
func turnBlocks(t *testing.T, body []byte) []conversation.ContentBlock {
	t.Helper()

	var response ConversationResponse
	require.NoError(t, json.Unmarshal(body, &response))
	require.Len(t, response.Turns, 1)

	var blocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(response.Turns[0].Content, &blocks))
	return blocks
}

// blocksOfType returns the blocks of the response turn with the given type.
func blocksOfType(blocks []conversation.ContentBlock, blockType string) []conversation.ContentBlock {
	var result []conversation.ContentBlock
	for _, block := range blocks {
		if block.Type == blockType {
			result = append(result, block)
		}
	}
	return result
}

// getEdgeConversation issues the request a non-owning channel member makes,
// with conversationID placed in the path exactly as given.
func getEdgeConversation(t *testing.T, e *TestEnvironment, conversationID string) []byte {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/conversations/"+conversationID, nil)
	request.Header.Add("Mattermost-User-ID", testOtherUserID)
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)

	resp := recorder.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return body
}

func TestGetConversationAnchoredTurnShapes(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name string
		// stored is the turn content as the write path persists it.
		stored []conversation.ContentBlock
		// postMessage is the current message of the anchored post; an empty
		// anchorMissing flag means the post is readable.
		postMessage   string
		anchorMissing bool
		// postChannelID, when set, places the anchored post in another channel.
		postChannelID string
		// requestPath, when set, replaces the conversation ID in the request
		// path with an alternate spelling of the same ID.
		requestPath string
		validate    func(t *testing.T, blocks []conversation.ContentBlock)
	}{
		{
			name: "stored text carries the agent mention the plugin prepends",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: edgeMentionedAgent + edgePostMessage},
			},
			postMessage: edgePostMessage,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.Equal(t, edgePostMessage, conversation.TextContent(blocks),
					"the text served for the turn is the message a channel member can read on the post itself")
			},
		},
		{
			name: "attachments outlive the text of a turn whose post changed",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: "stale question"},
				{Type: conversation.BlockTypeImage, FileID: edgeAnchorImageID, Filename: "shot.png", MimeType: "image/png"},
				{Type: conversation.BlockTypeFile, FileID: edgeAnchorFileID, Filename: "notes.txt", MimeType: "text/plain"},
				{Type: conversation.BlockTypeToolUse, ID: "tc1", Name: "search", Shared: new(true), Input: json.RawMessage(`{"q":"x"}`)},
			},
			postMessage: edgePostMessage,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
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
			name: "text split across blocks that together match the post",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: "help "},
				{Type: conversation.BlockTypeText, Text: "me"},
			},
			postMessage: edgePostMessage,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.Equal(t, edgePostMessage, conversation.TextContent(blocks))
			},
		},
		{
			name: "citations on text split across blocks that together match the post",
			stored: []conversation.ContentBlock{
				{
					Type: conversation.BlockTypeText,
					Text: "help ",
					Citations: []conversation.Citation{
						{Type: "url_citation", URL: "https://example.com", Title: "Example", StartIndex: 0, EndIndex: 4},
					},
				},
				{Type: conversation.BlockTypeText, Text: "me"},
			},
			postMessage: edgePostMessage,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
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
			name: "text split across blocks that together differ from the post",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: "help "},
				{Type: conversation.BlockTypeText, Text: "me with the secret plan"},
			},
			postMessage: edgePostMessage,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.NotContains(t, conversation.TextContent(blocks), "secret plan",
					"no text block survives when the concatenated text differs from the post")
			},
		},
		{
			name: "stored text ending in the post message",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: "reveal the pricing change and help me"},
			},
			postMessage: edgePostMessage,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.NotContains(t, conversation.TextContent(blocks), "pricing change",
					"only text equal to the whole post message is served")
			},
		},
		{
			name: "stored text starting with the post message",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: edgePostMessage + " with the pricing change"},
			},
			postMessage: edgePostMessage,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.NotContains(t, conversation.TextContent(blocks), "pricing change",
					"only text equal to the whole post message is served")
			},
		},
		{
			name: "citations go with the text they index into",
			stored: []conversation.ContentBlock{
				{
					Type: conversation.BlockTypeText,
					Text: "stale question",
					Citations: []conversation.Citation{
						{Type: "url_citation", URL: "https://example.com", Title: "Example", StartIndex: 0, EndIndex: 5},
					},
				},
			},
			postMessage: edgePostMessage,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				for _, block := range blocks {
					assert.Empty(t, block.Citations,
						"citations index into text that is no longer served")
				}
			},
		},
		{
			name: "empty stored text alongside an empty post message",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeToolUse, ID: "tc1", Name: "search", Shared: new(true)},
			},
			postMessage: "",
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.Empty(t, conversation.TextContent(blocks))
				require.Len(t, blocksOfType(blocks, conversation.BlockTypeToolUse), 1)
			},
		},
		{
			name: "empty stored text alongside a post that has a message",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeToolUse, ID: "tc1", Name: "search", Shared: new(true)},
			},
			postMessage: edgePostMessage,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.Empty(t, conversation.TextContent(blocks),
					"a turn holding no text of its own keeps none")
				assert.NotContains(t, conversation.TextContent(blocks), edgePostMessage)
				require.Len(t, blocksOfType(blocks, conversation.BlockTypeToolUse), 1)
			},
		},
		{
			name: "anchor post id that resolves to no post",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: "stale question"},
			},
			anchorMissing: true,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.NotContains(t, conversation.TextContent(blocks), "stale question")
			},
		},
		{
			name: "anchor post living outside the conversation channel",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: "shared question"},
			},
			postChannelID: "otherchan123456789012345a",
			postMessage:   "confidential note from another channel",
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.NotContains(t, conversation.TextContent(blocks), "confidential note",
					"a post outside the conversation channel never supplies text to the response")
			},
		},
		{
			name: "conversation id spelled percent-encoded in the path",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: "stale question"},
			},
			postMessage: edgePostMessage,
			requestPath: "conv%2Dedge",
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.NotContains(t, conversation.TextContent(blocks), "stale question")
			},
		},
		{
			name: "stored text holding a bidi control character equal to the post",
			stored: []conversation.ContentBlock{
				{Type: conversation.BlockTypeText, Text: "help\u202eme"},
			},
			postMessage: "help\u202eme",
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.Equal(t, "help\u202eme", conversation.TextContent(blocks),
					"the text served is the text compared against the post")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			conversationID := "conv-edge"
			seedEdgeConversation(e, conversationID, "Edge Conversation", tt.stored)

			if tt.anchorMissing {
				e.mockAPI.On("GetPost", edgeAnchorPostID).Return(nil, &model.AppError{
					Id:         "app.post.get.app_error",
					StatusCode: http.StatusNotFound,
				}).Maybe()
			} else {
				postChannelID := testChannelID
				if tt.postChannelID != "" {
					postChannelID = tt.postChannelID
				}
				e.mockAPI.On("GetPost", edgeAnchorPostID).Return(&model.Post{
					Id:        edgeAnchorPostID,
					UserId:    testUserID,
					ChannelId: postChannelID,
					Message:   tt.postMessage,
				}, nil).Maybe()
			}
			allowAnyPostLookup(e.mockAPI)
			e.mockAPI.On("HasPermissionToChannel", testOtherUserID, testChannelID, model.PermissionReadChannel).Return(true)

			requestPath := conversationID
			if tt.requestPath != "" {
				requestPath = tt.requestPath
			}

			tt.validate(t, turnBlocks(t, getEdgeConversation(t, e, requestPath)))
		})
	}
}

// TestGetConversationReadsAnchoredPostPerRequest covers a post edited between
// two reads of the same conversation: each request answers from the state of
// the post at the time it runs.
func TestGetConversationReadsAnchoredPostPerRequest(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	conversationID := "conv-edge-reread"
	seedEdgeConversation(e, conversationID, "Edge Conversation", []conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: edgePostMessage},
	})

	anchored := &model.Post{
		Id:        edgeAnchorPostID,
		UserId:    testUserID,
		ChannelId: testChannelID,
		Message:   edgePostMessage,
	}
	e.mockAPI.On("GetPost", edgeAnchorPostID).Return(anchored, nil).Maybe()
	allowAnyPostLookup(e.mockAPI)
	e.mockAPI.On("HasPermissionToChannel", testOtherUserID, testChannelID, model.PermissionReadChannel).Return(true)

	first := turnBlocks(t, getEdgeConversation(t, e, conversationID))
	require.Equal(t, edgePostMessage, conversation.TextContent(first))

	anchored.Message = "a different question"

	second := turnBlocks(t, getEdgeConversation(t, e, conversationID))
	assert.NotContains(t, conversation.TextContent(second), edgePostMessage,
		"a later request resolves the anchored post again rather than reusing the earlier read")
}
