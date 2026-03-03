// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

type TestEnvironment struct {
	conversations *Conversations
	mockAPI       *plugintest.API
	bots          *bots.MMBots
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
	botsService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)

	conversations := &Conversations{
		mmClient: mmClient,
		bots:     botsService,
	}

	return &TestEnvironment{
		conversations: conversations,
		mockAPI:       mockAPI,
		bots:          botsService,
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
