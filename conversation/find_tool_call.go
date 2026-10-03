// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

var (
	// ErrToolCallNotFound is returned when no tool_use block with the given ID
	// exists in the post-anchored turn span.
	ErrToolCallNotFound = errors.New("tool call not found")
	// ErrAmbiguousToolCallID is returned when more than one tool_use block in
	// the post-anchored span shares the same ID.
	ErrAmbiguousToolCallID = errors.New("ambiguous tool call id")
)

// FindToolCallBlocks scans the turn span owned by postID for the tool_use
// block with the given ID and its matching tool_result block.
//
// Response ownership (turns must be ordered by Sequence):
//   - Backward: unanchored WriteToolTurns rounds belonging to this response
//     (stop at a user turn or a turn anchored to a different post).
//   - Anchor: assistant turn(s) whose PostID equals postID.
//   - Forward: only tool_result turns for tool_use IDs already owned by this
//     response; stop at the first user turn or assistant turn that starts
//     another unanchored round (or a foreign PostID).
//
// Duplicate tool_use IDs within the owned span are rejected as ambiguous.
func FindToolCallBlocks(turns []store.Turn, postID, toolCallID string) (toolUse, toolResult *ContentBlock, err error) {
	if postID == "" || toolCallID == "" {
		return nil, nil, ErrToolCallNotFound
	}

	span := postOwnedTurnSpan(turns, postID)
	if len(span) == 0 {
		return nil, nil, ErrToolCallNotFound
	}

	var useCount int
	for _, turn := range span {
		var blocks []ContentBlock
		if unmarshalErr := json.Unmarshal(turn.Content, &blocks); unmarshalErr != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal turn content: %w", unmarshalErr)
		}
		for i := range blocks {
			block := blocks[i]
			switch {
			case block.Type == BlockTypeToolUse && block.ID == toolCallID:
				useCount++
				copied := block
				toolUse = &copied
			case block.Type == BlockTypeToolResult && block.ToolUseID == toolCallID:
				copied := block
				toolResult = &copied
			}
		}
	}

	if useCount == 0 {
		return nil, nil, ErrToolCallNotFound
	}
	if useCount > 1 {
		return nil, nil, ErrAmbiguousToolCallID
	}
	return toolUse, toolResult, nil
}

// postOwnedTurnSpan returns the turns belonging to postID's response.
// turns must be ordered by Sequence.
func postOwnedTurnSpan(turns []store.Turn, postID string) []store.Turn {
	anchorIdx := -1
	for i, turn := range turns {
		if turn.PostID != nil && *turn.PostID == postID {
			anchorIdx = i
			break
		}
	}
	if anchorIdx == -1 {
		return nil
	}

	// Walk backward from the first PostID-linked turn to include WriteToolTurns
	// rounds that precede stream finalize (no PostID).
	start := anchorIdx
	for i := anchorIdx - 1; i >= 0; i-- {
		t := turns[i]
		if t.Role == "user" {
			break
		}
		if t.PostID != nil && *t.PostID != postID {
			break
		}
		start = i
	}

	// Include consecutive same-PostID anchors in the owned region.
	endExclusive := anchorIdx + 1
	for endExclusive < len(turns) {
		t := turns[endExclusive]
		if t.PostID != nil && *t.PostID == postID {
			endExclusive++
			continue
		}
		break
	}

	ownedIDs := collectToolUseIDs(turns[start:endExclusive])
	span := append([]store.Turn{}, turns[start:endExclusive]...)

	// Forward: only result turns for already-owned tool uses.
	for i := endExclusive; i < len(turns); i++ {
		t := turns[i]
		if t.Role == "user" {
			break
		}
		if t.PostID != nil && *t.PostID != postID {
			break
		}
		if t.Role == "assistant" {
			// An unanchored (or differently-anchored) assistant turn that
			// carries tool_use starts another response's WriteToolTurns round.
			if turnHasToolUse(t) {
				break
			}
			if t.PostID == nil {
				break
			}
		}
		if t.Role == "tool_result" {
			if !toolResultBelongsToOwned(t, ownedIDs) {
				break
			}
			span = append(span, t)
			continue
		}
		break
	}
	return span
}

func collectToolUseIDs(turns []store.Turn) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, turn := range turns {
		var blocks []ContentBlock
		if err := json.Unmarshal(turn.Content, &blocks); err != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type == BlockTypeToolUse && block.ID != "" {
				ids[block.ID] = struct{}{}
			}
		}
	}
	return ids
}

func turnHasToolUse(turn store.Turn) bool {
	var blocks []ContentBlock
	if err := json.Unmarshal(turn.Content, &blocks); err != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == BlockTypeToolUse {
			return true
		}
	}
	return false
}

func toolResultBelongsToOwned(turn store.Turn, ownedIDs map[string]struct{}) bool {
	var blocks []ContentBlock
	if err := json.Unmarshal(turn.Content, &blocks); err != nil {
		return false
	}
	matched := false
	for _, block := range blocks {
		if block.Type != BlockTypeToolResult || block.ToolUseID == "" {
			continue
		}
		if _, ok := ownedIDs[block.ToolUseID]; !ok {
			return false
		}
		matched = true
	}
	return matched
}
