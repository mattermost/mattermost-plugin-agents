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

// FindToolCallBlocks scans the turn span anchored to postID for the tool_use
// block with the given ID and its matching tool_result block.
//
// Anchoring matches the webapp's collectResponseTurns: preceding unanchored
// WriteToolTurns rounds (auto-approved tools written before the final stream
// turn gets PostID), the assistant turn(s) whose PostID equals postID, and
// subsequent turns until the next post-anchored assistant turn. This prevents
// a shared result from another round that reused a provider tool-call ID from
// authorizing a different tool_use.
//
// Duplicate tool_use IDs within the span are rejected as ambiguous.
// turns must be ordered by Sequence (as returned by GetTurnsForConversation).
func FindToolCallBlocks(turns []store.Turn, postID, toolCallID string) (toolUse, toolResult *ContentBlock, err error) {
	if postID == "" || toolCallID == "" {
		return nil, nil, ErrToolCallNotFound
	}

	span := postAnchoredTurnSpan(turns, postID)
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

// postAnchoredTurnSpan returns the turns belonging to postID's response:
// unanchored preceding tool rounds, the PostID-linked assistant turn(s), and
// following turns until another post-anchored assistant turn begins.
// turns must be ordered by Sequence.
func postAnchoredTurnSpan(turns []store.Turn, postID string) []store.Turn {
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
	// rounds that precede stream finalize (no PostID), matching the webapp.
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

	var span []store.Turn
	for i := start; i < len(turns); i++ {
		t := turns[i]
		if i > start && t.PostID != nil && *t.PostID != postID {
			break
		}
		span = append(span, t)
	}
	return span
}
