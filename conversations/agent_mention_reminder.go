// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
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

// maybeNotifyAgentMentionNeeded posts an ephemeral reminder to the user when
// their thread reply did not @mention an agent but the immediately preceding
// post in the thread was authored by an AI agent. The ephemeral uses a custom
// post type so the webapp can render an inline "click here to loop in" link.
//
// This is a no-op for top-level posts, DM channels, and threads whose previous
// post was authored by a human.
func (c *Conversations) maybeNotifyAgentMentionNeeded(post *model.Post, channel *model.Channel) {
	if post == nil || channel == nil {
		return
	}
	if post.RootId == "" {
		return
	}
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		return
	}

	prev, err := c.findPreviousThreadPost(post)
	if err != nil {
		c.mmClient.LogDebug("agent mention reminder: failed to load thread", "error", err.Error(), "post_id", post.Id)
		return
	}
	if prev == nil {
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
	if err := c.bots.CheckUsageRestrictions(post.UserId, bot, channel); err != nil {
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
