// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// DeletedPostPlaceholder stands in for a post-anchored turn whose Mattermost
// post was deleted (or is no longer live in the conversation's channel) when
// replaying history to an LLM. Keeping a non-empty placeholder preserves
// user/assistant role alternation across strict providers (Anthropic, Bedrock)
// and keeps tool_use/tool_result pairing intact.
const DeletedPostPlaceholder = "[message deleted]"

// EmptyEditedPostPlaceholder stands in for a post-anchored turn whose live
// Mattermost post was edited to empty (or whitespace-only) and carries no
// remaining attachments or tool calls, so strict providers never see blank
// text or drop the turn and break role alternation.
const EmptyEditedPostPlaceholder = "[message edited to empty]"

// ErrInitiatingPostDeleted is returned when the user post that initiated a
// regenerated response was deleted or is no longer live.
var ErrInitiatingPostDeleted = errors.New("initiating post was deleted")

// PostBatchGetter fetches multiple Mattermost posts in a single SQL query.
// *mmapi.DBClient implements it in production.
type PostBatchGetter interface {
	GetPostsByIDs(postIDs []string) (map[string]mmapi.LivePost, error)
}

// AnchoredPost describes the resolved live state of a Mattermost post that a
// conversation turn is anchored to (via PostID) or belongs to (via RunPostID).
type AnchoredPost struct {
	Message string
	EditAt  int64
	Live    bool
}

// AnchoredPosts holds the resolved state of every post referenced by a
// conversation's turns, scoped to the conversation's channel.
type AnchoredPosts struct {
	posts map[string]AnchoredPost
}

// Get returns the resolved post state for postID. known is false only when
// postID was neither pre-seeded nor looked up (e.g. unit tests with nil clients).
func (a *AnchoredPosts) Get(postID string) (AnchoredPost, bool) {
	if a == nil || postID == "" {
		return AnchoredPost{}, false
	}
	p, ok := a.posts[postID]
	return p, ok
}

// ResolveAnchoredPosts loads the current state of every post referenced by
// turns (PostID and RunPostID), scoped to conv's channel. Any posts already in
// seed (such as threadData.Posts) are reused without querying. Remaining IDs
// are fetched in a single SQL query via batch when provided, falling back to
// fallback.GetPost otherwise. Non-not-found lookup errors fail closed.
func ResolveAnchoredPosts(
	conv *store.Conversation,
	turns []store.Turn,
	batch PostBatchGetter,
	fallback PostGetter,
	seed ...*model.Post,
) (*AnchoredPosts, error) {
	seen := make(map[string]struct{}, len(turns))
	ids := make([]string, 0, len(turns))
	addID := func(id *string) {
		if id == nil || *id == "" {
			return
		}
		if _, dup := seen[*id]; dup {
			return
		}
		seen[*id] = struct{}{}
		ids = append(ids, *id)
	}
	for i := range turns {
		addID(turns[i].PostID)
		addID(turns[i].RunPostID)
	}
	return resolveAnchoredPostIDs(conv, ids, batch, fallback, seed...)
}

