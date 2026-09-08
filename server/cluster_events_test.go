// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// recordingStreamingService captures StopStreaming calls so the cluster
// event handler can be tested without spinning up the real streaming
// machinery. Other Service methods are unused by OnPluginClusterEvent.
type recordingStreamingService struct {
	stoppedPostIDs []string
}

func (s *recordingStreamingService) StreamToNewPost(_ context.Context, _, _ string, _ *llm.TextStreamResult, _ *model.Post, _ string) error {
	return nil
}

func (s *recordingStreamingService) StreamToNewDM(_ context.Context, _ string, _ *llm.TextStreamResult, _ string, _ *model.Post, _ string) error {
	return nil
}

func (s *recordingStreamingService) StreamToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}

func (s *recordingStreamingService) StreamContinuationToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}

func (s *recordingStreamingService) StopStreaming(postID string) {
	s.stoppedPostIDs = append(s.stoppedPostIDs, postID)
}

func (s *recordingStreamingService) GetStreamingContext(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}

func (s *recordingStreamingService) FinishStreaming(string) {}

var _ streaming.Service = (*recordingStreamingService)(nil)

// recordingAutoReplyRefresher captures RefreshChannel calls so the cluster
// event handler can be tested without a database.
type recordingAutoReplyRefresher struct {
	refreshed  []string
	refreshErr error
}

func (r *recordingAutoReplyRefresher) RefreshChannel(channelID string) error {
	r.refreshed = append(r.refreshed, channelID)
	return r.refreshErr
}

func TestOnPluginClusterEventStreamStop(t *testing.T) {
	tests := []struct {
		name           string
		event          model.PluginClusterEvent
		expectStopped  []string
		expectErrorLog string
	}{
		{
			name: "valid event triggers local StopStreaming",
			event: model.PluginClusterEvent{
				Id:   clusterEventStreamStop,
				Data: mustMarshal(t, streamStopClusterPayload{PostID: "post12345678901234567890ab"}),
			},
			expectStopped: []string{"post12345678901234567890ab"},
		},
		{
			name: "malformed payload is logged and ignored",
			event: model.PluginClusterEvent{
				Id:   clusterEventStreamStop,
				Data: []byte("not json"),
			},
			expectStopped:  nil,
			expectErrorLog: "Failed to unmarshal stream stop cluster payload",
		},
		{
			name: "empty postID is logged and ignored",
			event: model.PluginClusterEvent{
				Id:   clusterEventStreamStop,
				Data: mustMarshal(t, streamStopClusterPayload{}),
			},
			expectStopped:  nil,
			expectErrorLog: "Received stream stop cluster event with empty postID",
		},
		{
			name: "unrelated event id is a no-op",
			event: model.PluginClusterEvent{
				Id:   "some_other_event",
				Data: mustMarshal(t, streamStopClusterPayload{PostID: "post12345678901234567890ab"}),
			},
			expectStopped: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)

			if test.expectErrorLog != "" {
				mockAPI.On("LogError",
					test.expectErrorLog,
					mock.Anything, mock.Anything,
				).Once()
			}

			streamingSvc := &recordingStreamingService{}
			p := &Plugin{
				pluginAPI:        pluginapi.NewClient(mockAPI, nil),
				streamingService: streamingSvc,
			}
			p.SetAPI(mockAPI)

			p.OnPluginClusterEvent(&plugin.Context{}, test.event)

			require.Equal(t, test.expectStopped, streamingSvc.stoppedPostIDs)
		})
	}
}

