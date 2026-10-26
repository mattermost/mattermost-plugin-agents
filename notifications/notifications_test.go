// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package notifications

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
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

// probeAPI answers probes with answers in order, repeating the last one.
type probeAPI struct {
	answers []*model.AppError
	calls   int
}

func (p *probeAPI) DeliverPostNotifications(string, *model.DeliverPostNotificationsOptions) *model.AppError {
	answer := p.answers[min(p.calls, len(p.answers)-1)]
	p.calls++
	return answer
}

func probeErr(status int) *model.AppError {
	return model.NewAppError("DeliverPostNotifications", "app.post.get.app_error", nil, "", status)
}

func TestProbeSupport(t *testing.T) {
	tests := []struct {
		name    string
		appErr  *model.AppError
		want    bool
		wantErr bool
	}{
		{"not found for the probe post means supported", probeErr(http.StatusNotFound), true, false},
		{"nil from an old server means unsupported", nil, false, false},
		{"bad request is an error", probeErr(http.StatusBadRequest), false, true},
		{"internal error is an error", probeErr(http.StatusInternalServerError), false, true},
		{"not implemented is an error", probeErr(http.StatusNotImplemented), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			supported, err := ProbeSupport(&probeAPI{answers: []*model.AppError{tt.appErr}})
			assert.Equal(t, tt.want, supported)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func TestProbedService(t *testing.T) {
	tests := []struct {
		name    string
		answers []*model.AppError
		// wantSupported is Supported() before and after a re-probe is due.
		wantSupported [2]bool
		wantProbes    int
		wantWarn      bool
	}{
		{
			name:          "supporting server is probed once",
			answers:       []*model.AppError{probeErr(http.StatusNotFound)},
			wantSupported: [2]bool{true, true},
			wantProbes:    1,
		},
		{
			name:          "old server is probed once",
			answers:       []*model.AppError{nil},
			wantSupported: [2]bool{false, false},
			wantProbes:    1,
		},
		{
			name:          "failed probe counts as unsupported until a re-probe succeeds",
			answers:       []*model.AppError{probeErr(http.StatusInternalServerError), probeErr(http.StatusNotFound)},
			wantSupported: [2]bool{false, true},
			wantProbes:    2,
			wantWarn:      true,
		},
		{
			name:          "repeated probe failures keep re-probing when due",
			answers:       []*model.AppError{probeErr(http.StatusInternalServerError)},
			wantSupported: [2]bool{false, false},
			wantProbes:    2,
			wantWarn:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeClient()
			api := &probeAPI{answers: tt.answers}
			s := NewProbedService(client, i18n.Init(), api)

			s.reprobeInterval = time.Hour
			assert.Equal(t, tt.wantSupported[0], s.Supported())
			assert.Equal(t, tt.wantSupported[0], s.Supported(), "no re-probe before it is due")
			assert.Equal(t, 1, api.calls)

			s.reprobeInterval = 0
			assert.Equal(t, tt.wantSupported[1], s.Supported())
			assert.Equal(t, tt.wantProbes, api.calls)
			assert.Equal(t, tt.wantWarn, len(client.warnLogs) > 0)
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
		name string
		end  StreamEnd
		// staleProbe makes this node's cached probe say unsupported.
		staleProbe bool
		viewing    bool
		kvErr      error
		want       []deliverCall
	}{
		{
			name: "everyone releases the deferred post",
			end:  StreamEnd{Outcome: OutcomeCompleted, PostDeferred: true},
			want: []deliverCall{{postID: testPostID}},
		},
		{
			name:       "a deferred post is released even when the cached probe says unsupported",
			end:        StreamEnd{Outcome: OutcomeCompleted, PostDeferred: true},
			staleProbe: true,
			want:       []deliverCall{{postID: testPostID}},
		},
		{
			name:       "the requester of a deferred post is notified even when the cached probe says unsupported",
			end:        StreamEnd{Outcome: OutcomeErrored, PostDeferred: true},
			staleProbe: true,
			want:       []deliverCall{{postID: testPostID, userIDs: []string{testRequesterID}}},
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
			client.chans[testChannelID] = &model.Channel{Id: testChannelID, Type: model.ChannelTypeOpen}
			s := NewService(client, i18n.Init(), !tt.staleProbe)
			if tt.viewing {
				require.NoError(t, s.SetViewing(testPostID, testRequesterID, testTabID, true))
			}
			client.kvErr = tt.kvErr

			end := tt.end
			end.PostID = testPostID
			end.ChannelID = testChannelID
			end.RequesterID = testRequesterID
			s.Notify(context.Background(), end)

			assert.Equal(t, tt.want, client.delivered)
			assert.Empty(t, client.pushes, "deferred posts never use the fallback push")
		})
	}
}

// TestNotifyLegacyPosts covers posts the server did not defer, which already
// notified the channel when they were created: only the requester gets the
// fallback push, whatever the run outcome.
func TestNotifyLegacyPosts(t *testing.T) {
	tests := []struct {
		name      string
		supported bool
		shared    bool
		end       StreamEnd
		wantPush  bool
	}{
		{"final answer on a server without support", false, false, StreamEnd{Outcome: OutcomeCompleted}, true},
		{"final answer in a shared channel", true, true, StreamEnd{Outcome: OutcomeCompleted}, true},
		{"error in a shared channel", true, true, StreamEnd{Outcome: OutcomeErrored}, true},
		{"pause in a shared channel", true, true, StreamEnd{Outcome: OutcomeCompleted, PausedForRequester: true}, true},
		{"stop by the requester in a shared channel", true, true, StreamEnd{Outcome: OutcomeStoppedByUser}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFallbackFixture(model.ChannelTypeOpen)
			if tt.shared {
				client.chans[testChannelID].Shared = new(true)
			}
			s := NewService(client, i18n.Init(), tt.supported)
			assert.True(t, s.UsesLegacyNotifications(testChannelID))

			end := tt.end
			end.PostID = testPostID
			end.ChannelID = testChannelID
			end.RequesterID = testRequesterID
			s.Notify(context.Background(), end)

			assert.Empty(t, client.delivered, "a legacy post is never released or delivered by the server")
			if tt.wantPush {
				require.Len(t, client.pushes, 1)
				assert.Equal(t, testRequesterID, client.pushes[0].userID)
			} else {
				assert.Empty(t, client.pushes)
			}
		})
	}
}

func TestUsesLegacyNotifications(t *testing.T) {
	tests := []struct {
		name      string
		supported bool
		channel   *model.Channel
		want      bool
	}{
		{"server without support", false, &model.Channel{Id: testChannelID}, true},
		{"shared channel", true, &model.Channel{Id: testChannelID, Shared: new(true)}, true},
		{"channel that is not shared", true, &model.Channel{Id: testChannelID, Shared: new(false)}, false},
		{"failed channel lookup counts as not shared", true, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeClient()
			if tt.channel != nil {
				client.chans[testChannelID] = tt.channel
			}
			s := NewService(client, i18n.Init(), tt.supported)
			assert.Equal(t, tt.want, s.UsesLegacyNotifications(testChannelID))
		})
	}
}

func TestReleaseFailures(t *testing.T) {
	errServer := errors.New("server error")
	release := deliverCall{postID: testPostID}
	toRequester := deliverCall{postID: testPostID, userIDs: []string{testRequesterID}}
	tests := []struct {
		name string
		// releaseErrs answers release attempts in order, repeating the last.
		releaseErrs    []error
		scopedErr      error
		releasedAnyway bool
		viewing        bool
		want           []deliverCall
		wantPush       bool
		wantErrorLog   bool
	}{
		{
			name:        "a release that succeeds on retry notifies the channel once",
			releaseErrs: []error{errServer, nil},
			want:        []deliverCall{release, release},
		},
		{
			name:         "a release that keeps failing notifies the requester",
			releaseErrs:  []error{errServer},
			want:         []deliverCall{release, release, release, toRequester},
			wantErrorLog: true,
		},
		{
			name:         "a release that keeps failing notifies nobody else while the requester is viewing",
			releaseErrs:  []error{errServer},
			viewing:      true,
			want:         []deliverCall{release, release, release},
			wantErrorLog: true,
		},
		{
			name:           "a release that failed after releasing the post is not followed up",
			releaseErrs:    []error{errServer},
			releasedAnyway: true,
			want:           []deliverCall{release, release, release},
			wantErrorLog:   true,
		},
		{
			name:         "a requester the server cannot notify gets the fallback push",
			releaseErrs:  []error{errServer},
			scopedErr:    errServer,
			want:         []deliverCall{release, release, release, toRequester},
			wantPush:     true,
			wantErrorLog: true,
		},
		{
			name:        "a deleted post is not retried",
			releaseErrs: []error{pluginapi.ErrNotFound},
			want:        []deliverCall{release},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFallbackFixture(model.ChannelTypeOpen)
			client.posts[testPostID].AddProp(model.PostPropsDeferredNotification, true)
			if tt.releasedAnyway {
				client.posts[testPostID].DelProp(model.PostPropsDeferredNotification)
			}
			releases := 0
			client.onDeliver = func(call deliverCall) error {
				if call.userIDs != nil {
					return tt.scopedErr
				}
				err := tt.releaseErrs[min(releases, len(tt.releaseErrs)-1)]
				releases++
				return err
			}
			s := NewService(client, i18n.Init(), true)
			s.releaseRetryDelays = []time.Duration{0, 0}
			client.onPush = func(msg *model.PushNotification, userID string) bool {
				return s.ConsumeFallbackPush(msg.PostId, userID)
			}
			if tt.viewing {
				require.NoError(t, s.SetViewing(testPostID, testRequesterID, testTabID, true))
			}

			s.Notify(context.Background(), StreamEnd{PostID: testPostID, ChannelID: testChannelID, RequesterID: testRequesterID, Outcome: OutcomeCompleted, PostDeferred: true})

			assert.Equal(t, tt.want, client.delivered)
			assert.Equal(t, tt.wantPush, len(client.pushes) == 1)
			assert.Equal(t, tt.wantErrorLog, slices.Contains(client.errorLogs, "Failed to release agent post notifications to the channel"))
		})
	}
}

