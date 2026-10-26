// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notificationsTestClient adds what the notifications service needs beyond
// mmapi.Client, with push enabled and the requester away from the channel.
type notificationsTestClient struct {
	*fakeMMClient
	pushMu sync.Mutex
	pushes []string
}

func (c *notificationsTestClient) GetPost(postID string) (*model.Post, error) {
	for _, p := range c.createdPosts {
		if p.Id == postID {
			return p, nil
		}
	}
	return c.fakeMMClient.GetPost(postID)
}

func (c *notificationsTestClient) GetChannelMember(channelID, userID string) (*model.ChannelMember, error) {
	return &model.ChannelMember{ChannelId: channelID, UserId: userID, NotifyProps: model.GetDefaultChannelNotifyProps()}, nil
}

func (c *notificationsTestClient) GetUserStatus(userID string) (*model.Status, error) {
	return &model.Status{UserId: userID, Status: model.StatusOffline}, nil
}

func (c *notificationsTestClient) GetConfig() *model.Config {
	cfg := c.fakeMMClient.GetConfig()
	cfg.EmailSettings.SendPushNotifications = new(true)
	cfg.EmailSettings.PushNotificationContents = new(model.FullNotification)
	return cfg
}

func (c *notificationsTestClient) GetPreferenceForUser(string, string, string) (model.Preference, error) {
	return model.Preference{}, errors.New("no preference")
}

func (c *notificationsTestClient) SendPushNotification(_ *model.PushNotification, userID string) error {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	c.pushes = append(c.pushes, userID)
	return nil
}

func (c *notificationsTestClient) pushedTo() []string {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	return append([]string(nil), c.pushes...)
}

// observedNotifier is the real notifications service, signaling each run end.
type observedNotifier struct {
	*notifications.Service
	ended chan notifications.StreamEnd
}

func (o *observedNotifier) Notify(ctx context.Context, end notifications.StreamEnd) {
	o.Service.Notify(ctx, end)
	o.ended <- end
}

func (o *observedNotifier) waitForRunEnd(t *testing.T) notifications.StreamEnd {
	t.Helper()
	select {
	case end := <-o.ended:
		return end
	case <-time.After(10 * time.Second):
		require.FailNow(t, "run never notified")
		return notifications.StreamEnd{}
	}
}

// requireNoMoreRunEnds fails if another notification arrives shortly after.
func (o *observedNotifier) requireNoMoreRunEnds(t *testing.T) {
	t.Helper()
	select {
	case end := <-o.ended:
		require.FailNow(t, "unexpected second notification", "%+v", end)
	case <-time.After(100 * time.Millisecond):
	}
}

type notificationWiring struct {
	*policyEnv
	client    *notificationsTestClient
	notifier  *observedNotifier
	streaming *streaming.MMPostStreamService
}

// setupNotificationWiring is a policy environment wired like OnActivate: the
// real streaming service with the conversation store as its turn store, and
// the real notifications service injected into both streaming and
// conversations.
func setupNotificationWiring(t *testing.T, supported bool, llmResponses ...*llm.TextStreamResult) *notificationWiring {
	t.Helper()
	env := setupPolicyEnv(t, []llm.BotConfig{policyBotConfig()}, llmResponses...)
	client := &notificationsTestClient{fakeMMClient: env.mmClient}
	notifier := &observedNotifier{
		Service: notifications.NewService(client, i18n.Init(), supported),
		ended:   make(chan notifications.StreamEnd, 8),
	}
	streamingService := streaming.NewMMPostStreamService(env.mmClient, i18n.Init())
	streamingService.SetTurnStore(env.convStore)
	streamingService.SetNotifier(notifier)
	env.conversations.SetStreamingServiceForTesting(streamingService)
	env.conversations.SetNotifier(notifier)
	return &notificationWiring{policyEnv: env, client: client, notifier: notifier, streaming: streamingService}
}

func (w *notificationWiring) dmPost(message string) *model.Post {
	post := &model.Post{Id: model.NewId(), UserId: autoReplyUserID, ChannelId: w.dm.Id, Message: message}
	w.setThread(post.Id, post)
	return post
}

