// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stringPtr(s string) *string { return &s }

func assistantTurnWithPending(t *testing.T, id, postID string, seq int) store.Turn {
	t.Helper()
	blocks := []conversation.ContentBlock{
		{Type: conversation.BlockTypeToolUse, ID: "tu_" + id, Name: "search", Status: conversation.StatusPending},
	}
	content, err := json.Marshal(blocks)
	require.NoError(t, err)
	return store.Turn{
		ID:       id,
		PostID:   stringPtr(postID),
		Role:     "assistant",
		Content:  content,
		Sequence: seq,
	}
}

func TestFindPendingToolTurn(t *testing.T) {
	alicePendingPost := "post-alice-pending"
	bobPendingPost := "post-bob-pending"

	turns := []store.Turn{
		{ID: "u1", Role: "user", Sequence: 1, Content: json.RawMessage("[]")},
		assistantTurnWithPending(t, "a-alice", alicePendingPost, 2),
		{ID: "u2", Role: "user", Sequence: 3, Content: json.RawMessage("[]")},
		assistantTurnWithPending(t, "a-bob", bobPendingPost, 4),
	}

	t.Run("returns the turn matching the clicked post", func(t *testing.T) {
		got, blocks, err := findPendingToolTurn(turns, alicePendingPost)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "a-alice", got.ID)
		require.Len(t, blocks, 1)
		assert.Equal(t, "tu_a-alice", blocks[0].ID)
	})

	t.Run("does not cross-resolve a later pending turn", func(t *testing.T) {
		got, _, err := findPendingToolTurn(turns, alicePendingPost)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.NotEqual(t, "a-bob", got.ID)
	})

	t.Run("errors when clicked post has no matching turn", func(t *testing.T) {
		_, _, err := findPendingToolTurn(turns, "post-does-not-exist")
		require.Error(t, err)
	})

	t.Run("errors when clicked post's turn has no pending tool_use blocks", func(t *testing.T) {
		resolvedBlocks := []conversation.ContentBlock{
			{Type: conversation.BlockTypeToolUse, ID: "tu_x", Name: "search", Status: conversation.StatusSuccess},
		}
		content, err := json.Marshal(resolvedBlocks)
		require.NoError(t, err)
		resolved := store.Turn{
			ID: "a-resolved", PostID: stringPtr("post-resolved"), Role: "assistant",
			Content: content, Sequence: 5,
		}
		turnsWithResolved := append([]store.Turn{}, turns...)
		turnsWithResolved = append(turnsWithResolved, resolved)
		_, _, err = findPendingToolTurn(turnsWithResolved, "post-resolved")
		assert.Error(t, err)
	})
}

func TestFollowUpAlreadyStreamed(t *testing.T) {
	t.Run("no tool_result turn yet returns false", func(t *testing.T) {
		turns := []store.Turn{
			{ID: "u1", Role: "user", Sequence: 1},
			{ID: "a1", Role: "assistant", Sequence: 2, PostID: stringPtr("p-a1")},
		}
		assert.False(t, followUpAlreadyStreamed(turns))
	})

	t.Run("tool_result with no follow-up assistant returns false", func(t *testing.T) {
		turns := []store.Turn{
			{ID: "u1", Role: "user", Sequence: 1},
			{ID: "a1", Role: "assistant", Sequence: 2, PostID: stringPtr("p-a1")},
			{ID: "tr1", Role: "tool_result", Sequence: 3},
		}
		assert.False(t, followUpAlreadyStreamed(turns))
	})

	t.Run("follow-up assistant with PostID after tool_result returns true", func(t *testing.T) {
		turns := []store.Turn{
			{ID: "u1", Role: "user", Sequence: 1},
			{ID: "a1", Role: "assistant", Sequence: 2, PostID: stringPtr("p-a1")},
			{ID: "tr1", Role: "tool_result", Sequence: 3},
			{ID: "a2", Role: "assistant", Sequence: 4, PostID: stringPtr("p-a2")},
		}
		assert.True(t, followUpAlreadyStreamed(turns))
	})

	t.Run("follow-up assistant without PostID does not count", func(t *testing.T) {
		turns := []store.Turn{
			{ID: "u1", Role: "user", Sequence: 1},
			{ID: "a1", Role: "assistant", Sequence: 2, PostID: stringPtr("p-a1")},
			{ID: "tr1", Role: "tool_result", Sequence: 3},
			{ID: "a2-inline", Role: "assistant", Sequence: 4, PostID: nil},
		}
		assert.False(t, followUpAlreadyStreamed(turns))
	})
}