func TestNotifyLogsDeliveryErrors(t *testing.T) {
	client := newFakeClient()
	client.deliverErr = errors.New("boom")
	s := NewService(client, i18n.Init(), true)
	s.releaseRetryDelays = nil
	s.Notify(context.Background(), StreamEnd{PostID: testPostID, RequesterID: testRequesterID, Outcome: OutcomeCompleted, PostDeferred: true})
	assert.Contains(t, client.errorLogs, "Failed to deliver agent post notifications")
}

const (
	testTabID      = "tab-1"
	testOtherTabID = "tab-2"
)

func TestViewing(t *testing.T) {
	type report struct {
		tab     string
		viewing bool
	}
	tests := []struct {
		name    string
		reports []report
		want    bool
	}{
		{"nothing reported", nil, false},
		{"a tab viewing", []report{{testTabID, true}}, true},
		{"a tab that stopped viewing", []report{{testTabID, true}, {testTabID, false}}, false},
		{"another tab still viewing", []report{{testTabID, true}, {testOtherTabID, true}, {testTabID, false}}, true},
		{"the latest tab stopped viewing", []report{{testTabID, true}, {testOtherTabID, true}, {testOtherTabID, false}}, false},
		{"a report without a tab", []report{{"", true}}, true},
		{"a stop without a tab", []report{{"", true}, {"", false}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeClient()
			s := NewService(client, i18n.Init(), true)
			for _, r := range tt.reports {
				require.NoError(t, s.SetViewing(testPostID, testRequesterID, r.tab, r.viewing))
			}
			viewing, err := s.IsViewing(testPostID, testRequesterID)
			require.NoError(t, err)
			assert.Equal(t, tt.want, viewing)
			if tt.want {
				assert.Equal(t, ViewingTTL, client.kvTTL[viewingKey(testPostID, testRequesterID)])
			}

			other, err := s.IsViewing(testPostID, testBotID)
			require.NoError(t, err)
			assert.False(t, other, "viewing is per user")
		})
	}
}

