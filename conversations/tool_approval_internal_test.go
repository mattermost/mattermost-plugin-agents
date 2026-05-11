// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
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
		require.ErrorIs(t, err, ErrStaleToolClick,
			"a second click on an already-resolved approval is a client-side staleness issue, not a server error")
	})
}

// TestBuildToolResultBlocks pins the privacy/visibility contract for
// tool_result block creation in HandleToolCall. The rejected-vs-channel
// distinction is the half of MM-67597 that lives on the writer side: a
// regression that drops the rejected `Shared: true` override would silently
// re-introduce the bug because the directive rejection text would be
// scrubbed by the unshared-redaction in the channel follow-up.
func TestBuildToolResultBlocks(t *testing.T) {
	const now = int64(1_700_000_000_000)

	const (
		acceptedID = "tu-accepted"
		rejectedID = "tu-rejected"
	)
	statusByID := map[string]string{
		acceptedID: conversation.StatusSuccess,
		rejectedID: conversation.StatusRejected,
	}
	results := []toolrunner.ToolResult{
		{ToolCallID: acceptedID, Name: "search", Result: "PUBLIC", IsError: false},
		{ToolCallID: rejectedID, Name: "delete_channel", Result: conversation.RejectedToolCallMessage, IsError: true},
	}

	indexByID := func(blocks []conversation.ContentBlock) map[string]conversation.ContentBlock {
		out := map[string]conversation.ContentBlock{}
		for _, b := range blocks {
			out[b.ToolUseID] = b
		}
		return out
	}

	t.Run("channel: accepted block stays undecided, rejected forces shared=true and decided", func(t *testing.T) {
		blocks := buildToolResultBlocks(results, statusByID, false, now)
		require.Len(t, blocks, 2)

		idx := indexByID(blocks)

		accepted := idx[acceptedID]
		require.NotNil(t, accepted.Shared)
		assert.False(t, *accepted.Shared,
			"channel accepted result must stay shared=false until the user clicks Share")
		assert.Equal(t, conversation.StatusSuccess, accepted.Status)
		assert.Nil(t, accepted.DecidedAt,
			"channel accepted result must stay undecided so the Share/Keep Private prompt renders")

		rejected := idx[rejectedID]
		require.NotNil(t, rejected.Shared)
		assert.True(t, *rejected.Shared,
			"rejected result must be shared=true so the server-authored "+
				"RejectedToolCallMessage reaches the LLM through the unshared "+
				"redaction layer (MM-67597)")
		assert.Equal(t, conversation.StatusRejected, rejected.Status,
			"rejected tool_result status must mirror the rejected tool_use, "+
				"not the IsError-derived StatusError")
		require.NotNil(t, rejected.DecidedAt)
		assert.Equal(t, now, *rejected.DecidedAt,
			"rejected results have no Share decision to make, so DecidedAt must be set at creation")
		assert.Equal(t, conversation.RejectedToolCallMessage, rejected.Content,
			"rejected result content must be the directive message so the "+
				"LLM can ask the user for clarification instead of silently stalling")
	})

	t.Run("DM: every result is shared and decided", func(t *testing.T) {
		blocks := buildToolResultBlocks(results, statusByID, true, now)
		require.Len(t, blocks, 2)

		for _, b := range blocks {
			require.NotNil(t, b.Shared)
			assert.True(t, *b.Shared, "DM results are auto-shared regardless of accept/reject")
			require.NotNil(t, b.DecidedAt, "DM results have no Share/Keep Private prompt, so DecidedAt is set immediately")
		}
	})

	t.Run("error result on accepted tool stays as StatusError, not StatusRejected", func(t *testing.T) {
		errResults := []toolrunner.ToolResult{
			{ToolCallID: acceptedID, Name: "search", Result: "boom", IsError: true},
		}
		blocks := buildToolResultBlocks(errResults, statusByID, false, now)
		require.Len(t, blocks, 1)
		assert.Equal(t, conversation.StatusError, blocks[0].Status,
			"a tool_use that the user accepted but that failed at execution "+
				"is StatusError, not StatusRejected — the rejected path is "+
				"reserved for user-initiated rejection")
	})
}
