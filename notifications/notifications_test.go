// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package notifications

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name string
		end  StreamEnd
		want Delivery
	}{
		{"first final answer on a deferred post notifies everyone", StreamEnd{Outcome: OutcomeCompleted, PostDeferred: true}, DeliveryEveryone},
		{"final answer on an already delivered post notifies the requester", StreamEnd{Outcome: OutcomeCompleted}, DeliveryRequesterOnly},
		{"pause for the requester on a deferred post notifies the requester", StreamEnd{Outcome: OutcomeCompleted, PausedForRequester: true, PostDeferred: true}, DeliveryRequesterOnly},
		{"pause for the requester on a delivered post notifies the requester", StreamEnd{Outcome: OutcomeCompleted, PausedForRequester: true}, DeliveryRequesterOnly},
		{"error on a deferred post notifies the requester", StreamEnd{Outcome: OutcomeErrored, PostDeferred: true}, DeliveryRequesterOnly},
		{"error on a delivered post notifies the requester", StreamEnd{Outcome: OutcomeErrored}, DeliveryRequesterOnly},
		{"empty result notifies the requester", StreamEnd{Outcome: OutcomeEmpty, PostDeferred: true}, DeliveryRequesterOnly},
		{"cancel not requested by the user notifies the requester", StreamEnd{Outcome: OutcomeCancelled, PostDeferred: true}, DeliveryRequesterOnly},
		{"failed final update notifies the requester", StreamEnd{Outcome: OutcomeCompleted, UpdateFailed: true, PostDeferred: true}, DeliveryRequesterOnly},
		{"stop by the requester notifies nobody", StreamEnd{Outcome: OutcomeStoppedByUser, PostDeferred: true}, DeliveryNone},
		{"stop by the requester with a failed update notifies nobody", StreamEnd{Outcome: OutcomeStoppedByUser, UpdateFailed: true}, DeliveryNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Decide(tt.end))
		})
	}
}

type probeAPI struct{ appErr *model.AppError }

func (p probeAPI) DeliverPostNotifications(postID string, _ *model.DeliverPostNotificationsOptions) *model.AppError {
	return p.appErr
}

func TestProbeSupport(t *testing.T) {
	tests := []struct {
		name   string
		appErr *model.AppError
		want   bool
	}{
		{"not found for the probe post means supported", model.NewAppError("DeliverPostNotifications", "app.post.get.app_error", nil, "", http.StatusNotFound), true},
		{"nil from an old server means unsupported", nil, false},
		{"bad request means unsupported", model.NewAppError("DeliverPostNotifications", "x", nil, "", http.StatusBadRequest), false},
		{"internal error means unsupported", model.NewAppError("DeliverPostNotifications", "x", nil, "", http.StatusInternalServerError), false},
		{"not implemented means unsupported", model.NewAppError("DeliverPostNotifications", "x", nil, "", http.StatusNotImplemented), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ProbeSupport(probeAPI{appErr: tt.appErr}))
		})
	}
}

func TestMarkDeferred(t *testing.T) {
	tests := []struct {
		name      string
		supported bool
	}{
		{"supported server marks the post", true},
		{"unsupported server leaves the post alone", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewService(newFakeClient(), i18n.Init(), tt.supported)
			post := &model.Post{}
			s.MarkDeferred(post)
			assert.Equal(t, tt.supported, post.HasDeferredNotification())
		})
	}
}

const (
	testPostID      = "postid00000000000000000000"
	testChannelID   = "channelid00000000000000000"
	testRequesterID = "requesterid000000000000000"
	testBotID       = "botid000000000000000000000"
)

func TestNotifySupported(t *testing.T) {
	tests := []struct {
		name     string
		end      StreamEnd
		viewing  bool
		kvErr    error
		want     []deliverCall
		wantPush int
	}{
		{
			name: "everyone releases the deferred post",
			end:  StreamEnd{Outcome: OutcomeCompleted, PostDeferred: true},
			want: []deliverCall{{postID: testPostID}},
		},
		{
			name:    "everyone releases the post even while the requester is viewing",
			end:     StreamEnd{Outcome: OutcomeCompleted, PostDeferred: true},
			viewing: true,
			want:    []deliverCall{{postID: testPostID}},
		},
		{
			name: "requester only notifies just the requester",
			end:  StreamEnd{Outcome: OutcomeErrored, PostDeferred: true},
			want: []deliverCall{{postID: testPostID, userIDs: []string{testRequesterID}}},
		},
		{
			name:    "requester only is skipped while the requester is viewing",
			end:     StreamEnd{Outcome: OutcomeCompleted, PausedForRequester: true},
			viewing: true,
		},
		{
			name:  "requester only still notifies when the viewing lookup fails",
			end:   StreamEnd{Outcome: OutcomeCompleted},
			kvErr: errors.New("kv down"),
			want:  []deliverCall{{postID: testPostID, userIDs: []string{testRequesterID}}},
		},
		{
			name: "stop by the requester delivers nothing",
			end:  StreamEnd{Outcome: OutcomeStoppedByUser, PostDeferred: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeClient()
			s := NewService(client, i18n.Init(), true)
			if tt.viewing {
				require.NoError(t, s.SetViewing(testPostID, testRequesterID, true))
			}
			client.kvErr = tt.kvErr

			end := tt.end
			end.PostID = testPostID
			end.ChannelID = testChannelID
			end.RequesterID = testRequesterID
			s.Notify(context.Background(), end)

			assert.Equal(t, tt.want, client.delivered)
			assert.Empty(t, client.pushes, "supported servers never use the fallback push")
		})
	}
}