func TestViewingLookupFailure(t *testing.T) {
	client := newFakeClient()
	s := NewService(client, i18n.Init(), true)
	require.NoError(t, s.SetViewing(testPostID, testRequesterID, testTabID, true))
	client.kvErr = errors.New("kv down")
	_, err := s.IsViewing(testPostID, testRequesterID)
	require.Error(t, err)
	assert.False(t, s.RecipientIsViewing(testPostID, testRequesterID), "a failed lookup does not count as viewing")
}

// TestViewingHeartbeatFitsTTL ties the webapp heartbeat to the server TTL: a
// heartbeat slower than half the TTL would let viewing lapse between reports.
func TestViewingHeartbeatFitsTTL(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "webapp", "src", "viewing.ts"))
	require.NoError(t, err)
	match := regexp.MustCompile(`VIEWING_HEARTBEAT_MS\s*=\s*([\d_]+)`).FindSubmatch(src)
	require.NotNil(t, match, "webapp/src/viewing.ts defines VIEWING_HEARTBEAT_MS")
	ms, err := strconv.Atoi(strings.ReplaceAll(string(match[1]), "_", ""))
	require.NoError(t, err)

	assert.Equal(t, ViewingHeartbeat, time.Duration(ms)*time.Millisecond, "webapp heartbeat matches ViewingHeartbeat")
	assert.Less(t, 2*ViewingHeartbeat, ViewingTTL)
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

