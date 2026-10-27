// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

const (
	botUserID       = "agent-bot-user-id"
	recipientUserID = "recipient-user"
)

// hookNotificationsClient is a notifications.Client with an in-memory KV.
// The embedded mmapi.Client is nil; the fallback test sets what it reads.
type hookNotificationsClient struct {
	mmapi.Client
	kv     map[string][]byte
	kvErr  error
	posts  map[string]*model.Post
	users  map[string]*model.User
	chans  map[string]*model.Channel
	config *model.Config
	onPush func(*model.PushNotification, string)
}

func newHookNotificationsClient() *hookNotificationsClient {
	return &hookNotificationsClient{kv: map[string][]byte{}, posts: map[string]*model.Post{}, users: map[string]*model.User{}, chans: map[string]*model.Channel{}}
}

func (c *hookNotificationsClient) KVGet(key string, value any) error {
	if c.kvErr != nil {
		return c.kvErr
	}
	raw, ok := c.kv[key]
	if !ok {
		return mmapi.ErrKVNotFound
	}
	if out, ok := value.(*[]byte); ok {
		*out = raw
		return nil
	}
	return json.Unmarshal(raw, value)
}

// KVCompareAndSet follows the server: a nil oldValue requires the key to be
// absent, and a nil newValue deletes it.
func (c *hookNotificationsClient) KVCompareAndSet(key string, oldValue, newValue any) (bool, error) {
	current, ok := c.kv[key]
	if oldValue == nil {
		if ok {
			return false, nil
		}
	} else {
		oldRaw, err := json.Marshal(oldValue)
		if err != nil {
			return false, err
		}
		if !ok || string(current) != string(oldRaw) {
			return false, nil
		}
	}
	if newValue == nil {
		delete(c.kv, key)
		return true, nil
	}
	raw, err := json.Marshal(newValue)
	if err != nil {
		return false, err
	}
	c.kv[key] = raw
	return true, nil
}

func (c *hookNotificationsClient) KVCompareAndSetWithExpiry(key string, oldValue, newValue any, _ time.Duration) (bool, error) {
	return c.KVCompareAndSet(key, oldValue, newValue)
}

func (c *hookNotificationsClient) GetPost(id string) (*model.Post, error) { return c.posts[id], nil }
func (c *hookNotificationsClient) GetUser(id string) (*model.User, error) { return c.users[id], nil }
func (c *hookNotificationsClient) GetChannel(id string) (*model.Channel, error) {
	if channel, ok := c.chans[id]; ok {
		return channel, nil
	}
	return nil, errors.New("channel not found")
}
func (c *hookNotificationsClient) GetPreferenceForUser(string, string, string) (model.Preference, error) {
	return model.Preference{}, errors.New("preference not found")
}
func (c *hookNotificationsClient) GetUserStatus(id string) (*model.Status, error) {
	return &model.Status{UserId: id, Status: model.StatusOffline}, nil
}
func (c *hookNotificationsClient) GetChannelMember(channelID, userID string) (*model.ChannelMember, error) {
	return &model.ChannelMember{ChannelId: channelID, UserId: userID, NotifyProps: model.GetDefaultChannelNotifyProps()}, nil
}
func (c *hookNotificationsClient) GetConfig() *model.Config { return c.config }
func (c *hookNotificationsClient) SendPushNotification(msg *model.PushNotification, userID string) error {
	c.onPush(msg, userID)
	return nil
}
func (c *hookNotificationsClient) LogError(string, ...any) {}
func (c *hookNotificationsClient) LogWarn(string, ...any)  {}
func (c *hookNotificationsClient) LogDebug(string, ...any) {}

func pluginWithAgentBot(svc *notifications.Service) *Plugin {
	mmBots := &bots.MMBots{}
	mmBots.SetBotsForTesting([]*bots.Bot{
		bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: botUserID}, nil),
	})
	return &Plugin{bots: mmBots, notifications: svc}
}

// legacyPlugin is a plugin on a server without deferred post notifications.
func legacyPlugin() *Plugin {
	return pluginWithAgentBot(notifications.NewService(newHookNotificationsClient(), i18n.Init(), false))
}

