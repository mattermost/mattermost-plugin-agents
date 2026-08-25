// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost/server/public/model"
)

var (
	ErrLoopInNotPostOwner       = errors.New("only the post author can loop in an agent")
	ErrLoopInNotThreadReply     = errors.New("loop in is only available for thread replies")
	ErrLoopInUnsupportedChannel = errors.New("loop in is not available in direct or group messages")
	ErrLoopInAlreadyMentioned   = errors.New("post already mentions an agent")
	ErrLoopInWrongAgent         = errors.New("previous post in thread was not authored by the requested agent")
	ErrLoopInNoAgentContext     = errors.New("no agent context for this thread reply")
)

// HandleLoopInAgent processes the user's thread reply through the conversation
// flow when they click "loop in" on the agent mention reminder.
func (c *Conversations) HandleLoopInAgent(ctx context.Context, userID string, bot *bots.Bot, post *model.Post, channel *model.Channel) error {
	if post.UserId != userID {
		return ErrLoopInNotPostOwner
	}
	if post.RootId == "" {
		return ErrLoopInNotThreadReply
	}
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		return ErrLoopInUnsupportedChannel
	}
	if c.bots.GetBotMentioned(post.Message) != nil {
		return ErrLoopInAlreadyMentioned
	}

	prev, err := c.findPreviousThreadPost(post)
	if err != nil {
		return fmt.Errorf("failed to resolve thread agent context: %w", err)
	}
	if prev == nil {
		return ErrLoopInNoAgentContext
	}
	threadBot := c.bots.GetBotByID(prev.UserId)
	if threadBot == nil {
		return ErrLoopInNoAgentContext
	}

	requestedMMBot := bot.GetMMBot()
	threadMMBot := threadBot.GetMMBot()
	if requestedMMBot == nil || requestedMMBot.Username == "" || threadMMBot == nil || requestedMMBot.UserId != threadMMBot.UserId {
		return ErrLoopInWrongAgent
	}

	postingUser, err := c.mmClient.GetUser(userID)
	if err != nil {
		return fmt.Errorf("unable to get user: %w", err)
	}

	loopInPost := post.Clone()
	loopInPost.Message = "@" + requestedMMBot.Username
	if message := strings.TrimSpace(post.Message); message != "" {
		loopInPost.Message += " " + message
	}

	return c.handleMentions(ctx, bot, loopInPost, postingUser, channel)
}
