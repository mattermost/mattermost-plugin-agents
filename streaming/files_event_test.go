// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// TestStreamToPostFilesEvent covers the EventTypeFiles handling: the
// conversations layer has already linked the FileInfo rows, so the streaming
// layer only merges the IDs into the post so the final UpdatePost persists
// the attachment list, and a files-only reply counts as a valid response.
func TestStreamToPostFilesEvent(t *testing.T) {
	const (
		postID         = "post-id"
		channelID      = "channel-id"
		botID          = "bot-id"
		requesterID    = "requester-id"
		conversationID = "conv-id"
	)

	tests := []struct {
		name           string
		initialFileIDs []string
		events         []llm.TextStreamEvent
		wantFileIDs    []string
		wantFallback   bool
	}{
		{
			name: "files event merges IDs onto the post and the final update carries them",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Here is the report."},
				{Type: llm.EventTypeFiles, Value: []string{"file-1", "file-2"}},
				{Type: llm.EventTypeEnd},
			},
			wantFileIDs: []string{"file-1", "file-2"},
		},
		{
			name:           "duplicate IDs are not doubled",
			initialFileIDs: []string{"file-1"},
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Updated."},
				{Type: llm.EventTypeFiles, Value: []string{"file-1", "file-2"}},
				{Type: llm.EventTypeEnd},
			},
			wantFileIDs: []string{"file-1", "file-2"},
		},
		{
			name: "files-only stream is a valid response and skips the empty fallback",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeFiles, Value: []string{"file-1"}},
				{Type: llm.EventTypeEnd},
			},
			wantFileIDs: []string{"file-1"},
		},
		{
			name: "stream with neither text nor files still produces the empty fallback",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeEnd},
			},
			wantFallback: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := &fakeTurnStore{}
			client := &fakeStreamingClient{
				channels: map[string]*model.Channel{
					channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
				},
			}
			service := NewMMPostStreamService(client, i18n.Init())
			service.SetTurnStore(ts)

			post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID, FileIds: tt.initialFileIDs}
			post.AddProp(ConversationIDProp, conversationID)

			streamChannel := make(chan llm.TextStreamEvent, len(tt.events))
			for _, e := range tt.events {
				streamChannel <- e
			}
			close(streamChannel)

			service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)

			require.Equal(t, model.StringArray(tt.wantFileIDs), post.FileIds)

			require.NotEmpty(t, client.updatedPosts)
			finalUpdate := client.updatedPosts[len(client.updatedPosts)-1]
			require.Equal(t, model.StringArray(tt.wantFileIDs), finalUpdate.FileIds)

			if tt.wantFallback {
				require.Contains(t, finalUpdate.Message, "did not return a result")
			} else {
				require.NotContains(t, finalUpdate.Message, "did not return a result")
			}
		})
	}
}
