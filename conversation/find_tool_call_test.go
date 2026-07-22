// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/stretchr/testify/require"
)

func TestFindToolCallBlocksPostAnchored(t *testing.T) {
	postA := "posta23456789012345678901a"
	postB := "postb23456789012345678901b"
	uiMeta := &llm.ToolUIMeta{ResourceURI: "ui://srv/app.html"}

	mustBlocks := func(blocks []ContentBlock) json.RawMessage {
		t.Helper()
		data, err := json.Marshal(blocks)
		require.NoError(t, err)
		return data
	}

	turns := []store.Turn{
		{
			ID: "t1", PostID: &postA, Role: "assistant",
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolUse, ID: "reuse", Name: "demo",
				ServerOrigin: "http://srv-a/mcp", Shared: BoolPtr(false), UIMeta: uiMeta,
			}}),
			Sequence: 1,
		},
		{
			ID: "t2", Role: "tool_result",
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolResult, ToolUseID: "reuse", Content: "private",
				Shared: BoolPtr(false),
			}}),
			Sequence: 2,
		},
		{
			ID: "t3", PostID: &postB, Role: "assistant",
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolUse, ID: "reuse", Name: "demo",
				ServerOrigin: "http://srv-a/mcp", Shared: BoolPtr(true), UIMeta: uiMeta,
			}}),
			Sequence: 3,
		},
		{
			ID: "t4", Role: "tool_result",
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolResult, ToolUseID: "reuse", Content: "shared",
				Shared: BoolPtr(true),
			}}),
			Sequence: 4,
		},
	}

	t.Run("unshared round does not cross-authorize via later shared result", func(t *testing.T) {
		toolUse, toolResult, err := FindToolCallBlocks(turns, postA, "reuse")
		require.NoError(t, err)
		require.NotNil(t, toolUse)
		require.False(t, toolUse.Shared != nil && *toolUse.Shared)
		require.NotNil(t, toolResult)
		require.False(t, toolResult.Shared != nil && *toolResult.Shared)
		require.Equal(t, "private", toolResult.Content)
	})

	t.Run("shared round resolves its own blocks", func(t *testing.T) {
		toolUse, toolResult, err := FindToolCallBlocks(turns, postB, "reuse")
		require.NoError(t, err)
		require.NotNil(t, toolUse)
		require.True(t, toolUse.Shared != nil && *toolUse.Shared)
		require.NotNil(t, toolResult)
		require.True(t, toolResult.Shared != nil && *toolResult.Shared)
		require.Equal(t, "shared", toolResult.Content)
	})

	t.Run("duplicate id in same post span is ambiguous", func(t *testing.T) {
		dupTurns := []store.Turn{{
			ID: "d1", PostID: &postA, Role: "assistant",
			Content: mustBlocks([]ContentBlock{
				{Type: BlockTypeToolUse, ID: "dup", Name: "a"},
				{Type: BlockTypeToolUse, ID: "dup", Name: "b"},
			}),
		}}
		_, _, err := FindToolCallBlocks(dupTurns, postA, "dup")
		require.ErrorIs(t, err, ErrAmbiguousToolCallID)
	})

	t.Run("missing tool call", func(t *testing.T) {
		_, _, err := FindToolCallBlocks(turns, postA, "missing")
		require.ErrorIs(t, err, ErrToolCallNotFound)
	})

	t.Run("preceding WriteToolTurns rounds without PostID are in span", func(t *testing.T) {
		botPost := "postc23456789012345678901c"
		autoTurns := []store.Turn{
			{
				ID: "u1", Role: "user", Sequence: 1,
				Content: mustBlocks([]ContentBlock{{Type: BlockTypeText, Text: "preview please"}}),
			},
			{
				ID: "a1", Role: "assistant", Sequence: 2,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolUse, ID: "auto1", Name: "preview_post",
					ServerOrigin: "embedded://mattermost", Shared: BoolPtr(true), UIMeta: uiMeta,
				}}),
			},
			{
				ID: "r1", Role: "tool_result", Sequence: 3,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolResult, ToolUseID: "auto1", Content: `{"message":"hi"}`,
					Shared: BoolPtr(true),
				}}),
			},
			{
				ID: "a2", PostID: &botPost, Role: "assistant", Sequence: 4,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeText, Text: "The post preview is shown above.",
				}}),
			},
		}
		toolUse, toolResult, err := FindToolCallBlocks(autoTurns, botPost, "auto1")
		require.NoError(t, err)
		require.NotNil(t, toolUse)
		require.Equal(t, uiMeta, toolUse.UIMeta)
		require.NotNil(t, toolResult)
		require.Equal(t, `{"message":"hi"}`, toolResult.Content)
	})

	// A anchor/result → B unanchored use/result → B anchor.
	// Looking up A must not absorb B's pre-anchor rounds.
	continuationTurns := []store.Turn{
		{
			ID: "a-anchor", PostID: &postA, Role: "assistant", Sequence: 1,
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolUse, ID: "call-a", Name: "demo",
				ServerOrigin: "http://srv-a/mcp", Shared: BoolPtr(true), UIMeta: uiMeta,
			}}),
		},
		{
			ID: "a-result", Role: "tool_result", Sequence: 2,
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolResult, ToolUseID: "call-a", Content: "result-a",
				Shared: BoolPtr(true),
			}}),
		},
		{
			ID: "b-use", Role: "assistant", Sequence: 3,
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolUse, ID: "call-b", Name: "demo",
				ServerOrigin: "http://srv-a/mcp", Shared: BoolPtr(true), UIMeta: uiMeta,
			}}),
		},
		{
			ID: "b-result", Role: "tool_result", Sequence: 4,
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolResult, ToolUseID: "call-b", Content: "result-b",
				Shared: BoolPtr(true),
			}}),
		},
		{
			ID: "b-anchor", PostID: &postB, Role: "assistant", Sequence: 5,
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeText, Text: "done",
			}}),
		},
	}

	t.Run("post A lookup does not resolve B unanchored tool id", func(t *testing.T) {
		_, _, err := FindToolCallBlocks(continuationTurns, postA, "call-b")
		require.ErrorIs(t, err, ErrToolCallNotFound)
	})

	t.Run("post B resolves its own unanchored tool id", func(t *testing.T) {
		toolUse, toolResult, err := FindToolCallBlocks(continuationTurns, postB, "call-b")
		require.NoError(t, err)
		require.NotNil(t, toolUse)
		require.Equal(t, "call-b", toolUse.ID)
		require.NotNil(t, toolResult)
		require.Equal(t, "result-b", toolResult.Content)
	})

	t.Run("reused id across A and B each resolves its own without spurious ambiguity", func(t *testing.T) {
		reused := []store.Turn{
			{
				ID: "a-anchor", PostID: &postA, Role: "assistant", Sequence: 1,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolUse, ID: "reuse-x", Name: "demo",
					ServerOrigin: "http://srv-a/mcp", Shared: BoolPtr(true), UIMeta: uiMeta,
				}}),
			},
			{
				ID: "a-result", Role: "tool_result", Sequence: 2,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolResult, ToolUseID: "reuse-x", Content: "from-a",
					Shared: BoolPtr(true),
				}}),
			},
			{
				ID: "b-use", Role: "assistant", Sequence: 3,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolUse, ID: "reuse-x", Name: "demo",
					ServerOrigin: "http://srv-a/mcp", Shared: BoolPtr(true), UIMeta: uiMeta,
				}}),
			},
			{
				ID: "b-result", Role: "tool_result", Sequence: 4,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolResult, ToolUseID: "reuse-x", Content: "from-b",
					Shared: BoolPtr(true),
				}}),
			},
			{
				ID: "b-anchor", PostID: &postB, Role: "assistant", Sequence: 5,
				Content: mustBlocks([]ContentBlock{{Type: BlockTypeText, Text: "done"}}),
			},
		}
		useA, resultA, err := FindToolCallBlocks(reused, postA, "reuse-x")
		require.NoError(t, err)
		require.Equal(t, "from-a", resultA.Content)
		require.NotNil(t, useA)

		useB, resultB, err := FindToolCallBlocks(reused, postB, "reuse-x")
		require.NoError(t, err)
		require.Equal(t, "from-b", resultB.Content)
		require.NotNil(t, useB)
	})

	t.Run("same id within one response owned rounds is ambiguous", func(t *testing.T) {
		botPost := "postd23456789012345678901d"
		dupOwned := []store.Turn{
			{
				ID: "a1", Role: "assistant", Sequence: 1,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolUse, ID: "dup-owned", Name: "demo",
					ServerOrigin: "http://srv-a/mcp", UIMeta: uiMeta,
				}}),
			},
			{
				ID: "r1", Role: "tool_result", Sequence: 2,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolResult, ToolUseID: "dup-owned", Content: "first",
				}}),
			},
			{
				ID: "a2", Role: "assistant", Sequence: 3,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolUse, ID: "dup-owned", Name: "demo",
					ServerOrigin: "http://srv-a/mcp", UIMeta: uiMeta,
				}}),
			},
			{
				ID: "r2", Role: "tool_result", Sequence: 4,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolResult, ToolUseID: "dup-owned", Content: "second",
				}}),
			},
			{
				ID: "anchor", PostID: &botPost, Role: "assistant", Sequence: 5,
				Content: mustBlocks([]ContentBlock{{Type: BlockTypeText, Text: "final"}}),
			},
		}
		_, _, err := FindToolCallBlocks(dupOwned, botPost, "dup-owned")
		require.ErrorIs(t, err, ErrAmbiguousToolCallID)
	})

	t.Run("user turn stops backward walk before prior response unanchored rounds", func(t *testing.T) {
		// Fable M2: deleting the Role=="user" stop must make this fail as ambiguous.
		botPost := "poste23456789012345678901e"
		separated := []store.Turn{
			{
				ID: "old-use", Role: "assistant", Sequence: 1,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolUse, ID: "shared-x", Name: "demo",
					ServerOrigin: "http://srv-a/mcp", UIMeta: uiMeta,
				}}),
			},
			{
				ID: "old-result", Role: "tool_result", Sequence: 2,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolResult, ToolUseID: "shared-x", Content: "old",
				}}),
			},
			{
				ID: "user-2", Role: "user", Sequence: 3,
				Content: mustBlocks([]ContentBlock{{Type: BlockTypeText, Text: "try again"}}),
			},
			{
				ID: "new-use", Role: "assistant", Sequence: 4,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolUse, ID: "shared-x", Name: "demo",
					ServerOrigin: "http://srv-a/mcp", Shared: BoolPtr(true), UIMeta: uiMeta,
				}}),
			},
			{
				ID: "new-result", Role: "tool_result", Sequence: 5,
				Content: mustBlocks([]ContentBlock{{
					Type: BlockTypeToolResult, ToolUseID: "shared-x", Content: "new",
					Shared: BoolPtr(true),
				}}),
			},
			{
				ID: "anchor", PostID: &botPost, Role: "assistant", Sequence: 6,
				Content: mustBlocks([]ContentBlock{{Type: BlockTypeText, Text: "final"}}),
			},
		}
		toolUse, toolResult, err := FindToolCallBlocks(separated, botPost, "shared-x")
		require.NoError(t, err)
		require.NotNil(t, toolUse)
		require.NotNil(t, toolResult)
		require.Equal(t, "new", toolResult.Content)
	})
}
