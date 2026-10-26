// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost/server/public/model"
)

// AutoReplySettings is the read side of the per-channel auto-reply
// configuration. GetCached must be the in-memory cached lookup (Phase 1): it
// runs from MessageHasBeenPosted for every posted message and must never touch
// the database. ok is false when the channel has auto-reply off (no row).
// Implemented by *autoreply.Service.
type AutoReplySettings interface {
	GetCached(channelID string) (autoreply.Setting, bool)
}

// autoReplySettingForChannel returns the channel's auto-reply setting, or nil
// when the feature is unconfigured or unavailable. This runs on the
// MessageHasBeenPosted hot path and must never break message handling.
func (c *Conversations) autoReplySettingForChannel(channel *model.Channel) *autoreply.Setting {
	if c.autoReplySettings == nil || channel == nil {
		return nil
	}
	// Defensive: rows can never exist for DM/GM channels (PUT rejects them), and the
	// DM conversation path has already returned before this lookup runs.
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		return nil
	}
	setting, ok := c.autoReplySettings.GetCached(channel.Id)
	if !ok {
		return nil
	}
	return &setting
}

// handleAutoReply fires the configured agent for a post that did not @-mention
// any agent, by synthesizing the mention on a cloned post and delegating to
// handleMentions (the HandleLoopInAgent technique). The posting user's
// permissions and usage restrictions apply, and conversation creation, tool
// running, streaming, and CheckUsageRestrictions are all reused.
//
// All conditions that prevent a reply return an error wrapping ErrNoResponse so
// MessageHasBeenPosted logs them at debug, not error, level.
func (c *Conversations) handleAutoReply(ctx context.Context, setting *autoreply.Setting, post *model.Post, postingUser *model.User, channel *model.Channel) error {
	// Mode gate: root_posts fires only for top-level posts, ambient_threads
	// only for thread replies; threads and ambient fire for both. A mode this
	// version does not know never replies.
	switch setting.Mode {
	case autoreply.ModeRootPosts:
		if post.RootId != "" {
			return fmt.Errorf("auto-reply mode root_posts ignores thread replies: %w", ErrNoResponse)
		}
	case autoreply.ModeAmbientThreads:
		if post.RootId == "" {
			return fmt.Errorf("auto-reply mode ambient_threads ignores top-level posts: %w", ErrNoResponse)
		}
	case autoreply.ModeThreads, autoreply.ModeAmbient:
	default:
		return fmt.Errorf("unknown auto-reply mode %q: %w", setting.Mode, ErrNoResponse)
	}

	// Re-check the license at trigger time; fail closed on a nil checker.
	if !c.licenseChecker.Allows(enterprise.CapChannelAutoReply) {
		return fmt.Errorf("channel agent auto-reply is available at Enterprise Advanced and above: %w", ErrNoResponse)
	}
	if setting.Mode.IsAmbient() && !c.ambientAutoReplyEnabled() {
		return fmt.Errorf("ambient auto-reply is disabled by the system admin: %w", ErrNoResponse)
	}

	// The bot may have been deleted or restricted after the setting was
	// written; both are expected staleness and decline quietly.
	bot := c.bots.GetBotByID(setting.BotID)
	if bot == nil {
		return fmt.Errorf("auto-reply bot no longer exists: %w", ErrNoResponse)
	}

	if setting.Mode.IsAmbient() {
		// Gate the classifier call so restricted users never trigger LLM spend.
		if restrictionErr := c.bots.CheckUsageRestrictions(ctx, post.UserId, bot, channel); restrictionErr != nil {
			return fmt.Errorf("auto-reply bot unavailable for user/channel: %v: %w", restrictionErr, ErrNoResponse)
		}
		thread, threadErr := c.ambientThreadPosts(post)
		if threadErr != nil {
			return fmt.Errorf("ambient auto-reply failed to load thread: %v: %w", threadErr, ErrNoResponse)
		}
		if setting.Mode == autoreply.ModeAmbientThreads && !threadHasReplyFrom(thread, bot.GetMMBot().UserId) {
			return fmt.Errorf("auto-reply mode ambient_threads ignores threads the agent has not replied in: %w", ErrNoResponse)
		}
		if classErr := c.classifyAmbientReply(ctx, bot, setting, post, postingUser, channel, thread); classErr != nil {
			return classErr
		}
	}

	err := c.handleMentions(ctx, bot, cloneWithAgentMention(post, bot.GetMMBot().Username), postingUser, channel)
	if errors.Is(err, bots.ErrUsageRestriction) {
		return fmt.Errorf("auto-reply bot unavailable for user/channel: %v: %w", err, ErrNoResponse)
	}
	return err
}