func activeInChannel(c *fakeClient) {
	c.status[testRequesterID].ActiveChannel = testChannelID
	c.status[testRequesterID].LastActivityAt = model.GetMillis()
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
				_ = s.SetViewing(testPostID, testRequesterID, testTabID, true)
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
		{
			name:        "a post with no text uses the generic message in a DM",
			channelType: model.ChannelTypeDirect,
			setup: func(c *fakeClient, _ *Service) {
				c.posts[testPostID].Message = ""
				c.posts[testPostID].FileIds = model.StringArray{"fileid"}
			},
			end:         StreamEnd{Outcome: OutcomeCompleted},
			wantPush:    true,
			wantMessage: "sent you a message.",
			wantChannel: "ai",
		},
		{
			name:        "a post paused on tool calls uses the generic message in a channel",
			channelType: model.ChannelTypeOpen,
			setup: func(c *fakeClient, _ *Service) {
				c.posts[testPostID].Message = "  "
			},
			end:         StreamEnd{Outcome: OutcomeCompleted, PausedForRequester: true},
			wantPush:    true,
			wantMessage: "ai replied to you.",
			wantChannel: "Town Square",
		},
		{
			name:        "a reply with collapsed threads on is pushed while active in the channel",
			channelType: model.ChannelTypeOpen,
			setup: func(c *fakeClient, _ *Service) {
				activeInChannel(c)
				c.posts[testPostID].RootId = "rootid"
				c.config.ServiceSettings.CollapsedThreads = new(model.CollapsedThreadsAlwaysOn)
			},
			end:         StreamEnd{Outcome: OutcomeCompleted},
			wantPush:    true,
			wantMessage: "ai: Here is the answer",
			wantChannel: "Town Square",
		},
		{
			name:        "a reply with collapsed threads off is not pushed while active in the channel",
			channelType: model.ChannelTypeOpen,
			setup: func(c *fakeClient, _ *Service) {
				activeInChannel(c)
				c.posts[testPostID].RootId = "rootid"
				c.config.ServiceSettings.CollapsedThreads = new(model.CollapsedThreadsDisabled)
			},
			end: StreamEnd{Outcome: OutcomeCompleted},
		},
		{
			name:        "a reply for a user who turned collapsed threads off is not pushed while active in the channel",
			channelType: model.ChannelTypeOpen,
			setup: func(c *fakeClient, _ *Service) {
				activeInChannel(c)
				c.posts[testPostID].RootId = "rootid"
				c.config.ServiceSettings.CollapsedThreads = new(model.CollapsedThreadsDefaultOn)
				c.prefs[testRequesterID+model.PreferenceCategoryDisplaySettings+model.PreferenceNameCollapsedThreadsEnabled] = model.Preference{Value: "off"}
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
