// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversations"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/i18n"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

const (
	reminderBotID       = "bot-id"
	reminderBotUsername = "test-bot"
	reminderBotDisplay  = "Test Bot"
	reminderUserID      = "user-id"
	reminderOtherUserID = "other-user-id"
	reminderChannelID   = "channel-id"
	reminderRootID      = "root-id"
	reminderReplyID     = "reply-id"
)

type reminderFixture struct {
	conv       *conversations.Conversations
	client     *fakeMMClient
	botService *bots.MMBots
}

func newReminderFixture(t *testing.T) *reminderFixture {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{}).Maybe()
	pluginClient := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginClient)

	botService := bots.New(mockAPI, pluginClient, licenseChecker, nil, &http.Client{}, nil)
	bot := bots.NewBot(
		llm.BotConfig{ID: reminderBotID, Name: reminderBotUsername, DisplayName: reminderBotDisplay},
		llm.ServiceConfig{},
		&model.Bot{UserId: reminderBotID, Username: reminderBotUsername, DisplayName: reminderBotDisplay},
		nil,
	)
	botService.SetBotsForTesting([]*bots.Bot{bot})

	contextBuilder := llmcontext.NewLLMContextBuilder(pluginClient, nil, nil, &testConfigProvider{})

	client := &fakeMMClient{
		users: map[string]*model.User{
			reminderUserID:      {Id: reminderUserID, Username: "user", Locale: "en"},
			reminderOtherUserID: {Id: reminderOtherUserID, Username: "other", Locale: "en"},
			reminderBotID:       {Id: reminderBotID, Username: reminderBotUsername, IsBot: true, Locale: "en"},
		},
		channels: map[string]*model.Channel{},
	}

	conv := conversations.New(nil, client, nil, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, &testToolCallingConfig{})

	return &reminderFixture{
		conv:       conv,
		client:     client,
		botService: botService,
	}
}

func (f *reminderFixture) setChannel(ch *model.Channel) {
	f.client.channels[ch.Id] = ch
}

func (f *reminderFixture) setThread(rootID string, posts ...*model.Post) {
	if f.client.postThreads == nil {
		f.client.postThreads = map[string]*model.PostList{}
	}
	postsByID := map[string]*model.Post{}
	order := make([]string, 0, len(posts))
	for _, p := range posts {
		postsByID[p.Id] = p
		order = append(order, p.Id)
	}
	list := &model.PostList{Order: order, Posts: postsByID}
	for _, p := range posts {
		f.client.postThreads[p.Id] = list
	}
	if rootID != "" && f.client.postThreads[rootID] == nil {
		f.client.postThreads[rootID] = list
	}
}

func TestMessageHasBeenPostedSendsReminderWhenPreviousPostIsAgent(t *testing.T) {
	cases := []struct {
		name             string
		channel          *model.Channel
		previousUserID   string
		replyHasMention  bool
		replyHasRootID   bool
		expectEphemeral  bool
		expectReplyTopic string
	}{
		{
			name:            "thread reply after agent post triggers reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			previousUserID:  reminderBotID,
			replyHasMention: false,
			replyHasRootID:  true,
			expectEphemeral: true,
		},
		{
			name:            "thread reply after human post does not trigger reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			previousUserID:  reminderOtherUserID,
			replyHasMention: false,
			replyHasRootID:  true,
			expectEphemeral: false,
		},
		{
			name:            "top-level post does not trigger reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			previousUserID:  reminderBotID,
			replyHasMention: false,
			replyHasRootID:  false,
			expectEphemeral: false,
		},
		{
			name:            "thread reply in DM channel does not trigger reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeDirect},
			previousUserID:  reminderBotID,
			replyHasMention: false,
			replyHasRootID:  true,
			expectEphemeral: false,
		},
		{
			name:            "thread reply that already mentions the bot does not trigger reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			previousUserID:  reminderBotID,
			replyHasMention: true,
			replyHasRootID:  true,
			expectEphemeral: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newReminderFixture(t)
			fix.setChannel(tc.channel)

			rootPost := &model.Post{
				Id:        reminderRootID,
				ChannelId: tc.channel.Id,
				UserId:    reminderUserID,
				CreateAt:  100,
				Message:   "kicking off",
			}
			previousPost := &model.Post{
				Id:        "prev-post-id",
				ChannelId: tc.channel.Id,
				UserId:    tc.previousUserID,
				RootId:    reminderRootID,
				CreateAt:  200,
				Message:   "previous message",
			}

			replyMessage := "thanks!"
			if tc.replyHasMention {
				replyMessage = "@" + reminderBotUsername + " thanks!"
			}
			rootIDForReply := reminderRootID
			if !tc.replyHasRootID {
				rootIDForReply = ""
			}
			reply := &model.Post{
				Id:        reminderReplyID,
				ChannelId: tc.channel.Id,
				UserId:    reminderUserID,
				RootId:    rootIDForReply,
				CreateAt:  300,
				Message:   replyMessage,
			}

			if tc.replyHasRootID {
				fix.setThread(reminderRootID, rootPost, previousPost, reply)
			} else {
				fix.setThread(reminderReplyID, reply)
			}

			fix.conv.MessageHasBeenPosted(nil, reply)

			if tc.expectEphemeral {
				require.Len(t, fix.client.ephemeralPosts, 1, "expected one ephemeral reminder")
				require.Equal(t, reminderUserID, fix.client.ephemeralPostUserIDs[0])
				ephemeral := fix.client.ephemeralPosts[0]
				require.Equal(t, conversations.AgentMentionReminderPostType, ephemeral.GetProp("type"))
				require.Equal(t, tc.channel.Id, ephemeral.ChannelId)
				require.Equal(t, reminderRootID, ephemeral.RootId)
				require.Equal(t, reminderBotID, ephemeral.GetProp(conversations.AgentMentionReminderBotUserIDProp))
				require.Equal(t, reminderBotUsername, ephemeral.GetProp(conversations.AgentMentionReminderBotUsernameProp))
				require.Equal(t, reminderBotDisplay, ephemeral.GetProp(conversations.AgentMentionReminderBotDisplayNameProp))
				require.Equal(t, reminderReplyID, ephemeral.GetProp(conversations.AgentMentionReminderTargetPostIDProp))
				require.NotEmpty(t, ephemeral.Message)
			} else {
				require.Empty(t, fix.client.ephemeralPosts, "expected no ephemeral reminder")
			}
		})
	}
}

func TestMessageHasBeenPostedReminderHandlesNoPreviousPost(t *testing.T) {
	fix := newReminderFixture(t)
	channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen}
	fix.setChannel(channel)

	reply := &model.Post{
		Id:        reminderReplyID,
		ChannelId: channel.Id,
		UserId:    reminderUserID,
		RootId:    reminderRootID,
		CreateAt:  100,
		Message:   "lonely reply",
	}

	fix.setThread(reminderRootID, reply)

	fix.conv.MessageHasBeenPosted(nil, reply)

	require.Empty(t, fix.client.ephemeralPosts)
}
