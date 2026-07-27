// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// maxResponseAttachments caps attachments on a response post (Mattermost
// allows ~10 attachments per post).
const maxResponseAttachments = 10

// fileLinker links created files to a post; *mmapi.DBClient.LinkFilesToPost
// is the production implementation. Injected for testability.
type fileLinker func(fileIDs []string, postID, channelID string) ([]string, error)

// decorateStreamWithCreatedFiles wraps stream so that, when the stream ends
// cleanly, any files created during the turn (recorded on the given contexts
// via CreateFile, plus extraFileIDs recovered from persisted turns) are
// linked to post and announced with an EventTypeFiles event immediately
// before EventTypeEnd. All other events pass through unchanged.
func (c *Conversations) decorateStreamWithCreatedFiles(stream *llm.TextStreamResult, post *model.Post, extraFileIDs []string, contexts ...*llm.Context) *llm.TextStreamResult {
	if stream == nil {
		return nil
	}
	if c.db == nil {
		if c.mmClient != nil {
			c.mmClient.LogDebug("Skipping created-file attachment: no database client configured")
		}
		return stream
	}
	return c.decorateStreamWithCreatedFilesUsing(c.db.LinkFilesToPost, stream, post, extraFileIDs, contexts...)
}

// decorateStreamWithCreatedFilesUsing is decorateStreamWithCreatedFiles with
// the linker injected so tests can stub the database write.
func (c *Conversations) decorateStreamWithCreatedFilesUsing(link fileLinker, stream *llm.TextStreamResult, post *model.Post, extraFileIDs []string, contexts ...*llm.Context) *llm.TextStreamResult {
	if stream == nil {
		return nil
	}
	output := make(chan llm.TextStreamEvent)
	go func() {
		defer close(output)
		for event := range stream.Stream {
			// Only a clean end attaches files. Error events and a channel
			// close without an End event pass through untouched: the created
			// files stay unattached and a later follow-up's turn scan may
			// recover them.
			if event.Type == llm.EventTypeEnd {
				if ids := c.collectAttachableFileIDs(post, extraFileIDs, contexts); len(ids) > 0 {
					linked, err := link(ids, post.Id, post.ChannelId)
					if err != nil && c.mmClient != nil {
						c.mmClient.LogError("Failed to link created files to response post", "error", err, "post_id", post.Id)
					}
					// A partial link (error after some rows updated) still
					// announces the linked subset so the post reflects it.
					if len(linked) > 0 {
						output <- llm.TextStreamEvent{Type: llm.EventTypeFiles, Value: linked}
					}
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
	if room := maxResponseAttachments - len(post.FileIds); len(candidates) > room {
		if room < 0 {
			room = 0
		}
		if c.mmClient != nil {
			c.mmClient.LogWarn("Truncating created files beyond the response attachment cap",
				"post_id", post.Id, "dropped", len(candidates)-room, "cap", maxResponseAttachments)
		}
		candidates = candidates[:room]
	}
	return candidates
}

// collectCreatedFileIDsFromTurns returns file IDs from successful CreateFile
// tool results persisted in the turns of the agent run that produced postID
// (from the initiating user turn onward). Used on cross-request resumes where
// the in-memory created-file registry is gone. Safe to over-collect: linking
// only touches still-unattached files.
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
// results, returning the parsed file IDs in order of appearance.
//
// The window mirrors conversation.Service.GetInitiatingUserTurn: locate the
// assistant turn anchored to postID, then scan every turn after the closest
// preceding user turn. In the pending-approval resume the anchor turn (the
// one findPendingToolTurn matched) carries postID, so the anchor lookup
// succeeds there too. When no assistant turn matches postID at all, fall
// back to scanning from the LAST user turn onward — at worst that
// over-collects within the newest run, which is safe because linking only
// touches still-unattached files.
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
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role != "user" {
			continue
		}
		if anchorFound && turns[i].Sequence >= anchorSeq {
			continue
		}
		windowStart, windowFound = turns[i].Sequence, true
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
		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turns[i].Content, &blocks); err != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case conversation.BlockTypeToolUse:
				// Only the built-in CreateFile (empty ServerOrigin) counts: an
				// MCP tool that happens to share the name must not be trusted
				// to have produced a real Mattermost file.
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