func TestNewResponsesNotifyOnceWhenTheyEnd(t *testing.T) {
	tests := []struct {
		name         string
		supported    bool
		responses    func() []*llm.TextStreamResult
		post         func(w *notificationWiring) *model.Post
		wantDeferred bool
		wantOutcome  notifications.Outcome
		wantPaused   bool
		wantDelivery []fakeDelivery // postID filled in from the created post
		wantPushes   []string
	}{
		{
			name:         "DM final answer releases the deferred post to everyone",
			supported:    true,
			responses:    func() []*llm.TextStreamResult { return []*llm.TextStreamResult{dmMakeTextStream("It's sunny")} },
			post:         func(w *notificationWiring) *model.Post { return w.dmPost("what is the weather") },
			wantDeferred: true,
			wantOutcome:  notifications.OutcomeCompleted,
			wantDelivery: []fakeDelivery{{}},
		},
		{
			name:      "channel mention final answer releases the deferred post to everyone",
			supported: true,
			responses: func() []*llm.TextStreamResult { return []*llm.TextStreamResult{dmMakeTextStream("It's sunny")} },
			post: func(w *notificationWiring) *model.Post {
				return w.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" weather?")
			},
			wantDeferred: true,
			wantOutcome:  notifications.OutcomeCompleted,
			wantDelivery: []fakeDelivery{{}},
		},
		{
			name:      "DM paused for tool approval notifies only the requester",
			supported: true,
			responses: func() []*llm.TextStreamResult {
				return []*llm.TextStreamResult{dmMakeToolCallStream([]llm.ToolCall{policyWeatherCall})}
			},
			post:         func(w *notificationWiring) *model.Post { return w.dmPost("what is the weather") },
			wantDeferred: true,
			wantOutcome:  notifications.OutcomeCompleted,
			wantPaused:   true,
			wantDelivery: []fakeDelivery{{userIDs: []string{autoReplyUserID}}},
		},
		{
			name:         "DM failure before streaming notifies only the requester",
			supported:    true,
			responses:    func() []*llm.TextStreamResult { return nil },
			post:         func(w *notificationWiring) *model.Post { return w.dmPost("what is the weather") },
			wantDeferred: true,
			wantOutcome:  notifications.OutcomeErrored,
			wantDelivery: []fakeDelivery{{userIDs: []string{autoReplyUserID}}},
		},
		{
			name:        "server without deferred notifications pushes to the requester instead",
			responses:   func() []*llm.TextStreamResult { return []*llm.TextStreamResult{dmMakeTextStream("It's sunny")} },
			post:        func(w *notificationWiring) *model.Post { return w.dmPost("what is the weather") },
			wantOutcome: notifications.OutcomeCompleted,
			wantPushes:  []string{autoReplyUserID},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := setupNotificationWiring(t, tc.supported, tc.responses()...)
			w.checker.setPolicy(policyToolOrigin, policyToolName, mcp.ToolPolicyAsk)

			w.conversations.MessageHasBeenPosted(nil, tc.post(w))
			end := w.notifier.waitForRunEnd(t)
			w.notifier.requireNoMoreRunEnds(t)

			require.Len(t, w.mmClient.createdPosts, 1)
			created := w.mmClient.createdPosts[0]
			assert.Equal(t, tc.wantDeferred, created.HasDeferredNotification())
			assert.Equal(t, created.Id, end.PostID)
			assert.Equal(t, autoReplyUserID, end.RequesterID)
			assert.Equal(t, tc.wantOutcome, end.Outcome)
			assert.Equal(t, tc.wantPaused, end.PausedForRequester)

			var want []fakeDelivery
			for _, d := range tc.wantDelivery {
				d.postID = created.Id
				want = append(want, d)
			}
			assert.Equal(t, want, w.mmClient.deliveredNotifications())
			assert.Equal(t, tc.wantPushes, w.client.pushedTo())
		})
	}
}

func TestShareDecisionPauseNotifiesTheRequester(t *testing.T) {
	w := setupNotificationWiring(t, true)
	w.config.enableChannelMentionToolCalling = true
	w.checker.setPolicy(policyToolOrigin, policyToolName, mcp.ToolPolicyAsk)

	botPost := w.seedRun(t, w.channel, nil, humanPost("what is the weather"), pendingWeatherBlock(), nil)
	botPost.AddProp(streaming.LLMRequesterUserIDProp, autoReplyUserID)
	botPost.AddProp(model.PostPropsDeferredNotification, true)

	require.NoError(t, w.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, w.channel, []string{policyWeatherCall.ID}, nil))
	require.Equal(t, int32(1), w.executions.Load())

	end := w.notifier.waitForRunEnd(t)
	w.notifier.requireNoMoreRunEnds(t)
	assert.True(t, end.PausedForRequester)
	assert.Equal(t, []fakeDelivery{{postID: botPost.Id, userIDs: []string{autoReplyUserID}}}, w.mmClient.deliveredNotifications())
}

func TestRegenerateOfDeliveredPostNotifiesTheRequester(t *testing.T) {
	w := setupNotificationWiring(t, true, dmMakeTextStream("It's still sunny"))
	answer := []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "It's sunny"}}
	botPost := w.seedRun(t, w.dm, nil, humanPost("what is the weather"), answer, nil)
	botPost.AddProp(streaming.LLMRequesterUserIDProp, autoReplyUserID)

	require.NoError(t, w.conversations.HandleRegenerate(context.Background(), autoReplyUserID, botPost, w.dm))

	end := w.notifier.waitForRunEnd(t)
	w.notifier.requireNoMoreRunEnds(t)
	assert.Equal(t, notifications.OutcomeCompleted, end.Outcome)
	assert.False(t, end.PostDeferred)
	assert.Equal(t, []fakeDelivery{{postID: botPost.Id, userIDs: []string{autoReplyUserID}}}, w.mmClient.deliveredNotifications())
}
