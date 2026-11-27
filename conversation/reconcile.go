// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"errors"
	"fmt"
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

// ErrInitiatingPostDeleted is returned by BuildRegenerateRequest when the user
// post that initiated the regenerated response was deleted or is no longer live.
var ErrInitiatingPostDeleted = errors.New("initiating post was deleted")

// AnchoredPost describes the resolved state of a Mattermost post that a
// conversation turn is anchored to.
type AnchoredPost struct {
	Message  string
	CreateAt int64
	EditAt   int64
	UpdateAt int64
	Live     bool
}

// AnchoredPostResolver resolves and caches the current state of Mattermost
// posts that turns are anchored to, at most once per post ID per request.
type AnchoredPostResolver struct {
	posts              PostGetter
	channelID          string
	rootPostID         string
	rootChannelChecked bool
	rootDead           bool
	seeded             map[string]*model.Post
	resolved           map[string]AnchoredPost
}

// NewAnchoredPostResolver constructs a resolver bound to conv's channel.
// When conv.ChannelID is set, only live posts in that channel are considered
// live. When conv.ChannelID is nil (e.g. DM-delivered summaries) and
// conv.RootPostID is set, the root post's channel is resolved lazily to scope
// anchored posts. Any posts passed via seed (such as threadData.Posts) are
// reused without issuing extra GetPost calls.
func NewAnchoredPostResolver(posts PostGetter, conv *store.Conversation, seed ...*model.Post) *AnchoredPostResolver {
	r := &AnchoredPostResolver{
		posts:    posts,
		resolved: make(map[string]AnchoredPost),
	}
	if conv != nil {
		if conv.ChannelID != nil {
			r.channelID = *conv.ChannelID
		}
		if conv.RootPostID != nil {
			r.rootPostID = *conv.RootPostID
		}
	}
	if len(seed) > 0 {
		r.seeded = make(map[string]*model.Post, len(seed))
		for _, p := range seed {
			if p != nil && p.Id != "" {
				r.seeded[p.Id] = p
			}
		}
	}
	return r
}

// Lookup returns the current state of postID. known is false only when no
// PostGetter is configured and postID was not pre-seeded (unit tests with a
// nil client). Any lookup error other than not-found is returned so callers
// fail closed.
func (r *AnchoredPostResolver) Lookup(postID string) (AnchoredPost, bool, error) {
	if r == nil || postID == "" {
		return AnchoredPost{}, false, nil
	}
	if cached, seen := r.resolved[postID]; seen {
		return cached, true, nil
	}

	channelID, rootDead, err := r.expectedChannelID()
	if err != nil {
		return AnchoredPost{}, false, err
	}
	if rootDead {
		r.resolved[postID] = AnchoredPost{}
		return AnchoredPost{}, true, nil
	}
	if cached, seen := r.resolved[postID]; seen {
		return cached, true, nil
	}

	if post, ok := r.seeded[postID]; ok {
		anchor := evaluateAnchoredPost(post, channelID)
		r.resolved[postID] = anchor
		return anchor, true, nil
	}

	if r.posts == nil {
		return AnchoredPost{}, false, nil
	}

	post, err := r.posts.GetPost(postID)
	var anchor AnchoredPost
	switch {
	case err != nil:
		if !mmapi.IsNotFound(err) {
			return AnchoredPost{}, false, fmt.Errorf("failed to read the post a conversation turn is anchored to: %w", err)
		}
	default:
		anchor = evaluateAnchoredPost(post, channelID)
	}

	r.resolved[postID] = anchor
	return anchor, true, nil
}

// expectedChannelID returns the channel ID anchored posts must belong to.
// When conv.ChannelID is nil and conv.RootPostID is set, it inspects the root
// post once to learn its channel (or detect that the root post itself was
// deleted, in which case rootDead is true).
func (r *AnchoredPostResolver) expectedChannelID() (channelID string, rootDead bool, err error) {
	if r.channelID != "" || r.rootChannelChecked || r.rootPostID == "" {
		return r.channelID, r.rootDead, nil
	}

	if post, ok := r.seeded[r.rootPostID]; ok {
		r.rootChannelChecked = true
		if post.DeleteAt != 0 {
			r.rootDead = true
			return "", true, nil
		}
		r.channelID = post.ChannelId
		return r.channelID, false, nil
	}

	if r.posts == nil {
		r.rootChannelChecked = true
		return "", false, nil
	}

	post, err := r.posts.GetPost(r.rootPostID)
	if err != nil {
		if mmapi.IsNotFound(err) {
			r.rootChannelChecked = true
			r.rootDead = true
			r.resolved[r.rootPostID] = AnchoredPost{}
			return "", true, nil
		}
		return "", false, fmt.Errorf("failed to read conversation root post: %w", err)
	}
	r.rootChannelChecked = true
	if post == nil || post.DeleteAt != 0 {
		r.rootDead = true
		r.resolved[r.rootPostID] = AnchoredPost{}
		return "", true, nil
	}
	r.channelID = post.ChannelId
	r.resolved[r.rootPostID] = evaluateAnchoredPost(post, r.channelID)
	return r.channelID, false, nil
}