func resolveAnchoredPostIDs(
	conv *store.Conversation,
	ids []string,
	batch PostBatchGetter,
	fallback PostGetter,
	seed ...*model.Post,
) (*AnchoredPosts, error) {
	if len(ids) == 0 {
		return &AnchoredPosts{}, nil
	}

	var expectedChannelID, rootPostID string
	if conv != nil {
		if conv.ChannelID != nil {
			expectedChannelID = *conv.ChannelID
		}
		if conv.RootPostID != nil {
			rootPostID = *conv.RootPostID
		}
	}

	queryIDs := ids
	if expectedChannelID == "" && rootPostID != "" && !slices.Contains(ids, rootPostID) {
		queryIDs = append(append([]string(nil), ids...), rootPostID)
	}

	raw := make(map[string]mmapi.LivePost, len(queryIDs))
	seeded := make(map[string]bool, len(seed))
	for _, p := range seed {
		if p != nil && p.Id != "" {
			raw[p.Id] = mmapi.LivePost{
				ID:        p.Id,
				ChannelID: p.ChannelId,
				Message:   p.Message,
				EditAt:    p.EditAt,
				DeleteAt:  p.DeleteAt,
			}
			seeded[p.Id] = true
		}
	}

	missing := make([]string, 0, len(queryIDs))
	for _, id := range queryIDs {
		if !seeded[id] {
			missing = append(missing, id)
		}
	}

	canLookup := batch != nil || fallback != nil
	if len(missing) > 0 && canLookup {
		if batch != nil {
			fetched, err := batch.GetPostsByIDs(missing)
			if err != nil {
				return nil, fmt.Errorf("failed to read conversation posts: %w", err)
			}
			maps.Copy(raw, fetched)
		} else {
			for _, id := range missing {
				post, err := fallback.GetPost(id)
				if err != nil {
					if mmapi.IsNotFound(err) {
						continue
					}
					return nil, fmt.Errorf("failed to read the post a conversation turn is anchored to: %w", err)
				}
				if post != nil {
					raw[id] = mmapi.LivePost{
						ID:        post.Id,
						ChannelID: post.ChannelId,
						Message:   post.Message,
						EditAt:    post.EditAt,
						DeleteAt:  post.DeleteAt,
					}
				}
			}
		}
	}

	// For ChannelID == nil conversations (e.g. DM-delivered summaries), learn
	// the channel from the root post so cross-channel anchors are still rejected.
	rootDead := false
	if expectedChannelID == "" && rootPostID != "" && (seeded[rootPostID] || canLookup) {
		root, ok := raw[rootPostID]
		if !ok || root.DeleteAt != 0 {
			rootDead = true
		} else {
			expectedChannelID = root.ChannelID
		}
	}

	resolved := make(map[string]AnchoredPost, len(ids))
	for _, id := range ids {
		if !seeded[id] && !canLookup {
			continue
		}
		if rootDead {
			resolved[id] = AnchoredPost{}
			continue
		}
		post, ok := raw[id]
		if !ok || post.DeleteAt != 0 || (expectedChannelID != "" && post.ChannelID != expectedChannelID) {
			resolved[id] = AnchoredPost{}
			continue
		}
		resolved[id] = AnchoredPost{
			Message: post.Message,
			EditAt:  post.EditAt,
			Live:    true,
		}
	}
	return &AnchoredPosts{posts: resolved}, nil
}

// WithoutLeadingMention drops the "@agent " the plugin puts in front of the
// text of a turn it answers on its own (auto-reply, loop-in), where agent is
// agentUsername. A bare mention, stored for a post with no text of its own,
// leaves nothing. Any other text is returned as it is.
func WithoutLeadingMention(text, agentUsername string) string {
	if agentUsername == "" {
		return text
	}
	mention := "@" + agentUsername
	if text == mention {
		return ""
	}
	if rest, found := strings.CutPrefix(text, mention+" "); found {
		return rest
	}
	return text
}

// ReconcileTurnForDisplay reconciles a turn's content blocks against its
// resolved Mattermost post for GET /conversations/{id}. textChanged reports
// whether the turn's effective text differs from what was stored (ignoring a
// plugin-added leading "@agent " mention), which callers use to clear a title
// derived from the opening turn.
func ReconcileTurnForDisplay(
	turn store.Turn,
	blocks []ContentBlock,
	agentUsername string,
	anchored *AnchoredPosts,
) (reconciled []ContentBlock, textChanged bool) {
	reconciled, textChanged, _ = reconcileTurnBlocks(turn, blocks, anchored, agentUsername, false)
	return reconciled, textChanged
}

