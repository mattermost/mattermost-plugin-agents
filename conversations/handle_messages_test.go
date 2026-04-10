// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
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

	t.Run("don't respond to webhooks without activate_ai", func(t *testing.T) {
		post := &model.Post{
			UserId:    "userid",
			ChannelId: "channelid",
		}
		post.AddProp("from_webhook", true)
		err := e.conversations.handleMessages(post)
		require.ErrorIs(t, err, ErrNoResponse)
	})

	t.Run("don't respond to webhooks with activate_ai set to false", func(t *testing.T) {
		post := &model.Post{
			UserId:    "userid",
			ChannelId: "channelid",
		}
		post.AddProp("from_webhook", true)
		post.AddProp("activate_ai", false)
		err := e.conversations.handleMessages(post)
		require.ErrorIs(t, err, ErrNoResponse)
	})

	t.Run("don't respond to webhooks with activate_ai set to string false", func(t *testing.T) {
		post := &model.Post{
			UserId:    "userid",
			ChannelId: "channelid",
		}
		post.AddProp("from_webhook", true)
		post.AddProp("activate_ai", "false")
		err := e.conversations.handleMessages(post)
		require.ErrorIs(t, err, ErrNoResponse)
	})
}

func TestIsActivateAISet(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  bool
	}{
		{"nil prop", nil, false},
		{"bool true", true, true},
		{"bool false", false, false},
		{"string true", "true", true},
		{"string false", "false", false},
		{"empty string", "", false},
		{"integer", 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			post := &model.Post{}
			if tc.value != nil {
				post.AddProp(ActivateAIProp, tc.value)
			}
			require.Equal(t, tc.want, isActivateAISet(post))
		})
	}
}

func TestIsAutomatedInvoker(t *testing.T) {
	tests := []struct {
		name        string
		postingUser *model.User
		want        bool
	}{
		{"nil user", nil, false},
		{"human user", &model.User{Id: "u1", IsBot: false}, false},
		{"bot user", &model.User{Id: "b1", IsBot: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAutomatedInvoker(tt.postingUser)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestComputeAllowToolsInChannel(t *testing.T) {
	tests := []struct {
		name          string
		configEnabled bool
		want          bool
	}{
		{"config disabled", false, false},
		{"config enabled", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeAllowToolsInChannel(tt.configEnabled)
			require.Equal(t, tt.want, got)
		})
	}
}
