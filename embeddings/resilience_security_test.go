// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings_test

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stage 10: Resilience & Security Tests
// These tests verify security boundaries and resilient behavior of the embedding search system.

// TestUserRemovedFromChannel verifies that users cannot access posts
// from channels they've been removed from.
func TestUserRemovedFromChannel(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	const dimensions = 64
	search := createFullSearchSystem(t, db, dimensions)
	ctx := context.Background()

	// Create a channel where user1 is initially a member
	addTestChannel(t, db, "private_channel", "team1", "P", []string{"user1", "user2"})

	now := model.GetMillis()

	// User2 posts a message while user1 is still a member
	addTestPost(t, db, "secret_post", "user2", "private_channel", "Secret information that should be protected", now)

	doc := embeddings.PostDocument{
		PostID:    "secret_post",
		CreateAt:  now,
		TeamID:    "team1",
		ChannelID: "private_channel",
		UserID:    "user2",
		Content:   "Secret information that should be protected",
	}
	err := search.Store(ctx, []embeddings.PostDocument{doc})
	require.NoError(t, err)

	// Verify user1 CAN see the post while they're a member
	results, err := search.Search(ctx, "secret information", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user1",
	})
	require.NoError(t, err)
	assert.Len(t, results, 1, "User1 should see the post while they're a channel member")
	assert.Equal(t, "secret_post", results[0].Document.PostID)

	// Now remove user1 from the channel (simulate user being kicked/leaving)
	_, err = db.Exec("DELETE FROM ChannelMembers WHERE ChannelId = $1 AND UserId = $2",
		"private_channel", "user1")
	require.NoError(t, err)

	// Verify user1 can NO LONGER see the post
	results, err = search.Search(ctx, "secret information", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user1",
	})
	require.NoError(t, err)
	assert.Len(t, results, 0, "User1 should NOT see the post after being removed from channel")

	// Verify user2 (still a member) CAN still see the post
	results, err = search.Search(ctx, "secret information", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user2",
	})
	require.NoError(t, err)
	assert.Len(t, results, 1, "User2 should still see the post")
}

// TestSearchAcrossArchivedChannels verifies that posts in archived channels
// are excluded from search results (archived = DeleteAt != 0).
func TestSearchAcrossArchivedChannels(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	const dimensions = 64
	search := createFullSearchSystem(t, db, dimensions)
	ctx := context.Background()

	// Create two channels - one will be archived
	addTestChannel(t, db, "active_channel", "team1", "O", []string{"user1"})
	addTestChannel(t, db, "to_be_archived", "team1", "O", []string{"user1"})

	now := model.GetMillis()

	// Add posts to both channels
	addTestPost(t, db, "active_post", "user1", "active_channel", "Content in active channel", now)
	addTestPost(t, db, "archived_post", "user1", "to_be_archived", "Content in soon-to-be-archived channel", now+1)

	docs := []embeddings.PostDocument{
		{
			PostID:    "active_post",
			CreateAt:  now,
			TeamID:    "team1",
			ChannelID: "active_channel",
			UserID:    "user1",
			Content:   "Content in active channel",
		},
		{
			PostID:    "archived_post",
			CreateAt:  now + 1,
			TeamID:    "team1",
			ChannelID: "to_be_archived",
			UserID:    "user1",
			Content:   "Content in soon-to-be-archived channel",
		},
	}
	err := search.Store(ctx, docs)
	require.NoError(t, err)

	// Verify both posts are visible before archival
	results, err := search.Search(ctx, "content channel", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user1",
	})
	require.NoError(t, err)
	assert.Len(t, results, 2, "Both posts should be visible before archival")

	// Archive the channel (Mattermost sets DeleteAt to non-zero timestamp)
	_, err = db.Exec("UPDATE Channels SET DeleteAt = $1 WHERE Id = $2", model.GetMillis(), "to_be_archived")
	require.NoError(t, err)

	// Verify only active channel posts are visible after archival
	results, err = search.Search(ctx, "content channel", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user1",
	})
	require.NoError(t, err)
	assert.Len(t, results, 1, "Only active channel post should be visible after archival")
	if len(results) > 0 {
		assert.Equal(t, "active_post", results[0].Document.PostID)
	}

	// Verify the archived post embedding still exists in the database
	// (it's just filtered out at query time)
	var embeddingExists bool
	err = db.Get(&embeddingExists, "SELECT EXISTS(SELECT 1 FROM llm_posts_embeddings WHERE post_id = 'archived_post')")
	require.NoError(t, err)
	assert.True(t, embeddingExists, "Embedding should still exist for archived channel post")
}
