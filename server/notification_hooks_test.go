// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const botUserID = "agent-bot-user-id"

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
			name: "blocks AI agent threaded reply in a regular channel",
			notification: &model.PushNotification{
				PostId:      "post-1",
				SenderId:    botUserID,
				RootId:      "parent-post-1",
				ChannelType: model.ChannelTypeOpen,
			},
			wantBlocked: true,
		},
		{
			name: "blocks AI agent custom_llmbot root post in a regular channel",
			notification: &model.PushNotification{
				PostId:      "post-2",
				SenderId:    botUserID,
				PostType:    "custom_llmbot",
				ChannelType: model.ChannelTypeOpen,
			},
			wantBlocked: true,
		},
		{
			name: "blocks AI agent root post in a DM channel (MM-66720)",
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
			name: "does NOT block a non-bot user's post in a DM",
			notification: &model.PushNotification{
				PostId:      "post-5",
				SenderId:    "regular-user",
				ChannelType: model.ChannelTypeDirect,
			},
			wantBlocked: false,
		},
		{
			name: "does NOT block a custom_llmbot post from an unknown sender",
			notification: &model.PushNotification{
				PostId:      "post-6",
				SenderId:    "regular-user",
				PostType:    "custom_llmbot",
				ChannelType: model.ChannelTypeOpen,
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

func TestEmailNotificationWillBeSent_BotsCacheUninitialized(t *testing.T) {
	p := &Plugin{}
	notification := &model.EmailNotification{
		PostId:          "post-1",
		SenderId:        botUserID,
		IsDirectMessage: true,
		RootId:          "parent-post-1",
	}

	got, reason := p.EmailNotificationWillBeSent(notification)
	require.NotNil(t, got, "must pass through when bots service is not initialized")
	require.Empty(t, reason)
}

const (
	editedPostID       = "editedpost123456789012345"
	editedPostTurnID   = "editedturn123456789012345"
	editedPostOldText  = "the original question"
	editedPostNewText  = "the question as it now reads"
	editedPostChanneID = "editedchan123456789012345"
)

// singleTurnStore is a conversation store holding one turn. Only the two
// operations the edited-post path uses are implemented; anything else is a
// programming error and panics through the embedded nil interface.
type singleTurnStore struct {
	conversation.Store
	turn *store.Turn
}

func (s *singleTurnStore) GetTurnByPostID(postID string) (*store.Turn, error) {
	if s.turn.PostID == nil || *s.turn.PostID != postID {
		return nil, nil
	}
	copied := *s.turn
	return &copied, nil
}

func (s *singleTurnStore) UpdateTurnContent(id string, content json.RawMessage) error {
	if id != s.turn.ID {
		return nil
	}
	s.turn.Content = content
	return nil
}

// setupEditedPostPlugin wires a Plugin around a conversation service holding a
// single user turn anchored to editedPostID.
func setupEditedPostPlugin(t *testing.T) (*Plugin, *singleTurnStore) {
	t.Helper()

	postID := editedPostID
	content, err := json.Marshal([]conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: editedPostOldText},
		{Type: conversation.BlockTypeImage, FileID: "file1", Filename: "shot.png", MimeType: "image/png"},
	})
	require.NoError(t, err)

	turnStore := &singleTurnStore{turn: &store.Turn{
		ID:             editedPostTurnID,
		ConversationID: "conv-edited",
		PostID:         &postID,
		Role:           "user",
		Content:        content,
		Sequence:       1,
	}}

	convs := conversations.New(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	convs.SetConversationService(conversation.NewService(turnStore, nil, nil, nil))

	mockAPI := &plugintest.API{}
	for i := 1; i <= 10; i++ {
		args := make([]any, i)
		for j := range args {
			args[j] = mock.Anything
		}
		mockAPI.On("LogError", args...).Maybe()
		mockAPI.On("LogWarn", args...).Maybe()
	}

	return &Plugin{
		pluginAPI:            pluginapi.NewClient(mockAPI, nil),
		conversationsService: convs,
	}, turnStore
}

func TestMessageHasBeenUpdatedTurnContent(t *testing.T) {
	tests := []struct {
		name     string
		newPost  *model.Post
		oldPost  *model.Post
		validate func(t *testing.T, blocks []conversation.ContentBlock)
	}{
		{
			name: "message changed",
			newPost: &model.Post{
				Id: editedPostID, ChannelId: editedPostChanneID, Message: editedPostNewText,
			},
			oldPost: &model.Post{
				Id: editedPostID, ChannelId: editedPostChanneID, Message: editedPostOldText,
			},
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.Equal(t, editedPostNewText, conversation.TextContent(blocks))
				require.Len(t, blocks, 2)
				assert.Equal(t, "file1", blocks[1].FileID,
					"attachments on the turn are left as stored")
			},
		},
		{
			name: "message unchanged",
			newPost: &model.Post{
				Id: editedPostID, ChannelId: editedPostChanneID, Message: editedPostOldText,
			},
			oldPost: &model.Post{
				Id: editedPostID, ChannelId: editedPostChanneID, Message: editedPostOldText,
			},
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.Equal(t, editedPostOldText, conversation.TextContent(blocks))
			},
		},
		{
			name: "post the conversation does not reference",
			newPost: &model.Post{
				Id: "otherpost12345678901234567", ChannelId: editedPostChanneID, Message: editedPostNewText,
			},
			oldPost: &model.Post{
				Id: "otherpost12345678901234567", ChannelId: editedPostChanneID, Message: editedPostOldText,
			},
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.Equal(t, editedPostOldText, conversation.TextContent(blocks))
			},
		},
		{
			name:    "no post supplied",
			newPost: nil,
			oldPost: nil,
			validate: func(t *testing.T, blocks []conversation.ContentBlock) {
				assert.Equal(t, editedPostOldText, conversation.TextContent(blocks))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, turnStore := setupEditedPostPlugin(t)

			p.MessageHasBeenUpdated(&plugin.Context{}, tt.newPost, tt.oldPost)

			blocks, err := conversation.UnmarshalBlocks(turnStore.turn.Content)
			require.NoError(t, err)
			tt.validate(t, blocks)
		})
	}
}
