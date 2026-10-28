// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

// AgentMentionReminderPostType is the custom post type used for the ephemeral
// "you must @mention an agent" reminder rendered by the webapp.
const AgentMentionReminderPostType = "custom_agent_mention_reminder"

// Prop keys carried on the custom post for the webapp to read when rendering.
const (
	AgentMentionReminderBotUserIDProp      = "bot_user_id"
	AgentMentionReminderBotUsernameProp    = "bot_username"
	AgentMentionReminderBotDisplayNameProp = "bot_display_name"
	AgentMentionReminderTargetPostIDProp   = "target_post_id"
)

const (
	// agentMentionReminderOptOutKeyPrefix keys the per-user preference that
	// turns the reminder off in every thread.
	agentMentionReminderOptOutKeyPrefix = "agent_mention_reminder_optout_"

	// agentMentionReminderShownKeyPrefix keys the per-user, per-thread marker
	// recording that the reminder was already shown in that thread.
	agentMentionReminderShownKeyPrefix = "agent_mention_reminder_shown_"
)

// agentMentionReminderShownTTL bounds how long the per-thread "already shown"
// marker lives so markers for abandoned threads expire instead of growing
// without bound. It is deliberately generous: re-showing the reminder once in a
// thread a user returns to after this long is harmless.
const agentMentionReminderShownTTL = 30 * 24 * time.Hour

func agentMentionReminderOptOutKey(userID string) string {
	return agentMentionReminderOptOutKeyPrefix + userID
}

func agentMentionReminderShownKey(userID, rootID string) string {
	return agentMentionReminderShownKeyPrefix + userID + "_" + rootID
}

// LoadAgentMentionReminderOptOut reports whether the user turned off the
// "you must @mention an agent" reminder for every thread. A missing preference
// means the reminder is still enabled.
func LoadAgentMentionReminderOptOut(client mmapi.Client, userID string) (bool, error) {
	var optedOut bool
	if err := client.KVGet(agentMentionReminderOptOutKey(userID), &optedOut); err != nil {
		if mmapi.IsKVNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to load agent mention reminder preference: %w", err)
	}
	return optedOut, nil
}

// SaveAgentMentionReminderOptOut records that the user never wants to see the
// reminder again, in any thread.
func SaveAgentMentionReminderOptOut(client mmapi.Client, userID string) error {
	if err := client.KVSet(agentMentionReminderOptOutKey(userID), true); err != nil {
		return fmt.Errorf("failed to save agent mention reminder preference: %w", err)
	}
	return nil
}

