// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
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
	// Mode gate: root_posts fires only for top-level posts; threads fires for both.
	if setting.Mode == autoreply.ModeRootPosts && post.RootId != "" {
		return fmt.Errorf("auto-reply mode root_posts ignores thread replies: %w", ErrNoResponse)
	}

	// Re-check the license at trigger time; fail closed on a nil checker.
	if c.licenseChecker == nil || !c.licenseChecker.IsBasicsLicensed() {
		return fmt.Errorf("auto-reply requires a license: %w", ErrNoResponse)
	}

	// Re-check the bot still exists and is allowed: the bot may have been
	// deleted or restricted after the setting was written. No-op quietly.
	// handleMentions re-runs CheckUsageRestrictions, but a restriction failure
	// surfaced from there would be logged at error level; the pre-check
	// converts the expected "setting went stale" cases into quiet no-ops.
	bot := c.bots.GetBotByID(setting.BotID)
	if bot == nil {
		return fmt.Errorf("auto-reply bot no longer exists: %w", ErrNoResponse)
	}
	if err := c.bots.CheckUsageRestrictions(post.UserId, bot, channel); err != nil {
		return fmt.Errorf("auto-reply bot unavailable for user/channel: %v: %w", err, ErrNoResponse)
	}

	if setting.Mode == autoreply.ModeAmbient {
		if classErr := c.classifyAmbientReply(ctx, bot, setting, post); classErr != nil {
			return classErr
		}
	}

	autoPost := post.Clone()
	autoPost.Message = "@" + bot.GetMMBot().Username
	if message := strings.TrimSpace(post.Message); message != "" {
		autoPost.Message += " " + message
	}

	return c.handleMentions(ctx, bot, autoPost, postingUser, channel)
}
