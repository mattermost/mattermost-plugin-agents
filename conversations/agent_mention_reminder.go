// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"github.com/mattermost/mattermost-plugin-agents/i18n"
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
	if channel.Type == model.ChannelTypeDirect {
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

// findPreviousThreadPost returns the post in the same thread as `post` with
// the greatest CreateAt strictly less than post.CreateAt. Returns (nil, nil)
// when no such post exists. Ties on CreateAt are broken by lexicographic Id
// to keep the result stable.
func (c *Conversations) findPreviousThreadPost(post *model.Post) (*model.Post, error) {
	thread, err := c.mmClient.GetPostThread(post.Id)
	if err != nil {
		return nil, err
	}
	if thread == nil {
		return nil, nil
	}

	var prev *model.Post
	for _, p := range thread.Posts {
		if p == nil || p.Id == post.Id {
			continue
		}
		if p.CreateAt > post.CreateAt {
			continue
		}
		if p.CreateAt == post.CreateAt && p.Id >= post.Id {
			continue
		}
		if prev == nil {
			prev = p
			continue
		}
		if p.CreateAt > prev.CreateAt || (p.CreateAt == prev.CreateAt && p.Id > prev.Id) {
			prev = p
		}
	}
	return prev, nil
}
