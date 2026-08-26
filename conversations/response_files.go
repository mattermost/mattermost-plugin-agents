// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

const maxResponseAttachments = llm.MaxPostAttachments

// decorateStreamWithCreatedFiles wraps stream so that, on a clean end, the
// files created during the turn (recorded on the given contexts via
// CreateFile, plus extraFileIDs recovered from persisted turns) are announced
// with an EventTypeFiles event immediately before EventTypeEnd. The streaming
// layer merges the IDs into post.FileIds and the server's UpdatePost performs
// the attach, only touching still-unattached files and stripping the rest —
// which makes over-collection (e.g. from the turn-scan fallback) safe.
func (c *Conversations) decorateStreamWithCreatedFiles(stream *llm.TextStreamResult, post *model.Post, extraFileIDs []string, contexts ...*llm.Context) *llm.TextStreamResult {
	if stream == nil {
		return nil
	}
	output := make(chan llm.TextStreamEvent)
	go func() {
		defer close(output)
		for event := range stream.Stream {
			// Errors and a close without End pass through untouched; the
			// files stay unattached until a later follow-up's turn scan.
			if event.Type == llm.EventTypeEnd {
				if ids := c.collectAttachableFileIDs(post, extraFileIDs, contexts); len(ids) > 0 {
					output <- llm.TextStreamEvent{Type: llm.EventTypeFiles, Value: ids}
				}
			}
			output <- event
		}
	}()
	return &llm.TextStreamResult{Stream: output}
}

// collectAttachableFileIDs merges the created-file registries of the given
// contexts (nil contexts are skipped) with extraFileIDs, dropping duplicates
// and IDs already attached to post, and truncating so the post stays within
// maxResponseAttachments.
func (c *Conversations) collectAttachableFileIDs(post *model.Post, extraFileIDs []string, contexts []*llm.Context) []string {
	seen := make(map[string]bool, len(post.FileIds))
	for _, id := range post.FileIds {
		seen[id] = true
	}
	var candidates []string
	appendID := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		candidates = append(candidates, id)
	}
	for _, llmCtx := range contexts {
		if llmCtx == nil {
			continue
		}
		for _, f := range mmtools.ConsumeCreatedFiles(llmCtx) {
			appendID(f.ID)
		}
	}
	for _, id := range extraFileIDs {
		appendID(id)
	}
	if room := max(maxResponseAttachments-len(post.FileIds), 0); len(candidates) > room {
		if c.mmClient != nil {
			c.mmClient.LogWarn("Truncating created files beyond the response attachment cap",
				"post_id", post.Id, "dropped", len(candidates)-room, "cap", maxResponseAttachments)
		}
		candidates = candidates[:room]
	}
	return candidates
}

// collectCreatedFileIDsFromTurns returns file IDs from successful CreateFile
// tool results persisted in the turns of the agent run that produced postID.
// Used on cross-request resumes where the in-memory registry is gone.
func (c *Conversations) collectCreatedFileIDsFromTurns(convID, postID string) []string {
	if c.convService == nil {
		return nil
	}
	turns, err := c.convService.GetTurns(convID)
	if err != nil {
		if c.mmClient != nil {
			c.mmClient.LogError("Failed to get turns for created-file recovery", "error", err, "conversation_id", convID)
		}
		return nil
	}
	return createdFileIDsFromTurnWindow(turns, postID)
}

// createdFileIDsFromTurnWindow scans the turns of the agent run that produced
// postID for built-in CreateFile tool_use blocks and their successful
// results, returning the parsed file IDs in order of appearance. The window
// mirrors conversation.Service.GetInitiatingUserTurn (every turn after the
// user turn that initiated the run); when no assistant turn matches postID it
// falls back to scanning from the last user turn onward, which at worst
// over-collects within the newest run.
func createdFileIDsFromTurnWindow(turns []store.Turn, postID string) []string {
	anchorSeq, anchorFound := 0, false
	for i := range turns {
		if turns[i].Role == "assistant" && turns[i].PostID != nil && *turns[i].PostID == postID {
			anchorSeq, anchorFound = turns[i].Sequence, true
			break
		}
	}
	// Exclusive lower bound of the scan: the initiating user turn's sequence.
	// When no user turn qualifies, the whole conversation is scanned.
	windowStart, windowFound := 0, false
	for _, turn := range slices.Backward(turns) {
		if turn.Role != "user" {
			continue
		}
		if anchorFound && turn.Sequence >= anchorSeq {
			continue
		}
		windowStart, windowFound = turn.Sequence, true
		break
	}

	createFileUses := make(map[string]bool)
	seen := make(map[string]bool)
	var ids []string
	for i := range turns {
		if windowFound && turns[i].Sequence <= windowStart {
			continue
		}
		if turns[i].Role != "assistant" && turns[i].Role != "tool_result" {
			continue
		}
		blocks, err := conversation.UnmarshalBlocks(turns[i].Content)
		if err != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case conversation.BlockTypeToolUse:
				// Empty ServerOrigin excludes MCP tools that share the name.
				if turns[i].Role == "assistant" && b.Name == mmtools.CreateFileToolName && b.ServerOrigin == "" && b.ID != "" {
					createFileUses[b.ID] = true
				}
			case conversation.BlockTypeToolResult:
				if !createFileUses[b.ToolUseID] {
					continue
				}
				if b.Status != conversation.StatusSuccess && b.Status != conversation.StatusAutoApproved {
					continue
				}
				if result, ok := mmtools.ParseCreateFileResult(b.Content); ok && !seen[result.FileID] {
					seen[result.FileID] = true
					ids = append(ids, result.FileID)
				}
			}
		}
	}
	return ids
}
