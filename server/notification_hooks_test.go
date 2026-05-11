// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// botUserID is the ID of an AI agent bot used by the tests below.
const botUserID = "agent-bot-user-id"

// pluginWithAgentBot returns a *Plugin whose bot cache contains a single AI
// agent bot with UserId == botUserID, matching what the real plugin produces
// for a configured agent.
func pluginWithAgentBot() *Plugin {
	mmBots := &bots.MMBots{}
	mmBots.SetBotsForTesting([]*bots.Bot{
		bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: botUserID}, nil),
	})
	return &Plugin{bots: mmBots}
}

func TestNotificationWillBePushed(t *testing.T) {
	tests := []struct {
		name         string
		notification *model.PushNotification
		wantBlocked  bool
	}{
		{
			name: "blocks AI agent threaded reply (existing behaviour)",
			notification: &model.PushNotification{
				PostId:   "post-1",
				SenderId: botUserID,
				RootId:   "parent-post-1",
				PostType: "custom_llmbot",
			},
			wantBlocked: true,
		},
		{
			name: "blocks AI agent root DM post created by an RHS action (MM-66720)",
			notification: &model.PushNotification{
				PostId:      "post-2",
				SenderId:    botUserID,
				RootId:      "",
				PostType:    "custom_llmbot",
				ChannelType: model.ChannelTypeDirect,
			},
			wantBlocked: true,
		},
		{
			name: "blocks AI agent post in a DM channel without custom_llmbot type",
			notification: &model.PushNotification{
				PostId:      "post-3",
				SenderId:    botUserID,
				ChannelType: model.ChannelTypeDirect,
			},
			wantBlocked: true,
		},
		{
			name: "does NOT block AI agent root post in a regular channel (e.g. meeting postback)",
			notification: &model.PushNotification{
				PostId:      "post-4",
				SenderId:    botUserID,
				PostType:    "custom_llm_postback",
				ChannelType: model.ChannelTypeOpen,
			},
			wantBlocked: false,
		},
		{
			name: "does NOT block a non-bot user's post",
			notification: &model.PushNotification{
				PostId:      "post-5",
				SenderId:    "regular-user",
				ChannelType: model.ChannelTypeDirect,
			},
			wantBlocked: false,
		},
		{
			name: "passes through when PostId is empty (no post to inspect)",
			notification: &model.PushNotification{
				SenderId: botUserID,
			},
			wantBlocked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := pluginWithAgentBot()
			got, reason := p.NotificationWillBePushed(tt.notification, "recipient-user")
			if tt.wantBlocked {
				require.Nil(t, got, "expected notification to be blocked")
				require.NotEmpty(t, reason, "expected a non-empty block reason")
			} else {
				require.Equal(t, tt.notification, got, "expected notification to pass through unchanged")
				require.Empty(t, reason)
			}
		})
	}
}

func TestEmailNotificationWillBeSent(t *testing.T) {
	tests := []struct {
		name         string
		notification *model.EmailNotification
		wantBlocked  bool
	}{
		{
			name: "blocks AI agent threaded reply",
			notification: &model.EmailNotification{
				PostId:   "post-1",
				SenderId: botUserID,
				RootId:   "parent-post-1",
			},
			wantBlocked: true,
		},
		{
			name: "blocks AI agent root DM post (MM-66720)",
			notification: &model.EmailNotification{
				PostId:          "post-2",
				SenderId:        botUserID,
				IsDirectMessage: true,
			},
			wantBlocked: true,
		},
		{
			name: "does NOT block AI agent root post in a non-DM channel",
			notification: &model.EmailNotification{
				PostId:   "post-3",
				SenderId: botUserID,
			},
			wantBlocked: false,
		},
		{
			name: "does NOT block a non-bot sender",
			notification: &model.EmailNotification{
				PostId:          "post-4",
				SenderId:        "regular-user",
				IsDirectMessage: true,
			},
			wantBlocked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := pluginWithAgentBot()
			got, reason := p.EmailNotificationWillBeSent(tt.notification)
			if tt.wantBlocked {
				require.Nil(t, got, "expected email notification to be blocked")
				require.NotEmpty(t, reason, "expected a non-empty block reason")
			} else {
				require.NotNil(t, got, "expected email notification content to be returned")
				require.Empty(t, reason)
			}
		})
	}
}

func TestNotificationWillBePushed_BotsCacheUninitialized(t *testing.T) {
	p := &Plugin{}
	notification := &model.PushNotification{
		PostId:   "post-1",
		SenderId: botUserID,
		RootId:   "parent-post-1",
		PostType: "custom_llmbot",
	}

	got, reason := p.NotificationWillBePushed(notification, "recipient-user")
	require.Equal(t, notification, got, "must pass through when bots service is not initialized")
	require.Empty(t, reason)
}
