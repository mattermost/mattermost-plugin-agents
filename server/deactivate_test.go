// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// deactivateStreamClient is the Mattermost side of the real streaming service.
type deactivateStreamClient struct{}

func (deactivateStreamClient) PublishWebSocketEvent(string, map[string]any, *model.WebsocketBroadcast) {
}
func (deactivateStreamClient) UpdatePost(*model.Post) error         { return nil }
func (deactivateStreamClient) CreatePost(*model.Post) error         { return nil }
func (deactivateStreamClient) DM(string, string, *model.Post) error { return nil }
func (deactivateStreamClient) GetUser(userID string) (*model.User, error) {
	return &model.User{Id: userID, Locale: "en"}, nil
}
func (deactivateStreamClient) GetChannel(channelID string) (*model.Channel, error) {
	return &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil
}
func (deactivateStreamClient) GetConfig() *model.Config {
	cfg := &model.Config{}
	cfg.SetDefaults()
	return cfg
}
func (deactivateStreamClient) KVSet(string, any) error { return nil }
func (deactivateStreamClient) LogError(string, ...any) {}
func (deactivateStreamClient) LogWarn(string, ...any)  {}
func (deactivateStreamClient) LogDebug(string, ...any) {}

type recordingNotifier struct {
	mu   sync.Mutex
	ends []notifications.StreamEnd
}

func (n *recordingNotifier) MarkDeferred(post *model.Post) {
	post.AddProp(model.PostPropsDeferredNotification, true)
}

func (n *recordingNotifier) Notify(_ context.Context, end notifications.StreamEnd) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ends = append(n.ends, end)
}

// TestOnDeactivateEndsInFlightResponses checks that stopping the plugin
// mid-response still ends the run: the requester is notified about the
// unfinished response instead of it staying deferred forever.
func TestOnDeactivateEndsInFlightResponses(t *testing.T) {
	notifier := &recordingNotifier{}
	streams := streaming.NewMMPostStreamService(deactivateStreamClient{}, i18n.Init())
	streams.SetNotifier(notifier)
	p := &Plugin{streamingService: streams}

	post := &model.Post{Id: model.NewId(), ChannelId: model.NewId()}
	never := make(chan llm.TextStreamEvent)
	require.NoError(t, streams.StreamToNewPost(context.Background(), botUserID, recipientUserID, &llm.TextStreamResult{Stream: never}, post, ""))

	require.NoError(t, p.OnDeactivate())

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	require.Len(t, notifier.ends, 1, "the run ended before deactivation returned")
	end := notifier.ends[0]
	require.Equal(t, post.Id, end.PostID)
	require.Equal(t, recipientUserID, end.RequesterID)
	require.Equal(t, notifications.OutcomeCancelled, end.Outcome)
	require.Equal(t, notifications.DeliveryRequesterOnly, notifications.Decide(end))
}