func TestNotificationWillBePushedLegacy(t *testing.T) {
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
			p := legacyPlugin()
			got, reason := p.NotificationWillBePushed(tt.notification, recipientUserID)
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

func TestEmailNotificationWillBeSentLegacy(t *testing.T) {
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
			p := legacyPlugin()
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

// TestAgentNotificationHooks covers both hooks on servers with and without
// deferred post notifications, in shared channels, which the server does not
// defer, with the recipient viewing the response or not, for agent and
// non-agent senders.
func TestAgentNotificationHooks(t *testing.T) {
	const (
		postID    = "post-1"
		channelID = "channel-1"
	)
	tests := []struct {
		name        string
		supported   bool
		shared      bool
		sender      string
		viewing     bool
		kvErr       error
		isDM        bool
		rootID      string
		wantBlocked bool
	}{
		{name: "shared channel: agent reply the recipient is not viewing", supported: true, shared: true, sender: botUserID, rootID: "root", wantBlocked: true},
		{name: "shared channel: agent DM post the recipient is not viewing", supported: true, shared: true, sender: botUserID, isDM: true, wantBlocked: true},
		{name: "shared channel: agent channel root post the recipient is viewing", supported: true, shared: true, sender: botUserID, viewing: true},
		{name: "shared channel: non-agent reply", supported: true, shared: true, sender: "regular-user", rootID: "root"},
		{name: "supported: agent post the recipient is viewing", supported: true, sender: botUserID, viewing: true, isDM: true, wantBlocked: true},
		{name: "supported: agent reply the recipient is viewing", supported: true, sender: botUserID, viewing: true, rootID: "root", wantBlocked: true},
		{name: "supported: agent DM post the recipient is not viewing", supported: true, sender: botUserID, isDM: true},
		{name: "supported: agent reply the recipient is not viewing", supported: true, sender: botUserID, rootID: "root"},
		{name: "supported: non-agent post the recipient is viewing", supported: true, sender: "regular-user", viewing: true, isDM: true},
		{name: "supported: non-agent post the recipient is not viewing", supported: true, sender: "regular-user", isDM: true},
		{name: "supported: viewing lookup fails", supported: true, sender: botUserID, viewing: true, kvErr: errors.New("kv down"), isDM: true},
		{name: "unsupported: agent DM post the recipient is viewing", sender: botUserID, viewing: true, isDM: true, wantBlocked: true},
		{name: "unsupported: agent DM post the recipient is not viewing", sender: botUserID, isDM: true, wantBlocked: true},
		{name: "unsupported: agent channel root post the recipient is viewing", sender: botUserID, viewing: true},
		{name: "unsupported: non-agent post the recipient is viewing", sender: "regular-user", viewing: true, isDM: true},
		{name: "unsupported: non-agent post the recipient is not viewing", sender: "regular-user", isDM: true},
	}
	for _, tt := range tests {
		for _, kind := range []string{"push", "email"} {
			t.Run(kind+" "+tt.name, func(t *testing.T) {
				client := newHookNotificationsClient()
				client.chans[channelID] = &model.Channel{Id: channelID, Shared: new(tt.shared)}
				svc := notifications.NewService(client, i18n.Init(), tt.supported)
				if tt.viewing {
					require.NoError(t, svc.SetViewing(postID, recipientUserID, "tab", true))
				}
				client.kvErr = tt.kvErr
				p := pluginWithAgentBot(svc)

				var blocked bool
				switch kind {
				case "push":
					channelType := model.ChannelTypeOpen
					if tt.isDM {
						channelType = model.ChannelTypeDirect
					}
					got, reason := p.NotificationWillBePushed(&model.PushNotification{
						PostId:      postID,
						ChannelId:   channelID,
						SenderId:    tt.sender,
						RootId:      tt.rootID,
						ChannelType: channelType,
					}, recipientUserID)
					blocked = got == nil
					require.Equal(t, blocked, reason != "")
				case "email":
					got, reason := p.EmailNotificationWillBeSent(&model.EmailNotification{
						PostId:          postID,
						ChannelId:       channelID,
						SenderId:        tt.sender,
						RecipientId:     recipientUserID,
						RootId:          tt.rootID,
						IsDirectMessage: tt.isDM,
					})
					blocked = got == nil
					require.Equal(t, blocked, reason != "")
				}
				require.Equal(t, tt.wantBlocked, blocked)
			})
		}
	}
}

// TestFallbackPushPassesTheHookOnce runs the old-server fallback push through
// NotificationWillBePushed, as SendPushNotification does: that push passes,
// and the same agent push afterwards is blocked again.
func TestFallbackPushPassesTheHookOnce(t *testing.T) {
	const (
		postID    = "fallback-post"
		channelID = "dm-channel"
	)
	client := newHookNotificationsClient()
	client.config = &model.Config{}
	client.config.SetDefaults()
	client.config.EmailSettings.SendPushNotifications = new(true)
	client.posts[postID] = &model.Post{Id: postID, ChannelId: channelID, UserId: botUserID, Type: "custom_llmbot", Message: "answer"}
	client.users[recipientUserID] = &model.User{Id: recipientUserID, Locale: "en"}
	client.users[botUserID] = &model.User{Id: botUserID, Username: "ai", IsBot: true}
	client.chans[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeDirect}

	svc := notifications.NewService(client, i18n.Init(), false)
	p := pluginWithAgentBot(svc)

	var hookResults []bool
	client.onPush = func(msg *model.PushNotification, userID string) {
		got, _ := p.NotificationWillBePushed(msg, userID)
		hookResults = append(hookResults, got != nil)
	}

	svc.Notify(context.Background(), notifications.StreamEnd{
		PostID:      postID,
		ChannelID:   channelID,
		RequesterID: recipientUserID,
		Outcome:     notifications.OutcomeCompleted,
	})
	require.Equal(t, []bool{true}, hookResults, "the fallback push passes the hook exactly once")

	got, _ := p.NotificationWillBePushed(&model.PushNotification{
		PostId:      postID,
		SenderId:    botUserID,
		PostType:    "custom_llmbot",
		ChannelType: model.ChannelTypeDirect,
	}, recipientUserID)
	require.Nil(t, got, "a later agent push for the same post is blocked again")
}

func TestNotificationWillBePushed_BotsCacheUninitialized(t *testing.T) {
	p := &Plugin{}
	notification := &model.PushNotification{
		PostId:   "post-1",
		SenderId: botUserID,
		RootId:   "parent-post-1",
		PostType: "custom_llmbot",
	}

	got, reason := p.NotificationWillBePushed(notification, recipientUserID)
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
