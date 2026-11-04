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

func TestStreamToPostMarksUserStop(t *testing.T) {
	const channelID = "channel-id"
	const postID = "post-id"

	tests := []struct {
		name        string
		drive       func(s *MMPostStreamService, cancelParent context.CancelFunc, stream chan<- llm.TextStreamEvent)
		wantStopped bool
	}{
		{
			name: "requester stops the stream",
			drive: func(s *MMPostStreamService, _ context.CancelFunc, _ chan<- llm.TextStreamEvent) {
				s.StopStreaming(postID)
			},
			wantStopped: true,
		},
		{
			// Regenerate hands the stream context to the upstream, which
			// errors and closes on the same cancel.
			name: "requester stops and the upstream errors and closes",
			drive: func(s *MMPostStreamService, _ context.CancelFunc, stream chan<- llm.TextStreamEvent) {
				s.StopStreaming(postID)
				stream <- llm.TextStreamEvent{Type: llm.EventTypeError, Value: context.Canceled}
				close(stream)
			},
			wantStopped: true,
		},
		{
			name: "requester stops and the upstream ends",
			drive: func(s *MMPostStreamService, _ context.CancelFunc, stream chan<- llm.TextStreamEvent) {
				s.StopStreaming(postID)
				stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
				close(stream)
			},
			wantStopped: true,
		},
		{
			name: "run finishes without a user stop",
			drive: func(s *MMPostStreamService, _ context.CancelFunc, _ chan<- llm.TextStreamEvent) {
				s.FinishStreaming(postID)
			},
		},
		{
			name: "parent context is canceled",
			drive: func(_ *MMPostStreamService, cancelParent context.CancelFunc, _ chan<- llm.TextStreamEvent) {
				cancelParent()
			},
		},
		{
			name: "a new run completes",
			drive: func(_ *MMPostStreamService, _ context.CancelFunc, stream chan<- llm.TextStreamEvent) {
				stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "answer"}
				stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeStreamingClient{channels: map[string]*model.Channel{channelID: {Id: channelID, Type: model.ChannelTypeOpen}}}
			service := NewMMPostStreamService(client, i18n.Init())

			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			ctx, err := service.GetStreamingContext(parent, postID)
			require.NoError(t, err)

			stream := make(chan llm.TextStreamEvent, 2)
			tc.drive(service, cancelParent, stream)

			post := &model.Post{Id: postID, ChannelId: channelID}
			post.AddProp(GenerationStoppedProp, true)
			service.StreamToPost(ctx, &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")

			require.NotEmpty(t, client.updatedPosts)
			final := client.updatedPosts[len(client.updatedPosts)-1]
			if tc.wantStopped {
				require.Equal(t, true, final.GetProp(GenerationStoppedProp))
				require.NotContains(t, final.Message, "Sorry!")
			} else {
				require.Nil(t, final.GetProp(GenerationStoppedProp))
			}
		})
	}
}

func TestStopStreamingKeepsRunUntilItsCleanup(t *testing.T) {
	const postID = "post-id"
	service := NewMMPostStreamService(&fakeStreamingClient{}, i18n.Init())

	_, err := service.GetStreamingContext(context.Background(), postID)
	require.NoError(t, err)
	service.StopStreaming(postID)

	// A regenerate clicked right after Stop must not start while the stopped
	// run is still finishing; its cleanup would cancel the new run.
	_, err = service.GetStreamingContext(context.Background(), postID)
	require.ErrorIs(t, err, ErrAlreadyStreamingToPost)

	service.FinishStreaming(postID)
	next, err := service.GetStreamingContext(context.Background(), postID)
	require.NoError(t, err)
	require.NoError(t, next.Err())
}