// ReconcileTurnForLLM reconciles a turn's content blocks against its resolved
// Mattermost post for LLM request building. Deleted posts map to
// DeletedPostPlaceholder (or empty text on tool-only assistant turns) while
// dropping attachments and text-coupled blocks; posts edited to empty with no
// remaining attachments or tool calls map to EmptyEditedPostPlaceholder so
// strict providers keep valid alternating roles and paired tool calls.
func ReconcileTurnForLLM(
	turn store.Turn,
	blocks []ContentBlock,
	agentUsername string,
	anchored *AnchoredPosts,
) (reconciled []ContentBlock, editedToBlank bool) {
	reconciled, _, editedToBlank = reconcileTurnBlocks(turn, blocks, anchored, agentUsername, true)
	return reconciled, editedToBlank
}

func reconcileTurnBlocks(
	turn store.Turn,
	blocks []ContentBlock,
	anchored *AnchoredPosts,
	agentUsername string,
	forLLM bool,
) (reconciled []ContentBlock, textChanged bool, editedToBlank bool) {
	if anchored == nil {
		return blocks, false, false
	}

	// Tool-round and demoted-anchor turns are linked to their response post via
	// RunPostID rather than PostID. They carry intermediate round state (not the
	// post's final message), so live edits leave them alone while deleting the
	// post strips their text, attachments, and text-coupled blocks.
	if turn.PostID == nil || *turn.PostID == "" {
		if turn.RunPostID != nil && *turn.RunPostID != "" {
			if anchor, known := anchored.Get(*turn.RunPostID); known && !anchor.Live {
				return WithoutPostContent(blocks, ""), true, false
			}
		}
		return blocks, false, false
	}

	anchor, known := anchored.Get(*turn.PostID)
	if !known {
		return blocks, false, false
	}

	// While a response is still streaming, partial text is checkpointed on the
	// turn while the Mattermost placeholder post stays empty until settlement.
	if turn.Status == store.TurnStatusStreaming && anchor.Live {
		return blocks, false, false
	}

	if !anchor.Live {
		replacement := ""
		if forLLM && (turn.Role != "assistant" || TextContent(blocks) != "" || !hasToolUseBlocks(blocks)) {
			replacement = DeletedPostPlaceholder
		}
		return WithoutPostContent(blocks, replacement), true, false
	}

	// An unedited empty assistant post indicates either the brief window
	// between turn settlement and UpdatePost or a failed final UpdatePost (for
	// example exceeding Mattermost's post length limit), not a user edit.
	if turn.Role == "assistant" && anchor.Message == "" && anchor.EditAt == 0 {
		return blocks, false, false
	}

	stored := TextContent(blocks)
	if stored == "" && (!forLLM || turn.Role != "user") {
		return blocks, false, false
	}
	if anchor.Message == stored {
		return blocks, false, false
	}
	if agentUsername != "" && strings.TrimSpace(anchor.Message) == strings.TrimSpace(WithoutLeadingMention(stored, agentUsername)) {
		if forLLM {
			return blocks, false, false
		}
		return WithTextContent(blocks, anchor.Message), false, false
	}

	if !forLLM {
		return WithTextContent(blocks, anchor.Message), true, false
	}

	replacement := anchor.Message
	blankEdit := strings.TrimSpace(replacement) == ""
	if blankEdit {
		if turn.Role == "user" && !hasAttachmentBlocks(blocks) {
			replacement = EmptyEditedPostPlaceholder
		} else if turn.Role == "assistant" && !hasToolUseBlocks(blocks) {
			replacement = EmptyEditedPostPlaceholder
		}
	}
	return WithTextContent(blocks, replacement), true, blankEdit
}

func hasAttachmentBlocks(blocks []ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == BlockTypeFile || b.Type == BlockTypeImage {
			return true
		}
	}
	return false
}

func hasToolUseBlocks(blocks []ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == BlockTypeToolUse && b.ID != "" && b.Name != "" {
			return true
		}
	}
	return false
}