// agentMentionReminderAlreadyShown reports whether the reminder was already
// shown to the user in the thread rooted at rootID.
func (c *Conversations) agentMentionReminderAlreadyShown(userID, rootID string) (bool, error) {
	var shown bool
	if err := c.mmClient.KVGet(agentMentionReminderShownKey(userID, rootID), &shown); err != nil {
		if mmapi.IsKVNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return shown, nil
}

// claimAgentMentionReminderShown atomically records that the reminder is being
// shown to the user in the thread. It returns false when another post already
// claimed it, so the reminder shows at most once per user per thread even
// across concurrent events or HA nodes.
func (c *Conversations) claimAgentMentionReminderShown(userID, rootID string) (bool, error) {
	return c.mmClient.KVCompareAndSetWithExpiry(
		agentMentionReminderShownKey(userID, rootID), nil, true, agentMentionReminderShownTTL,
	)
}

// maybeNotifyAgentMentionNeeded posts an ephemeral reminder to the user when
// their thread reply did not @mention an agent but the immediately preceding
// post in the thread was authored by an AI agent. The ephemeral uses a custom
// post type so the webapp can render an inline "click here to loop in" link.
//
// This is a no-op for top-level posts, DM channels, threads whose previous
// post was authored by a human, and threads whose previous post was authored
// by the agent with user ID skipBotID (when non-empty).
func (c *Conversations) maybeNotifyAgentMentionNeeded(ctx context.Context, post *model.Post, channel *model.Channel, skipBotID string) {
	if post == nil || channel == nil {
		return
	}
	if post.RootId == "" {
		return
	}
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		return
	}
	// The reminder points at channel mentions and loop-in, which are
	// available at Professional and above.
	if !c.licenseChecker.Allows(enterprise.CapMultiplayerChannels) {
		return
	}

	// Respect a user who turned the reminder off in every thread.
	optedOut, err := LoadAgentMentionReminderOptOut(c.mmClient, post.UserId)
	if err != nil {
		c.mmClient.LogDebug("agent mention reminder: failed to load user preference", "error", err.Error(), "user_id", post.UserId)
		return
	}
	if optedOut {
		return
	}

	// Skip the thread fetch below when the reminder was already shown in this
	// thread; the atomic claim before sending is the authoritative guard.
	alreadyShown, err := c.agentMentionReminderAlreadyShown(post.UserId, post.RootId)
	if err != nil {
		c.mmClient.LogDebug("agent mention reminder: failed to load shown marker", "error", err.Error(), "post_id", post.Id, "user_id", post.UserId)
		return
	}
	if alreadyShown {
		return
	}

	prev, err := c.findPreviousThreadPost(post)
	if err != nil {
		c.mmClient.LogDebug("agent mention reminder: failed to load thread", "error", err.Error(), "post_id", post.Id)
		return
	}
	if prev == nil || (skipBotID != "" && prev.UserId == skipBotID) {
		return
	}

	bot := c.bots.GetBotByID(prev.UserId)
	if bot == nil {
		return
	}

	mmBot := bot.GetMMBot()
	if mmBot == nil {
		return
	}
	if err = c.bots.CheckUsageRestrictions(ctx, post.UserId, bot, channel); err != nil {
		c.mmClient.LogDebug(
			"agent mention reminder: bot unavailable for user/channel",
			"error", err.Error(),
			"post_id", post.Id,
			"user_id", post.UserId,
			"bot_username", mmBot.Username,
		)
		return
	}

	fallback := "To respond to an agent you must @mention them."
	if c.i18n != nil {
		T := i18n.LocalizerFunc(c.i18n, c.fallbackLocale(""))
		fallback = T("agents.agent_mention_reminder_fallback", fallback)
	}

	ephemeral := &model.Post{
		ChannelId: post.ChannelId,
		RootId:    post.RootId,
		Message:   fallback,
	}
	// Ephemeral posts cannot set the Post.Type directly (the server uses it to
	// mark the post ephemeral). Custom post types for ephemerals are signaled
	// via the "type" prop instead, which the webapp maps to the registered
	// custom post type component.
	ephemeral.AddProp("type", AgentMentionReminderPostType)
	ephemeral.AddProp(AgentMentionReminderBotUserIDProp, mmBot.UserId)
	ephemeral.AddProp(AgentMentionReminderBotUsernameProp, mmBot.Username)
	ephemeral.AddProp(AgentMentionReminderBotDisplayNameProp, mmBot.DisplayName)
	ephemeral.AddProp(AgentMentionReminderTargetPostIDProp, post.Id)

	// Show the reminder at most once per user per thread. Claim the marker
	// atomically so concurrent replies or HA peers can't double-post it.
	claimed, err := c.claimAgentMentionReminderShown(post.UserId, post.RootId)
	if err != nil {
		c.mmClient.LogDebug("agent mention reminder: failed to record shown marker", "error", err.Error(), "post_id", post.Id, "user_id", post.UserId)
		return
	}
	if !claimed {
		return
	}

	c.mmClient.SendEphemeralPost(post.UserId, ephemeral)
}

// findPreviousThreadPost uses thread order to break same-timestamp ties.
func (c *Conversations) findPreviousThreadPost(post *model.Post) (*model.Post, error) {
	thread, err := c.mmClient.GetPostThread(post.RootId)
	if err != nil {
		return nil, err
	}
	if thread == nil {
		return nil, nil
	}

	orderIndex := make(map[string]int, len(thread.Order))
	for i, id := range thread.Order {
		orderIndex[id] = i
	}
	currentIndex, currentInOrder := orderIndex[post.Id]

	isLater := func(candidate, current *model.Post) bool {
		if candidate.CreateAt != current.CreateAt {
			return candidate.CreateAt > current.CreateAt
		}

		candidateIndex, candidateOK := orderIndex[candidate.Id]
		currentPostIndex, currentOK := orderIndex[current.Id]
		if candidateOK && currentOK && candidateIndex != currentPostIndex {
			return candidateIndex > currentPostIndex
		}

		return candidate.Id > current.Id
	}

	var prev *model.Post
	for id, candidate := range thread.Posts {
		if candidate == nil || id == post.Id {
			continue
		}
		if candidate.CreateAt > post.CreateAt {
			continue
		}
		if candidate.CreateAt == post.CreateAt && currentInOrder {
			candidateIndex, ok := orderIndex[id]
			if !ok || candidateIndex >= currentIndex {
				continue
			}
		}
		if candidate.CreateAt == post.CreateAt && !currentInOrder && candidate.Id >= post.Id {
			continue
		}
		if prev == nil || isLater(candidate, prev) {
			prev = candidate
		}
	}

	return prev, nil
}