// TestStreamStopClusterRoundTrip wires PublishStreamStop on a publisher
// Plugin into OnPluginClusterEvent on a receiver Plugin to prove the
// payload format the publisher emits is the payload format the receiver
// decodes. This is the test that would have caught the original MM-67491
// HA bug: divergence between the two halves would let the stream stop click
// silently drop on peer nodes.
func TestStreamStopClusterRoundTrip(t *testing.T) {
	const postID = "post12345678901234567890ab"

	publisherAPI := &plugintest.API{}
	defer publisherAPI.AssertExpectations(t)
	receiverAPI := &plugintest.API{}
	defer receiverAPI.AssertExpectations(t)

	receiverStreaming := &recordingStreamingService{}
	receiver := &Plugin{
		pluginAPI:        pluginapi.NewClient(receiverAPI, nil),
		streamingService: receiverStreaming,
	}
	receiver.SetAPI(receiverAPI)

	publisher := &Plugin{
		pluginAPI: pluginapi.NewClient(publisherAPI, nil),
	}
	publisher.SetAPI(publisherAPI)

	// Capture the broadcast event on the publisher and feed it verbatim
	// to the receiver's OnPluginClusterEvent. This is exactly what the
	// Mattermost cluster does across nodes: reliable delivery of the
	// serialized event.
	publisherAPI.On("PublishPluginClusterEvent",
		mock.AnythingOfType("model.PluginClusterEvent"),
		mock.MatchedBy(func(opts model.PluginClusterEventSendOptions) bool {
			return opts.SendType == model.PluginClusterEventSendTypeReliable
		}),
	).Return(nil).Run(func(args mock.Arguments) {
		ev := args.Get(0).(model.PluginClusterEvent)
		receiver.OnPluginClusterEvent(&plugin.Context{}, ev)
	}).Once()

	require.NoError(t, publisher.PublishStreamStop(postID))

	require.Equal(t, []string{postID}, receiverStreaming.stoppedPostIDs,
		"a published stream stop must be decoded and applied by peer nodes verbatim")
}

// TestOnPluginClusterEventStreamStopWithoutService verifies the handler is
// safe when the streaming service has not yet been wired (e.g. an event
// arrives during plugin activation).
func TestOnPluginClusterEventStreamStopWithoutService(t *testing.T) {
	mockAPI := &plugintest.API{}
	defer mockAPI.AssertExpectations(t)

	p := &Plugin{
		pluginAPI: pluginapi.NewClient(mockAPI, nil),
	}
	p.SetAPI(mockAPI)

	require.NotPanics(t, func() {
		p.OnPluginClusterEvent(&plugin.Context{}, model.PluginClusterEvent{
			Id:   clusterEventStreamStop,
			Data: mustMarshal(t, streamStopClusterPayload{PostID: "post12345678901234567890ab"}),
		})
	})
}

