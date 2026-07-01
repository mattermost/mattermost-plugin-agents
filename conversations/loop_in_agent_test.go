// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestHandleLoopInAgentValidation(t *testing.T) {
	cases := []struct {
		name    string
		userID  string
		bot     *bots.Bot
		post    *model.Post
		channel *model.Channel
		thread  []*model.Post
		wantErr error
	}{
		{
			name:   "rejects non-owner",
			userID: reminderOtherUserID,
			post: &model.Post{
				Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID,
				RootId: reminderRootID, CreateAt: 300, Message: "thanks",
			},
			channel: &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			thread: []*model.Post{
				{Id: reminderRootID, ChannelId: reminderChannelID, UserId: reminderUserID, CreateAt: 100},
				{Id: "prev", ChannelId: reminderChannelID, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 200},
				{Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID, RootId: reminderRootID, CreateAt: 300, Message: "thanks"},
			},
			wantErr: conversations.ErrLoopInNotPostOwner,
		},
		{
			name:   "rejects top-level post",
			userID: reminderUserID,
			post: &model.Post{
				Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID,
				CreateAt: 300, Message: "thanks",
			},
			channel: &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			wantErr: conversations.ErrLoopInNotThreadReply,
		},
		{
			name:   "rejects DM channel",
			userID: reminderUserID,
			post: &model.Post{
				Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID,
				RootId: reminderRootID, CreateAt: 300, Message: "thanks",
			},
			channel: &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeDirect},
			wantErr: conversations.ErrLoopInUnsupportedChannel,
		},
		{
			name:   "rejects post that already mentions agent",
			userID: reminderUserID,
			post: &model.Post{
				Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID,
				RootId: reminderRootID, CreateAt: 300, Message: "@" + reminderBotUsername + " thanks",
			},
			channel: &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			thread: []*model.Post{
				{Id: reminderRootID, ChannelId: reminderChannelID, UserId: reminderUserID, CreateAt: 100},
				{Id: "prev", ChannelId: reminderChannelID, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 200},
				{Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID, RootId: reminderRootID, CreateAt: 300, Message: "@" + reminderBotUsername + " thanks"},
			},
			wantErr: conversations.ErrLoopInAlreadyMentioned,
		},
		{
			name:   "rejects when previous post is human",
			userID: reminderUserID,
			post: &model.Post{
				Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID,
				RootId: reminderRootID, CreateAt: 300, Message: "thanks",
			},
			channel: &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			thread: []*model.Post{
				{Id: reminderRootID, ChannelId: reminderChannelID, UserId: reminderUserID, CreateAt: 100},
				{Id: "prev", ChannelId: reminderChannelID, UserId: reminderOtherUserID, RootId: reminderRootID, CreateAt: 200},
				{Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID, RootId: reminderRootID, CreateAt: 300, Message: "thanks"},
			},
			wantErr: conversations.ErrLoopInNoAgentContext,
		},
		{
			name:   "rejects wrong requested bot",
			userID: reminderUserID,
			bot: bots.NewBot(
				llm.BotConfig{ID: "other-bot", Name: "other-bot"},
				llm.ServiceConfig{},
				&model.Bot{UserId: "other-bot", Username: "other-bot"},
				nil,
			),
			post: &model.Post{
				Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID,
				RootId: reminderRootID, CreateAt: 300, Message: "thanks",
			},
			channel: &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			thread: []*model.Post{
				{Id: reminderRootID, ChannelId: reminderChannelID, UserId: reminderUserID, CreateAt: 100},
				{Id: "prev", ChannelId: reminderChannelID, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 200},
				{Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID, RootId: reminderRootID, CreateAt: 300, Message: "thanks"},
			},
			wantErr: conversations.ErrLoopInWrongAgent,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newReminderFixture(t)
			fix.setChannel(tc.channel)
			if len(tc.thread) > 0 {
				fix.setThread(reminderRootID, tc.thread...)
			}

			bot := tc.bot
			if bot == nil {
				bot = fix.botService.GetBotByID(reminderBotID)
				require.NotNil(t, bot)
			}

			err := fix.conv.HandleLoopInAgent(context.Background(), tc.userID, bot, tc.post, tc.channel)
			require.Error(t, err)
			require.True(t, errors.Is(err, tc.wantErr), "got %v want %v", err, tc.wantErr)
		})
	}
}
