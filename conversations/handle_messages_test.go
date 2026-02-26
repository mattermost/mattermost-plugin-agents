// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

type testConfigProvider struct {
	enableChannelMentionToolCalling bool
	allowNativeWebSearchInChannels  bool
	disableDMs                      bool
}

func (c *testConfigProvider) EnableChannelMentionToolCalling() bool {
	return c.enableChannelMentionToolCalling
}

func (c *testConfigProvider) AllowNativeWebSearchInChannels() bool {
	return c.allowNativeWebSearchInChannels
}

func (c *testConfigProvider) DisableDMs() bool {
	return c.disableDMs
}

type TestEnvironment struct {
	conversations *Conversations
	mockAPI       *plugintest.API
	bots          *bots.MMBots
	mmClient      *mocks.MockClient
}

func (e *TestEnvironment) Cleanup(t *testing.T) {
	if e.mockAPI != nil {
		e.mockAPI.AssertExpectations(t)
	}
}

func SetupTestEnvironment(t *testing.T) *TestEnvironment {
	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	mmClient := mocks.NewMockClient(t)

	licenseChecker := enterprise.NewLicenseChecker(client)
	botsService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil, nil)

	conversations := &Conversations{
		mmClient: mmClient,
		bots:     botsService,
	}

	return &TestEnvironment{
		conversations: conversations,
		mockAPI:       mockAPI,
		bots:          botsService,
		mmClient:      mmClient,
	}
}

func TestHandleMessages(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	t.Run("don't respond to remote posts", func(t *testing.T) {
		remoteid := "remoteid"
		err := e.conversations.handleMessages(&model.Post{
			UserId:    "userid",
			ChannelId: "channelid",
			RemoteId:  &remoteid,
		})
		require.ErrorIs(t, err, ErrNoResponse)
	})

	t.Run("don't respond to plugins", func(t *testing.T) {
		post := &model.Post{
			UserId:    "userid",
			ChannelId: "channelid",
		}
		post.AddProp("from_plugin", true)
		err := e.conversations.handleMessages(post)
		require.ErrorIs(t, err, ErrNoResponse)
	})

	t.Run("don't respond to webhooks", func(t *testing.T) {
		post := &model.Post{
			UserId:    "userid",
			ChannelId: "channelid",
		}
		post.AddProp("from_webhook", true)
		err := e.conversations.handleMessages(post)
		require.ErrorIs(t, err, ErrNoResponse)
	})
}

func TestHandleMessagesDMDisabled(t *testing.T) {
	const userID = "userid"
	const botUserID = "botuserid"
	channelID := model.GetDMNameFromIds(userID, botUserID)

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	// Set up config provider with DMs disabled
	e.conversations.configProvider = &testConfigProvider{disableDMs: true}

	// Set up a bot so GetBotForDMChannel finds it
	e.bots.SetBotsForTesting([]*bots.Bot{
		bots.NewBot(llm.BotConfig{Name: "ai"}, llm.ServiceConfig{}, &model.Bot{UserId: botUserID, Username: "ai"}, nil),
	})

	// Mock the channel as a DM with the bot
	e.mmClient.EXPECT().GetChannel(channelID).Return(&model.Channel{
		Id:   channelID,
		Type: model.ChannelTypeDirect,
		Name: model.GetDMNameFromIds(userID, botUserID),
	}, nil)

	// Mock GetUser to return a non-bot user
	e.mmClient.EXPECT().GetUser(userID).Return(&model.User{
		Id: userID,
	}, nil)

	err := e.conversations.handleMessages(&model.Post{
		UserId:    userID,
		ChannelId: channelID,
	})
	require.ErrorIs(t, err, ErrNoResponse)
}
