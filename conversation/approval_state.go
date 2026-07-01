// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

// Approval-stage string values mirror the webapp ToolApprovalStage. A post-anchor
// assistant turn carries one of these so the UI can render the correct controls
// without re-deriving state on the client.
const (
	// ApprovalStageCall means one or more tool_use blocks on this post are
	// still pending an Accept/Reject decision.
	ApprovalStageCall = "call"
	// ApprovalStageResult means every tool was executed and at least one
	// tool_result still needs a Share/Keep-private decision.
	ApprovalStageResult = "result"
	// ApprovalStageDone means no user decision remains: every executed tool's
	// result has decided_at set (or the post had no tool_use blocks at all).
	ApprovalStageDone = "done"
)

// ComputePostApprovalState inspects the conversation turns and returns the
// approval stage for the assistant post identified by postID.
//
// A turn belongs to a post's "response" when it sits between the post's anchor
// turn (the assistant turn with matching post_id) and either the preceding
// user turn or another post's anchor. We walk backwards from the anchor to
// collect tool-round turns (no post_id) that belong to this response, then
// match their tool_use blocks against tool_result blocks anywhere in the
// conversation.
func ComputePostApprovalState(turns []store.Turn, postID string) string {
	anchorIdx := -1
	for i, t := range turns {
		if t.Role == "assistant" && t.PostID != nil && *t.PostID == postID {
			anchorIdx = i
		}
	}
	if anchorIdx == -1 {
		return ApprovalStageDone
	}

	responseTurns := []store.Turn{turns[anchorIdx]}
	for i := anchorIdx - 1; i >= 0; i-- {
		t := turns[i]
		if t.Role == "user" {
			break
		}
		if t.PostID != nil && *t.PostID != postID {
			break
		}
		responseTurns = append(responseTurns, t)
	}

	pendingToolUse := false
	executedToolUseIDs := make(map[string]struct{})
	for _, t := range responseTurns {
		var blocks []ContentBlock
		if err := json.Unmarshal(t.Content, &blocks); err != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != BlockTypeToolUse || b.ID == "" {
				continue
			}
			switch b.Status {
			case StatusPending, StatusAccepted:
				pendingToolUse = true
			case StatusSuccess, StatusError, StatusAutoApproved:
				executedToolUseIDs[b.ID] = struct{}{}
			}
		}
	}

	if pendingToolUse {
		return ApprovalStageCall
	}
	if len(executedToolUseIDs) == 0 {
		return ApprovalStageDone
	}

	undecidedResult := false
	sawMatchingResult := false
	for _, t := range turns {
		var blocks []ContentBlock
		if err := json.Unmarshal(t.Content, &blocks); err != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != BlockTypeToolResult || b.ToolUseID == "" {
				continue
			}
			if _, ok := executedToolUseIDs[b.ToolUseID]; !ok {
				continue
			}
			sawMatchingResult = true
			if b.DecidedAt == nil {
				undecidedResult = true
			}
		}
	}

	if !sawMatchingResult {
		return ApprovalStageDone
	}
	if undecidedResult {
		return ApprovalStageResult
	}
	return ApprovalStageDone
}