func evaluateAnchoredPost(post *model.Post, expectedChannelID string) AnchoredPost {
	if post == nil || post.DeleteAt != 0 {
		return AnchoredPost{}
	}
	if expectedChannelID != "" && post.ChannelId != expectedChannelID {
		return AnchoredPost{}
	}
	return AnchoredPost{
		Message:  post.Message,
		CreateAt: post.CreateAt,
		EditAt:   post.EditAt,
		UpdateAt: post.UpdateAt,
		Live:     true,
	}
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

// ReconcileTurnForDisplay reconciles a turn's content blocks against the live
// Mattermost post it is anchored to for GET /conversations/{id}. textChanged
// reports whether the turn's effective text differs from what was stored
// (ignoring a plugin-added leading "@agent " mention), which callers use to
// clear a title derived from the opening turn.
func ReconcileTurnForDisplay(
	turn store.Turn,
	blocks []ContentBlock,
	agentUsername string,
	resolver *AnchoredPostResolver,
) (reconciled []ContentBlock, textChanged bool, err error) {
	if turn.PostID == nil || *turn.PostID == "" || resolver == nil {
		return blocks, false, nil
	}
	stored := TextContent(blocks)
	if stored == "" && !hasAttachmentBlocks(blocks) {
		return blocks, false, nil
	}

	anchor, known, err := resolver.Lookup(*turn.PostID)
	if err != nil || !known {
		return blocks, false, err
	}

	// While a response is still streaming, partial text is checkpointed on the
	// turn while the Mattermost placeholder post stays empty until settlement.
	// Keep the checkpointed text as long as the post is still live.
	if turn.Status == store.TurnStatusStreaming && anchor.Live {
		return blocks, false, nil
	}

	if !anchor.Live {
		return WithoutPostContent(blocks, ""), true, nil
	}

	if stored == "" {
		return blocks, false, nil
	}

	if anchor.Message != stored {
		changed := strings.TrimSpace(anchor.Message) != strings.TrimSpace(WithoutLeadingMention(stored, agentUsername))
		return WithTextContent(blocks, anchor.Message), changed, nil
	}

	return blocks, false, nil
}

// ReconcileOptions configures anchored-post reconciliation when building an
// LLM completion request.
type ReconcileOptions struct {
	Resolver          *AnchoredPostResolver
	AgentUsername     string
	SkipUnvettedEdits bool
	VettedCutoff      int64
}

// ReconcileTurnForLLM reconciles a turn's content blocks against its anchored
// Mattermost post for LLM request building. Deletions always apply (even when
// SkipUnvettedEdits is set) and replace text with DeletedPostPlaceholder while
// dropping attachments and text-coupled blocks. Live edits replace text (unless
// SkipUnvettedEdits blocks edits made after VettedCutoff) and fall back to
// EmptyEditedPostPlaceholder when an edit empties a turn that would otherwise
// be dropped by strict providers.
func ReconcileTurnForLLM(
	turn store.Turn,
	blocks []ContentBlock,
	opts ReconcileOptions,
) (reconciled []ContentBlock, editedToBlank bool, err error) {
	if turn.PostID == nil || *turn.PostID == "" || opts.Resolver == nil {
		return blocks, false, nil
	}

	anchor, known, err := opts.Resolver.Lookup(*turn.PostID)
	if err != nil || !known {
		return blocks, false, err
	}

	if !anchor.Live {
		replacement := DeletedPostPlaceholder
		if turn.Role == "assistant" && TextContent(blocks) == "" && hasToolUseBlocks(blocks) {
			replacement = ""
		}
		return WithoutPostContent(blocks, replacement), false, nil
	}

	stored := TextContent(blocks)
	if stored == "" && turn.Role != "user" {
		return blocks, false, nil
	}
	if anchor.Message == stored {
		return blocks, false, nil
	}
	if opts.AgentUsername != "" && strings.TrimSpace(anchor.Message) == strings.TrimSpace(WithoutLeadingMention(stored, opts.AgentUsername)) {
		return blocks, false, nil
	}
	if opts.SkipUnvettedEdits && isEditAfterCutoff(anchor, opts.VettedCutoff) {
		return blocks, false, nil
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
	return WithTextContent(blocks, replacement), blankEdit, nil
}

func isEditAfterCutoff(anchor AnchoredPost, cutoff int64) bool {
	if cutoff == 0 {
		return true
	}
	if anchor.EditAt > 0 {
		return anchor.EditAt > cutoff
	}
	return anchor.UpdateAt > cutoff
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
