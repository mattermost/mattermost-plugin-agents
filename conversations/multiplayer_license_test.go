// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestChannelMentionLicenseGate(t *testing.T) {
	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			env := setupAutoReplyTestEnv(t, []llm.BotConfig{autoReplyBotConfig()}, dmMakeTextStream("canned reply"))
			env.overrideLicense(enterprisetest.LicenseFor(level))

			post := env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" help me")
			env.conversations.MessageHasBeenPosted(nil, post)

			if level >= enterprise.LevelProfessional {
				require.NotEmpty(t, env.mmClient.createdPosts, "channel mention is available at Professional and above")
				require.Empty(t, env.mmClient.ephemeralPosts)
				return
			}

			require.Empty(t, allConversations(env.convStore), "channel mention must not start a conversation below Professional")
			requireMultiplayerUnavailableReply(t, env.mmClient.createdPosts, env.mmClient.ephemeralPosts, autoReplyBotUserID, post.Id)
		})
	}
}

func TestGroupMessageMentionLicenseGate(t *testing.T) {
	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			env := setupAutoReplyTestEnv(t, []llm.BotConfig{autoReplyBotConfig()}, dmMakeTextStream("canned reply"))
			env.overrideLicense(enterprisetest.LicenseFor(level))
			env.channel.Type = model.ChannelTypeGroup
			env.channel.Name = autoReplyBotUserID + "__" + autoReplyUserID + "__other"

			post := env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" help me")
			env.conversations.MessageHasBeenPosted(nil, post)

			if level >= enterprise.LevelProfessional {
				require.NotEmpty(t, env.mmClient.createdPosts)
				require.Empty(t, env.mmClient.ephemeralPosts)
				return
			}

			requireMultiplayerUnavailableReply(t, env.mmClient.createdPosts, env.mmClient.ephemeralPosts, autoReplyBotUserID, post.Id)
		})
	}
}

func TestDMMentionWorksAtEveryLicenseLevel(t *testing.T) {
	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			env := setupDMTestEnv(t, dmMakeTextStream("hello from dm"))
			overrideMockLicense(env.mockAPI, enterprisetest.LicenseFor(level))

			env.conversations.MessageHasBeenPosted(nil, &model.Post{
				Id:        "dm-post",
				UserId:    env.userID,
				ChannelId: env.channelID,
				Message:   "hello",
			})

			require.NotEmpty(t, env.mmClient.createdPosts, "agent DMs are available at every license level")
			require.Empty(t, env.mmClient.ephemeralPosts)
		})
	}
}

func TestHandleLoopInAgentLicenseGate(t *testing.T) {
	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			fix := newReminderFixture(t)
			channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen, TeamId: reminderTeamID}
			fix.setChannel(channel)
			post := &model.Post{
				Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID,
				RootId: reminderRootID, CreateAt: 300, Message: "thanks",
			}
			fix.setThread(reminderRootID, []*model.Post{
				{Id: reminderRootID, ChannelId: reminderChannelID, UserId: reminderUserID, CreateAt: 100},
				{Id: "prev", ChannelId: reminderChannelID, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 200},
				post,
			}...)
			overrideMockLicense(fix.mockAPI, enterprisetest.LicenseFor(level))

			bot := fix.botService.GetBotByID(reminderBotID)
			require.NotNil(t, bot)

			err := fix.conv.HandleLoopInAgent(context.Background(), reminderUserID, bot, post, channel)

			if level >= enterprise.LevelProfessional {
				if err != nil {
					require.NotContains(t, err.Error(), "Multiplayer agents in channels")
				}
				for _, ephemeral := range fix.client.ephemeralPosts {
					require.NotContains(t, ephemeral.Message, "Professional license")
				}
				return
			}

			require.Error(t, err)
			require.ErrorIs(t, err, conversations.ErrNoResponse)
			requireMultiplayerUnavailableReply(t, fix.client.createdPosts, fix.client.ephemeralPosts, reminderBotID, reminderRootID)
			var licErr *enterprise.LicenseError
			require.ErrorAs(t, err, &licErr)
			require.Equal(t, enterprise.CapMultiplayerChannels, licErr.Capability)
		})
	}
}

func TestHandleLoopInAgentNilCheckerFailsClosed(t *testing.T) {
	fix := newReminderFixture(t)
	fix.client.allowCreatePost = true
	channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen, TeamId: reminderTeamID}
	fix.setChannel(channel)
	post := &model.Post{
		Id: reminderReplyID, ChannelId: reminderChannelID, UserId: reminderUserID,
		RootId: reminderRootID, CreateAt: 300, Message: "thanks",
	}
	fix.setThread(reminderRootID, []*model.Post{
		{Id: reminderRootID, ChannelId: reminderChannelID, UserId: reminderUserID, CreateAt: 100},
		{Id: "prev", ChannelId: reminderChannelID, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 200},
		post,
	}...)

	nilConv := conversations.New(nil, fix.client, nil, nil, fix.botService, nil, nil, nil, nil, nil)
	bot := fix.botService.GetBotByID(reminderBotID)
	require.NotNil(t, bot)

	err := nilConv.HandleLoopInAgent(context.Background(), reminderUserID, bot, post, channel)
	require.Error(t, err)
	require.ErrorIs(t, err, conversations.ErrNoResponse)
	requireMultiplayerUnavailableReply(t, fix.client.createdPosts, fix.client.ephemeralPosts, reminderBotID, reminderRootID)
	var licErr *enterprise.LicenseError
	require.ErrorAs(t, err, &licErr)
}

func TestChannelMentionNilCheckerFailsClosed(t *testing.T) {
	env := setupAutoReplyTestEnv(t, []llm.BotConfig{autoReplyBotConfig()}, dmMakeTextStream("canned reply"))
	nilConv := conversations.New(nil, env.mmClient, nil, nil, env.botService, nil, nil, nil, nil, nil)

	post := env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" help me")
	nilConv.MessageHasBeenPosted(nil, post)

	requireMultiplayerUnavailableReply(t, env.mmClient.createdPosts, env.mmClient.ephemeralPosts, autoReplyBotUserID, post.Id)
}

// requireMultiplayerUnavailableReply asserts the agent answered a mention it
// cannot serve with one visible thread reply naming the required plan.
func requireMultiplayerUnavailableReply(t *testing.T, created, ephemeral []*model.Post, botUserID, rootID string) {
	t.Helper()
	require.Empty(t, ephemeral)
	require.Len(t, created, 1)
	require.Equal(t, botUserID, created[0].UserId)
	require.Equal(t, rootID, created[0].RootId)
	require.Contains(t, created[0].Message, "Professional")
}
