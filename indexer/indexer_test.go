// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldIndexPost(t *testing.T) {
	tests := []struct {
		name     string
		post     *model.Post
		channel  *model.Channel
		expected bool
	}{
		{
			name: "should index regular post",
			post: &model.Post{
				Id:       "post1",
				Message:  "Hello world",
				Type:     model.PostTypeDefault,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			expected: true,
		},
		{
			name: "should not index deleted post",
			post: &model.Post{
				Id:       "post2",
				Message:  "Deleted message",
				Type:     model.PostTypeDefault,
				UserId:   "user1",
				DeleteAt: 123456789, // Non-zero DeleteAt means deleted
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			expected: false,
		},
		{
			name: "should not index empty message",
			post: &model.Post{
				Id:       "post3",
				Message:  "",
				Type:     model.PostTypeDefault,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			expected: false,
		},
		{
			name: "should not index non-default post type",
			post: &model.Post{
				Id:       "post4",
				Message:  "System message",
				Type:     model.PostTypeJoinChannel,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			expected: false,
		},
	}

	// Create indexer with empty bots
	mockBots := &bots.MMBots{}
	indexer := New(nil, nil, mockBots, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := indexer.shouldIndexPost(tt.post, tt.channel)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDeletePost(t *testing.T) {
	mockBots := &bots.MMBots{}
	ctx := context.Background()
	postID := "test-post-id"

	t.Run("does nothing when search is nil", func(t *testing.T) {
		// Create indexer with nil search
		indexer := New(nil, nil, mockBots, nil)

		// Should not panic and should return no error
		err := indexer.DeletePost(ctx, postID)
		require.NoError(t, err)
	})
}

func TestIndexPost(t *testing.T) {
	mockBots := &bots.MMBots{}
	ctx := context.Background()

	t.Run("does not index deleted post", func(t *testing.T) {
		indexer := New(nil, nil, mockBots, nil)

		post := &model.Post{
			Id:       "post2",
			Message:  "Deleted message",
			Type:     model.PostTypeDefault,
			UserId:   "user1",
			DeleteAt: 123456789, // Deleted post
		}
		channel := &model.Channel{
			Id:     "channel1",
			TeamId: "team1",
			Type:   model.ChannelTypeOpen,
		}

		// Call the method - should not panic and return no error
		err := indexer.IndexPost(ctx, post, channel)

		// Verify no error (deleted posts are ignored, not errored)
		require.NoError(t, err)
	})

	t.Run("does nothing when search is nil", func(t *testing.T) {
		// Create indexer with nil search
		indexer := New(nil, nil, mockBots, nil)

		post := &model.Post{
			Id:       "post1",
			Message:  "Test message",
			Type:     model.PostTypeDefault,
			UserId:   "user1",
			DeleteAt: 0,
		}
		channel := &model.Channel{
			Id:     "channel1",
			TeamId: "team1",
			Type:   model.ChannelTypeOpen,
		}

		// Should not panic and should return no error
		err := indexer.IndexPost(ctx, post, channel)
		require.NoError(t, err)
	})
}
