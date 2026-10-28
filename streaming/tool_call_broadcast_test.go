// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// TestToolCallBroadcastTiming pins when the requester receives each tool_call
// batch: a batch awaiting a user decision only once its turn is stored, so a
// click on the card can resolve, and an auto-run batch while the run is still
// going, so the UI shows progress during execution.
func TestToolCallBroadcastTiming(t *testing.T) {
	const (
		postID         = "post-id"
		channelID      = "channel-id"
		botID          = "bot-id"
		requesterID    = "requester-id"
		conversationID = "conv-id"
	)

	type sentBatch struct {
		status    llm.ToolCallStatus
		persisted bool
	}

	needsApproval := llm.ToolCall{ID: "tc-approve", Name: "create_post", Status: llm.ToolCallStatusPending}
	autoRun := llm.ToolCall{ID: "tc-auto", Name: "search", Status: llm.ToolCallStatusPending, WouldAutoExecute: true}
	autoRunDone := autoRun
	autoRunDone.Status = llm.ToolCallStatusAutoApproved

	tests := []struct {
		name   string
		events []llm.TextStreamEvent
		want   []sentBatch
	}{
		{
			name: "auto-run round is sent while it runs",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{autoRun}},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{autoRunDone}},
				{Type: llm.EventTypeText, Value: "Found it."},
				{Type: llm.EventTypeEnd},
			},
			want: []sentBatch{
				{status: llm.ToolCallStatusPending, persisted: false},
				{status: llm.ToolCallStatusAutoApproved, persisted: false},
			},
		},
		{
			name: "batch awaiting approval is sent after its turn is stored",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{needsApproval}},
				{Type: llm.EventTypeEnd},
			},
			want: []sentBatch{{status: llm.ToolCallStatusPending, persisted: true}},
		},
		{
			name: "batch mixing auto-run and approval calls is sent after its turn is stored",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{autoRun, needsApproval}},
				{Type: llm.EventTypeEnd},
			},
			want: []sentBatch{{status: llm.ToolCallStatusPending, persisted: true}},
		},
		{
			name: "approval batch after an auto-run round",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{autoRun}},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{autoRunDone}},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{needsApproval}},
				{Type: llm.EventTypeEnd},
			},
			want: []sentBatch{
				{status: llm.ToolCallStatusPending, persisted: false},
				{status: llm.ToolCallStatusAutoApproved, persisted: false},
				{status: llm.ToolCallStatusPending, persisted: true},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ts := &fakeTurnStore{}
			client := &fakeStreamingClient{
				channels: map[string]*model.Channel{
					channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
				},
			}
			var sent []sentBatch
			client.onPublish = func(ev publishedEvent) {
				if ev.payload["control"] != "tool_call" || ev.broadcast == nil || ev.broadcast.UserId != requesterID {
					return
				}
				var calls []llm.ToolCall
				require.NoError(t, json.Unmarshal([]byte(ev.payload["tool_call"].(string)), &calls))
				ts.mu.Lock()
				persisted := findStreamTurn(ts.turns, postID) != nil
				ts.mu.Unlock()
				sent = append(sent, sentBatch{status: calls[0].Status, persisted: persisted})
			}
			service := NewMMPostStreamService(client, i18n.Init())
			service.SetTurnStore(ts)

			post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
			post.AddProp(ConversationIDProp, conversationID)

			stream := make(chan llm.TextStreamEvent, len(test.events))
			for _, ev := range test.events {
				stream <- ev
			}
			close(stream)

			service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: stream}, post, "en", requesterID)

			require.Equal(t, test.want, sent)
		})
	}
}