func TestNotifyLogsDeliveryErrors(t *testing.T) {
	client := newFakeClient()
	client.deliverErr = errors.New("boom")
	s := NewService(client, i18n.Init(), true)
	s.Notify(context.Background(), StreamEnd{PostID: testPostID, RequesterID: testRequesterID, Outcome: OutcomeCompleted, PostDeferred: true})
	assert.Contains(t, client.errorLogs, "Failed to deliver agent post notifications")
}

func TestViewing(t *testing.T) {
	client := newFakeClient()
	s := NewService(client, i18n.Init(), true)

	viewing, err := s.IsViewing(testPostID, testRequesterID)
	require.NoError(t, err)
	assert.False(t, viewing)

	require.NoError(t, s.SetViewing(testPostID, testRequesterID, true))
	viewing, err = s.IsViewing(testPostID, testRequesterID)
	require.NoError(t, err)
	assert.True(t, viewing)
	assert.Equal(t, ViewingTTL, client.kvTTL[viewingKey(testPostID, testRequesterID)])

	other, err := s.IsViewing(testPostID, testBotID)
	require.NoError(t, err)
	assert.False(t, other, "viewing is per user")

	require.NoError(t, s.SetViewing(testPostID, testRequesterID, false))
	viewing, err = s.IsViewing(testPostID, testRequesterID)
	require.NoError(t, err)
	assert.False(t, viewing)

	client.kvErr = errors.New("kv down")
	_, err = s.IsViewing(testPostID, testRequesterID)
	require.Error(t, err)
	assert.False(t, s.RecipientIsViewing(testPostID, testRequesterID), "a failed lookup does not count as viewing")
}

func newFallbackFixture(channelType model.ChannelType) *fakeClient {
	client := newFakeClient()
	client.config.EmailSettings.SendPushNotifications = new(true)
	client.config.EmailSettings.PushNotificationContents = new(model.FullNotification)
	client.posts[testPostID] = &model.Post{Id: testPostID, ChannelId: testChannelID, UserId: testBotID, Type: "custom_llmbot", Message: "Here is the answer"}
	client.users[testRequesterID] = &model.User{Id: testRequesterID, Locale: "en", NotifyProps: model.StringMap{model.PushNotifyProp: model.UserNotifyMention}}
	client.users[testBotID] = &model.User{Id: testBotID, Username: "ai", IsBot: true}
	client.status[testRequesterID] = &model.Status{UserId: testRequesterID, Status: model.StatusOnline}
	client.chans[testChannelID] = &model.Channel{Id: testChannelID, Type: channelType, DisplayName: "Town Square", TeamId: "team"}
	client.members[testChannelID+testRequesterID] = &model.ChannelMember{ChannelId: testChannelID, UserId: testRequesterID, NotifyProps: model.GetDefaultChannelNotifyProps()}
	return client
}

