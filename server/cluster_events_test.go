// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
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

func TestOnPluginClusterEventStreamStop(t *testing.T) {
	tests := []struct {
		name           string
		event          model.PluginClusterEvent
		expectStopped  []string
		expectLogError bool
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
			expectLogError: true,
		},
		{
			name: "empty postID is logged and ignored",
			event: model.PluginClusterEvent{
				Id:   clusterEventStreamStop,
				Data: mustMarshal(t, streamStopClusterPayload{}),
			},
			expectStopped:  nil,
			expectLogError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)

			if test.expectLogError {
				mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()
				mockAPI.On("LogError", mock.Anything).Maybe()
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
				mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
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

func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
