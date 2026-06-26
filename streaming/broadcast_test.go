// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/i18n"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// postupdate events carry the full assistant message, tool calls, reasoning and
// annotations. These payloads routinely exceed the 49077-byte UDP limit, so they
// must be sent with ReliableClusterSend=true; otherwise the server drops them
// between cluster nodes and the streamed response never reaches the webapp.
func TestPostUpdateEventsUseReliableClusterSend(t *testing.T) {
	const (
		postID      = "post-id"
		channelID   = "channel-id"
		botID       = "bot-id"
		requesterID = "requester-id"
	)

	newService := func(client *fakeStreamingClient) *MMPostStreamService {
		return NewMMPostStreamService(client, i18n.Init())
	}

	newClient := func() *fakeStreamingClient {
		return &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeOpen},
			},
		}
	}

	assertAllPostUpdatesReliable := func(t *testing.T, events []publishedEvent) {
		t.Helper()
		sawPostUpdate := false
		for _, ev := range events {
			if ev.event != "postupdate" {
				continue
			}
			sawPostUpdate = true
			require.NotNil(t, ev.broadcast, "postupdate broadcast must not be nil")
			require.True(t, ev.broadcast.ReliableClusterSend,
				"postupdate event %#v must set ReliableClusterSend so large/essential payloads are not dropped over UDP",
				ev.payload)
		}
		require.True(t, sawPostUpdate, "expected at least one postupdate event")
	}

	post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}

	tests := []struct {
		name   string
		act    func(service *MMPostStreamService)
		assert func(t *testing.T, events []publishedEvent)
	}{
		{
			name: "text, control, reasoning and annotation events are reliable",
			act: func(service *MMPostStreamService) {
				streamChannel := make(chan llm.TextStreamEvent, 5)
				streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeReasoning, Value: "thinking"}
				streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeReasoningEnd, Value: llm.ReasoningData{Text: "thinking"}}
				streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "hello"}
				streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeAnnotations, Value: []llm.Annotation{
					{Type: llm.AnnotationTypeURLCitation, URL: "https://example.com", Title: "Example", Index: 1},
				}}
				streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
				close(streamChannel)

				service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)
			},
			assert: func(t *testing.T, events []publishedEvent) {
				assertAllPostUpdatesReliable(t, events)
			},
		},
		{
			name: "tool call events are reliable for requester and channel broadcasts",
			act: func(service *MMPostStreamService) {
				service.broadcastToolCalls(post, []llm.ToolCall{
					{ID: "tc1", Name: "search", Status: llm.ToolCallStatusSuccess},
				}, requesterID)
			},
			assert: func(t *testing.T, events []publishedEvent) {
				// broadcastToolCalls emits two postupdate events: full data to the
				// requester and redacted data to the rest of the channel. Both must be
				// reliable.
				require.Len(t, events, 2)
				assertAllPostUpdatesReliable(t, events)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient()
			service := newService(client)

			tt.act(service)
			tt.assert(t, client.events)
		})
	}
}
