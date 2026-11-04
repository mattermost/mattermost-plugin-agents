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
				ServerOrigin: "http://srv-a/mcp", Shared: new(false), UIMeta: uiMeta,
			}}),
			Sequence: 1,
		},
		{
			ID: "t2", Role: "tool_result",
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolResult, ToolUseID: "reuse", Content: "private",
				Shared: new(false),
			}}),
			Sequence: 2,
		},
		{
			ID: "t3", PostID: &postB, Role: "assistant",
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolUse, ID: "reuse", Name: "demo",
				ServerOrigin: "http://srv-a/mcp", Shared: new(true), UIMeta: uiMeta,
			}}),
			Sequence: 3,
		},
		{
			ID: "t4", Role: "tool_result",
			Content: mustBlocks([]ContentBlock{{
				Type: BlockTypeToolResult, ToolUseID: "reuse", Content: "shared",
				Shared: new(true),
			}}),
			Sequence: 4,
		},
	}

	dupTurns := []store.Turn{{
		ID: "d1", PostID: &postA, Role: "assistant",
		Content: mustBlocks([]ContentBlock{
			{Type: BlockTypeToolUse, ID: "dup", Name: "a"},
			{Type: BlockTypeToolUse, ID: "dup", Name: "b"},
		}),
	}}

	tests := []struct {
		name        string
		turns       []store.Turn
		postID      string
		toolCallID  string
		wantErr     error
		wantShared  bool
		wantContent string
	}{
		{name: "unshared round does not cross-authorize via later shared result", turns: turns, postID: postA, toolCallID: "reuse", wantContent: "private"},
		{name: "shared round resolves its own blocks", turns: turns, postID: postB, toolCallID: "reuse", wantShared: true, wantContent: "shared"},
		{name: "duplicate id in same post span is ambiguous", turns: dupTurns, postID: postA, toolCallID: "dup", wantErr: ErrAmbiguousToolCallID},
		{name: "missing tool call", turns: turns, postID: postA, toolCallID: "missing", wantErr: ErrToolCallNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolUse, toolResult, err := FindToolCallBlocks(tt.turns, tt.postID, tt.toolCallID)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, toolUse)
			require.NotNil(t, toolResult)
			require.Equal(t, tt.wantShared, toolUse.Shared != nil && *toolUse.Shared)
			require.Equal(t, tt.wantShared, toolResult.Shared != nil && *toolResult.Shared)
			require.Equal(t, tt.wantContent, toolResult.Content)
		})
	}
}