func TestFallbackPush(t *testing.T) {
	tests := []struct {
		name        string
		channelType model.ChannelType
		setup       func(c *fakeClient, s *Service)
		end         StreamEnd
		wantMessage string
		wantChannel string
		wantPush    bool
	}{
		{
			name:        "final answer in a DM pushes the content to the requester",
			channelType: model.ChannelTypeDirect,
			end:         StreamEnd{Outcome: OutcomeCompleted},
			wantPush:    true,
			wantMessage: "Here is the answer",
			wantChannel: "ai",
		},
		{
			name:        "final answer in a channel prefixes the sender",
			channelType: model.ChannelTypeOpen,
			end:         StreamEnd{Outcome: OutcomeCompleted},
			wantPush:    true,
			wantMessage: "ai: Here is the answer",
			wantChannel: "Town Square",
		},
		{
			name:        "generic contents hide the message in a DM",
			channelType: model.ChannelTypeDirect,
			setup: func(c *fakeClient, _ *Service) {
				c.config.EmailSettings.PushNotificationContents = new(model.GenericNotification)
			},
			end:         StreamEnd{Outcome: OutcomeErrored},
			wantPush:    true,
			wantMessage: "sent you a message.",
			wantChannel: "ai",
		},
		{
			name:        "generic without channel hides the channel name",
			channelType: model.ChannelTypeOpen,
			setup: func(c *fakeClient, _ *Service) {
				c.config.EmailSettings.PushNotificationContents = new(model.GenericNoChannelNotification)
			},
			end:         StreamEnd{Outcome: OutcomeCompleted, PausedForRequester: true},
			wantPush:    true,
			wantMessage: "ai replied to you.",
			wantChannel: "",
		},
		{
			name:        "stop by the requester pushes nothing",
			channelType: model.ChannelTypeDirect,
			end:         StreamEnd{Outcome: OutcomeStoppedByUser},
		},
		{
			name:        "requester viewing the response gets no push",
			channelType: model.ChannelTypeDirect,
			setup: func(_ *fakeClient, s *Service) {
				_ = s.SetViewing(testPostID, testRequesterID, true)
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
		{
			name:        "do not disturb blocks the push",
			channelType: model.ChannelTypeDirect,
			setup: func(c *fakeClient, _ *Service) {
				c.status[testRequesterID].Status = model.StatusDnd
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
		{
			name:        "out of office blocks the push",
			channelType: model.ChannelTypeDirect,
			setup: func(c *fakeClient, _ *Service) {
				c.status[testRequesterID].Status = model.StatusOutOfOffice
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
		{
			name:        "push preference none blocks the push",
			channelType: model.ChannelTypeDirect,
			setup: func(c *fakeClient, _ *Service) {
				c.users[testRequesterID].NotifyProps[model.PushNotifyProp] = model.UserNotifyNone
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
		{
			name:        "channel push preference none blocks the push",
			channelType: model.ChannelTypeOpen,
			setup: func(c *fakeClient, _ *Service) {
				c.members[testChannelID+testRequesterID].NotifyProps[model.PushNotifyProp] = model.ChannelNotifyNone
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
		{
			name:        "muted channel blocks the push",
			channelType: model.ChannelTypeOpen,
			setup: func(c *fakeClient, _ *Service) {
				c.members[testChannelID+testRequesterID].NotifyProps[model.MarkUnreadNotifyProp] = model.ChannelMarkUnreadMention
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
		{
			name:        "active in the channel with push on online blocks a top-level push",
			channelType: model.ChannelTypeOpen,
			setup: func(c *fakeClient, _ *Service) {
				c.status[testRequesterID].ActiveChannel = testChannelID
				c.status[testRequesterID].LastActivityAt = model.GetMillis()
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
		{
			name:        "push status away blocks while online",
			channelType: model.ChannelTypeDirect,
			setup: func(c *fakeClient, _ *Service) {
				c.users[testRequesterID].NotifyProps[model.PushStatusNotifyProp] = model.StatusAway
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
		{
			name:        "server push notifications disabled",
			channelType: model.ChannelTypeDirect,
			setup: func(c *fakeClient, _ *Service) {
				c.config.EmailSettings.SendPushNotifications = new(false)
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFallbackFixture(tt.channelType)
			s := NewService(client, i18n.Init(), false)
			if tt.setup != nil {
				tt.setup(client, s)
			}
			hookPassed := 0
			client.onPush = func(msg *model.PushNotification, userID string) bool {
				if s.ConsumeFallbackPush(msg.PostId, userID) {
					hookPassed++
					return true
				}
				return false
			}

			end := tt.end
			end.PostID = testPostID
			end.ChannelID = testChannelID
			end.RequesterID = testRequesterID
			s.Notify(context.Background(), end)

			assert.Empty(t, client.delivered, "unsupported servers never call DeliverPostNotifications")
			if !tt.wantPush {
				assert.Empty(t, client.pushes)
				return
			}
			require.Len(t, client.pushes, 1)
			assert.Equal(t, 1, hookPassed)
			push := client.pushes[0]
			assert.Equal(t, testRequesterID, push.userID)
			assert.Equal(t, testPostID, push.msg.PostId)
			assert.Equal(t, testBotID, push.msg.SenderId)
			assert.Equal(t, tt.wantMessage, push.msg.Message)
			assert.Equal(t, tt.wantChannel, push.msg.ChannelName)
			assert.False(t, s.ConsumeFallbackPush(testPostID, testRequesterID), "the exemption is used once")
		})
	}
}

func TestConsumeFallbackPushOnlyDuringSend(t *testing.T) {
	client := newFallbackFixture(model.ChannelTypeDirect)
	s := NewService(client, i18n.Init(), false)
	assert.False(t, s.ConsumeFallbackPush(testPostID, testRequesterID), "no exemption before a fallback push")

	// A hook that never runs (no sessions) must not leave the exemption behind.
	client.onPush = func(*model.PushNotification, string) bool { return true }
	s.Notify(context.Background(), StreamEnd{PostID: testPostID, ChannelID: testChannelID, RequesterID: testRequesterID, Outcome: OutcomeCompleted})
	require.Len(t, client.pushes, 1)
	assert.False(t, s.ConsumeFallbackPush(testPostID, testRequesterID))
}
