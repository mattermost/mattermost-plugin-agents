// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinalizedAssistantTurnTextMatchesPostMessage pins the relationship
// between the text stored on the finalized assistant turn and the message of
// the post that turn is anchored to, across the stream shapes the production
// path produces.
func TestFinalizedAssistantTurnTextMatchesPostMessage(t *testing.T) {
	const (
		postID         = "assistant-post-id"
		channelID      = "channel-id"
		botID          = "bot-id"
		requesterID    = "requester-id"
		conversationID = "conv-id"
	)

	// citedMessage and its cleaned form drive the citation-cleanup case.
	const (
		citedMessage   = "Answer !!CITE1!! and more"
		citationMarker = "!!CITE1!!"
	)
	citationStart := strings.Index(citedMessage, citationMarker)
	cleanedCitedMessage := citedMessage[:citationStart] + citedMessage[citationStart+len(citationMarker):]

	tests := []struct {
		name   string
		events []llm.TextStreamEvent
	}{
		{
			name: "plain text answer",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Hello there"},
				{Type: llm.EventTypeEnd},
			},
		},
		{
			name: "text arriving in several chunks",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Hello "},
				{Type: llm.EventTypeText, Value: "there"},
				{Type: llm.EventTypeEnd},
			},
		},
		{
			name: "reasoning ahead of the answer",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeReasoning, Value: "thinking about it"},
				{Type: llm.EventTypeReasoningEnd, Value: llm.ReasoningData{Text: "thinking about it"}},
				{Type: llm.EventTypeText, Value: "Hello there"},
				{Type: llm.EventTypeEnd},
			},
		},
		{
			name: "stream produced nothing",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeEnd},
			},
		},
		{
			name: "stream fails after partial output",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Partial answer"},
				{Type: llm.EventTypeError, Value: assert.AnError},
			},
		},
		{
			name: "pending tool calls and no text",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "search", Status: llm.ToolCallStatusPending},
				}},
				{Type: llm.EventTypeEnd},
			},
		},
		{
			name: "tool round resolved before the final answer",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Let me look that up"},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "search", Status: llm.ToolCallStatusSuccess},
				}},
				{Type: llm.EventTypeText, Value: "Here is the answer"},
				{Type: llm.EventTypeEnd},
			},
		},
		{
			name: "citation markers removed from the answer",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: citedMessage},
				{Type: llm.EventTypeAnnotations, Value: map[string]any{
					"annotations": []llm.Annotation{{
						Type: "url_citation", URL: "https://example.com", Title: "Example",
					}},
					"cleanedMessage":  cleanedCitedMessage,
					"originalMessage": citedMessage,
					"removedTextRanges": []llm.TextRange{
						{Start: citationStart, End: citationStart + len(citationMarker)},
					},
				}},
				{Type: llm.EventTypeEnd},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := &fakeTurnStore{}
			client := &fakeStreamingClient{
				channels: map[string]*model.Channel{
					channelID: {Id: channelID, Type: model.ChannelTypeOpen},
				},
			}
			service := NewMMPostStreamService(client, i18n.Init())
			service.SetTurnStore(ts)

			post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
			post.AddProp(ConversationIDProp, conversationID)

			streamChannel := make(chan llm.TextStreamEvent, len(tt.events))
			for _, event := range tt.events {
				streamChannel <- event
			}
			close(streamChannel)

			service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)

			ts.mu.Lock()
			defer ts.mu.Unlock()
			turn := findStreamTurn(ts.turns, postID)
			require.NotNil(t, turn, "the stream must persist an assistant turn anchored to the post")

			blocks := parseContentBlocks(t, turn.Content)
			assert.Equal(t, post.Message, conversation.TextContent(blocks),
				"stored assistant turn text and the anchored post message are compared for equality when the conversation is read")
		})
	}
}
