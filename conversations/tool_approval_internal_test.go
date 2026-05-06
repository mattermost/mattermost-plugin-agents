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

// TestFindPendingToolTurn_StaleClickErrorsAreTyped verifies that both
// stale-click cases (no matching turn / matching turn already resolved)
// return a typed sentinel error. The API handler needs this so it can map
// stale/duplicate clicks to 400 Bad Request rather than falling through to
// 500 Internal Server Error via string comparison.
func TestFindPendingToolTurn_StaleClickErrorsAreTyped(t *testing.T) {
	turns := []store.Turn{
		{ID: "u1", Role: "user", Sequence: 1, Content: json.RawMessage("[]")},
		assistantTurnWithPending(t, "a-alice", "post-alice-pending", 2),
	}

	t.Run("no matching turn returns ErrStaleToolClick", func(t *testing.T) {
		_, _, err := findPendingToolTurn(turns, "post-does-not-exist")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrStaleToolClick,
			"callers (HTTP handler) must be able to detect stale clicks via errors.Is; string matching is brittle and the current handler misses this case")
	})

	t.Run("matching turn already resolved returns ErrStaleToolClick", func(t *testing.T) {
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
		require.Error(t, err)
		require.ErrorIs(t, err, ErrStaleToolClick,
			"a second click on an already-resolved approval is a client-side staleness issue, not a server error")
	})
}
