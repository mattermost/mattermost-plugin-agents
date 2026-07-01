// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/stretchr/testify/require"
)

func postPtr(s string) *string { return &s }

func blockJSON(t *testing.T, blocks []ContentBlock) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(blocks)
	require.NoError(t, err)
	return b
}

// TestComputePostApprovalState covers the state machine that drives the tool
// approval UI. Each case pins down one transition we care about — the
// infinite-loop bugs behind these design changes all came from ambiguity in
// this machine, so the tests guard the distinct states explicitly.
func TestComputePostApprovalState(t *testing.T) {
	tests := []struct {
		name   string
		postID string
		turns  []store.Turn
		want   string
	}{
		{
			name:   "missing post returns done (fail-safe)",
			postID: "does-not-exist",
			turns: []store.Turn{
				{Role: "user", Sequence: 1, Content: blockJSON(t, nil)},
			},
			want: ApprovalStageDone,
		},
		{
			name:   "post with pending tool_use returns call",
			postID: "p1",
			turns: []store.Turn{
				{Role: "user", Sequence: 1, Content: blockJSON(t, nil)},
				{Role: "assistant", Sequence: 2, PostID: postPtr("p1"), Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolUse, ID: "tc1", Name: "x", Status: StatusPending},
				})},
			},
			want: ApprovalStageCall,
		},
		{
			name:   "executed tool with undecided tool_result returns result",
			postID: "p1",
			turns: []store.Turn{
				{Role: "user", Sequence: 1, Content: blockJSON(t, nil)},
				{Role: "assistant", Sequence: 2, PostID: postPtr("p1"), Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolUse, ID: "tc1", Name: "x", Status: StatusSuccess},
				})},
				{Role: "tool_result", Sequence: 3, Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolResult, ToolUseID: "tc1", Status: StatusSuccess},
				})},
			},
			want: ApprovalStageResult,
		},
		{
			name:   "decided_at on every tool_result returns done (keep private or share)",
			postID: "p1",
			turns: []store.Turn{
				{Role: "user", Sequence: 1, Content: blockJSON(t, nil)},
				{Role: "assistant", Sequence: 2, PostID: postPtr("p1"), Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolUse, ID: "tc1", Name: "x", Status: StatusSuccess},
				})},
				{Role: "tool_result", Sequence: 3, Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolResult, ToolUseID: "tc1", Status: StatusSuccess, DecidedAt: Int64Ptr(1000)},
				})},
			},
			want: ApprovalStageDone,
		},
		{
			name:   "all-rejected post returns done (no result stage to enter)",
			postID: "p1",
			turns: []store.Turn{
				{Role: "user", Sequence: 1, Content: blockJSON(t, nil)},
				{Role: "assistant", Sequence: 2, PostID: postPtr("p1"), Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolUse, ID: "tc1", Name: "x", Status: StatusRejected},
				})},
				{Role: "tool_result", Sequence: 3, Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolResult, ToolUseID: "tc1", Status: StatusError, DecidedAt: Int64Ptr(1000)},
				})},
			},
			want: ApprovalStageDone,
		},
		{
			name:   "mixed rejected and executed with undecided result returns result",
			postID: "p1",
			turns: []store.Turn{
				{Role: "user", Sequence: 1, Content: blockJSON(t, nil)},
				{Role: "assistant", Sequence: 2, PostID: postPtr("p1"), Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolUse, ID: "tc_reject", Name: "a", Status: StatusRejected},
					{Type: BlockTypeToolUse, ID: "tc_ok", Name: "b", Status: StatusSuccess},
				})},
				{Role: "tool_result", Sequence: 3, Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolResult, ToolUseID: "tc_reject", Status: StatusError},
					{Type: BlockTypeToolResult, ToolUseID: "tc_ok", Status: StatusSuccess},
				})},
			},
			want: ApprovalStageResult,
		},
		{
			name:   "tool-round turn (no post_id) for this post counts toward state",
			postID: "p1",
			turns: []store.Turn{
				{Role: "user", Sequence: 1, Content: blockJSON(t, nil)},
				{Role: "assistant", Sequence: 2, Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolUse, ID: "tc_auto", Name: "read", Status: StatusAutoApproved},
				})},
				{Role: "tool_result", Sequence: 3, Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeToolResult, ToolUseID: "tc_auto", Status: StatusSuccess, DecidedAt: Int64Ptr(1000)},
				})},
				{Role: "assistant", Sequence: 4, PostID: postPtr("p1"), Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeText, Text: "done"},
				})},
			},
			want: ApprovalStageDone,
		},
		{
			name:   "preceding post's tool_use does not cross the boundary",
			postID: "p2",
			turns: []store.Turn{
				{Role: "user", Sequence: 1, Content: blockJSON(t, nil)},
				{Role: "assistant", Sequence: 2, PostID: postPtr("p1"), Content: blockJSON(t, []ContentBlock{
					// Still pending for p1 — must not force p2 into 'call'.
					{Type: BlockTypeToolUse, ID: "tc_p1", Name: "x", Status: StatusPending},
				})},
				{Role: "assistant", Sequence: 3, PostID: postPtr("p2"), Content: blockJSON(t, []ContentBlock{
					{Type: BlockTypeText, Text: "follow up"},
				})},
			},
			want: ApprovalStageDone,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputePostApprovalState(tc.turns, tc.postID)
			require.Equal(t, tc.want, got)
		})
	}
}