func TestPublishStreamStop(t *testing.T) {
	tests := []struct {
		name             string
		postID           string
		publishErr       error
		expectPublish    bool
		expectReturnErr  bool
		expectPayloadHas string
	}{
		{
			name:             "publishes payload with postID",
			postID:           "post12345678901234567890ab",
			expectPublish:    true,
			expectPayloadHas: "post12345678901234567890ab",
		},
		{
			name:            "empty postID is a no-op",
			postID:          "",
			expectPublish:   false,
			expectReturnErr: false,
		},
		{
			name:             "publish error is returned",
			postID:           "post12345678901234567890ab",
			publishErr:       errors.New("boom"),
			expectPublish:    true,
			expectReturnErr:  true,
			expectPayloadHas: "post12345678901234567890ab",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)

			if test.expectPublish {
				mockAPI.On("PublishPluginClusterEvent",
					mock.MatchedBy(func(ev model.PluginClusterEvent) bool {
						if ev.Id != clusterEventStreamStop {
							return false
						}
						var payload streamStopClusterPayload
						if err := json.Unmarshal(ev.Data, &payload); err != nil {
							return false
						}
						return payload.PostID == test.expectPayloadHas
					}),
					mock.MatchedBy(func(opts model.PluginClusterEventSendOptions) bool {
						return opts.SendType == model.PluginClusterEventSendTypeReliable
					}),
				).Return(test.publishErr).Once()
			}

			if test.expectReturnErr {
				mockAPI.On("LogError",
					"Failed to publish cluster event",
					mock.Anything, mock.Anything, mock.Anything, mock.Anything,
				).Once()
			}

			p := &Plugin{
				pluginAPI: pluginapi.NewClient(mockAPI, nil),
			}
			p.SetAPI(mockAPI)

			err := p.PublishStreamStop(test.postID)
			if test.expectReturnErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPublishChannelAutoReplyInvalidate(t *testing.T) {
	tests := []struct {
		name             string
		channelID        string
		publishErr       error
		expectPublish    bool
		expectReturnErr  bool
		expectPayloadHas string
	}{
		{
			name:             "publishes payload with channelID",
			channelID:        "channel1234567890123456789ab",
			expectPublish:    true,
			expectPayloadHas: "channel1234567890123456789ab",
		},
		{
			name:            "empty channelID is a no-op",
			channelID:       "",
			expectPublish:   false,
			expectReturnErr: false,
		},
		{
			name:             "publish error is returned",
			channelID:        "channel1234567890123456789ab",
			publishErr:       errors.New("boom"),
			expectPublish:    true,
			expectReturnErr:  true,
			expectPayloadHas: "channel1234567890123456789ab",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)

			if test.expectPublish {
				mockAPI.On("PublishPluginClusterEvent",
					mock.MatchedBy(func(ev model.PluginClusterEvent) bool {
						if ev.Id != clusterEventChannelAutoReplyInvalidate {
							return false
						}
						var payload channelAutoReplyInvalidateClusterPayload
						if err := json.Unmarshal(ev.Data, &payload); err != nil {
							return false
						}
						return payload.ChannelID == test.expectPayloadHas
					}),
					mock.MatchedBy(func(opts model.PluginClusterEventSendOptions) bool {
						return opts.SendType == model.PluginClusterEventSendTypeReliable
					}),
				).Return(test.publishErr).Once()
			}

			if test.expectReturnErr {
				mockAPI.On("LogError",
					"Failed to publish cluster event",
					mock.Anything, mock.Anything, mock.Anything, mock.Anything,
				).Once()
			}

			p := &Plugin{
				pluginAPI: pluginapi.NewClient(mockAPI, nil),
			}
			p.SetAPI(mockAPI)

			err := p.PublishChannelAutoReplyInvalidate(test.channelID)
			if test.expectReturnErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOnPluginClusterEventChannelAutoReplyInvalidate(t *testing.T) {
	tests := []struct {
		name            string
		event           model.PluginClusterEvent
		refreshErr      error
		expectRefreshed []string
		expectErrorLog  string
	}{
		{
			name: "valid event triggers local RefreshChannel",
			event: model.PluginClusterEvent{
				Id:   clusterEventChannelAutoReplyInvalidate,
				Data: mustMarshal(t, channelAutoReplyInvalidateClusterPayload{ChannelID: "channel1234567890123456789ab"}),
			},
			expectRefreshed: []string{"channel1234567890123456789ab"},
		},
		{
			name: "malformed payload is logged and ignored",
			event: model.PluginClusterEvent{
				Id:   clusterEventChannelAutoReplyInvalidate,
				Data: []byte("not json"),
			},
			expectRefreshed: nil,
			expectErrorLog:  "Failed to unmarshal channel auto-reply cluster invalidation payload",
		},
		{
			name: "empty channelID is logged and ignored",
			event: model.PluginClusterEvent{
				Id:   clusterEventChannelAutoReplyInvalidate,
				Data: mustMarshal(t, channelAutoReplyInvalidateClusterPayload{}),
			},
			expectRefreshed: nil,
			expectErrorLog:  "Received channel auto-reply cluster invalidation with empty channelID",
		},
		{
			name: "unrelated event id is a no-op",
			event: model.PluginClusterEvent{
				Id:   "some_other_event",
				Data: mustMarshal(t, channelAutoReplyInvalidateClusterPayload{ChannelID: "channel1234567890123456789ab"}),
			},
			expectRefreshed: nil,
		},
		{
			name: "refresh error is logged and does not panic",
			event: model.PluginClusterEvent{
				Id:   clusterEventChannelAutoReplyInvalidate,
				Data: mustMarshal(t, channelAutoReplyInvalidateClusterPayload{ChannelID: "channel1234567890123456789ab"}),
			},
			refreshErr:      errors.New("db down"),
			expectRefreshed: []string{"channel1234567890123456789ab"},
			expectErrorLog:  "Failed to refresh channel auto-reply cache on cluster event",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)

			if test.expectErrorLog != "" {
				mockAPI.On("LogError",
					test.expectErrorLog,
					mock.Anything, mock.Anything, mock.Anything, mock.Anything,
				).Once()
			}

			refresher := &recordingAutoReplyRefresher{refreshErr: test.refreshErr}
			p := &Plugin{
				pluginAPI:        pluginapi.NewClient(mockAPI, nil),
				autoreplyService: refresher,
			}
			p.SetAPI(mockAPI)

			require.NotPanics(t, func() {
				p.OnPluginClusterEvent(&plugin.Context{}, test.event)
			})

			require.Equal(t, test.expectRefreshed, refresher.refreshed)
		})
	}
}

// TestChannelAutoReplyClusterRoundTrip wires PublishChannelAutoReplyInvalidate
// on a publisher Plugin into OnPluginClusterEvent on a receiver Plugin to
// prove the payload format the publisher emits is the payload format the
// receiver decodes. Divergence between the two halves would let peer nodes
// silently serve stale auto-reply settings.
func TestChannelAutoReplyClusterRoundTrip(t *testing.T) {
	const channelID = "channel1234567890123456789ab"

	publisherAPI := &plugintest.API{}
	defer publisherAPI.AssertExpectations(t)
	receiverAPI := &plugintest.API{}
	defer receiverAPI.AssertExpectations(t)

	receiverRefresher := &recordingAutoReplyRefresher{}
	receiver := &Plugin{
		pluginAPI:        pluginapi.NewClient(receiverAPI, nil),
		autoreplyService: receiverRefresher,
	}
	receiver.SetAPI(receiverAPI)

	publisher := &Plugin{
		pluginAPI: pluginapi.NewClient(publisherAPI, nil),
	}
	publisher.SetAPI(publisherAPI)

	// Capture the broadcast event on the publisher and feed it verbatim
	// to the receiver's OnPluginClusterEvent, exactly like the Mattermost
	// cluster does across nodes.
	publisherAPI.On("PublishPluginClusterEvent",
		mock.AnythingOfType("model.PluginClusterEvent"),
		mock.MatchedBy(func(opts model.PluginClusterEventSendOptions) bool {
			return opts.SendType == model.PluginClusterEventSendTypeReliable
		}),
	).Return(nil).Run(func(args mock.Arguments) {
		ev := args.Get(0).(model.PluginClusterEvent)
		receiver.OnPluginClusterEvent(&plugin.Context{}, ev)
	}).Once()

	require.NoError(t, publisher.PublishChannelAutoReplyInvalidate(channelID))

	require.Equal(t, []string{channelID}, receiverRefresher.refreshed,
		"a published auto-reply invalidation must be decoded and applied by peer nodes verbatim")
}

// TestOnPluginClusterEventChannelAutoReplyWithoutService verifies the handler
// is safe when the auto-reply service has not yet been wired (e.g. an event
// arrives during plugin activation).
func TestOnPluginClusterEventChannelAutoReplyWithoutService(t *testing.T) {
	mockAPI := &plugintest.API{}
	defer mockAPI.AssertExpectations(t)

	p := &Plugin{
		pluginAPI: pluginapi.NewClient(mockAPI, nil),
	}
	p.SetAPI(mockAPI)

	require.NotPanics(t, func() {
		p.OnPluginClusterEvent(&plugin.Context{}, model.PluginClusterEvent{
			Id:   clusterEventChannelAutoReplyInvalidate,
			Data: mustMarshal(t, channelAutoReplyInvalidateClusterPayload{ChannelID: "channel1234567890123456789ab"}),
		})
	})
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
