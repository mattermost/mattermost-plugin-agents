// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/api"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
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

func (s *recordingStreamingService) StreamToNewDM(_ context.Context, _ string, _ *llm.TextStreamResult, _ string, _ *model.Post, _ string, _ func()) error {
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

type agentListStub []llm.BotConfig

func (s agentListStub) ListAgents() ([]*llm.BotConfig, error) {
	out := make([]*llm.BotConfig, len(s))
	for i := range s {
		cfg := s[i]
		out[i] = &cfg
	}
	return out, nil
}

// publishedAgentUpdate returns the cluster event PublishAgentUpdate(agentID)
// broadcasts.
func publishedAgentUpdate(t *testing.T, agentID string) model.PluginClusterEvent {
	t.Helper()
	publisherAPI := &plugintest.API{}
	defer publisherAPI.AssertExpectations(t)
	var published model.PluginClusterEvent
	publisherAPI.On("PublishPluginClusterEvent",
		mock.AnythingOfType("model.PluginClusterEvent"),
		mock.MatchedBy(func(opts model.PluginClusterEventSendOptions) bool {
			return opts.SendType == model.PluginClusterEventSendTypeReliable
		}),
	).Return(nil).Run(func(args mock.Arguments) {
		published = args.Get(0).(model.PluginClusterEvent)
	}).Once()

	publisher := &Plugin{pluginAPI: pluginapi.NewClient(publisherAPI, nil)}
	publisher.SetAPI(publisherAPI)
	require.NoError(t, publisher.PublishAgentUpdate(agentID))
	require.Equal(t, clusterEventAgentUpdate, published.Id)
	return published
}

func TestPublishAgentUpdatePayload(t *testing.T) {
	tests := []struct {
		name          string
		agentID       string
		expectPayload bool
	}{
		{name: "no agent publishes no data"},
		{name: "renamed agent publishes its ID", agentID: "agent1234567890123456789ab", expectPayload: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ev := publishedAgentUpdate(t, test.agentID)
			if !test.expectPayload {
				require.Empty(t, ev.Data, "nodes that predate the payload must keep receiving a plain agent update")
				return
			}
			var payload agentUpdateClusterPayload
			require.NoError(t, json.Unmarshal(ev.Data, &payload))
			require.Equal(t, agentUpdateClusterPayload{AgentID: test.agentID}, payload)
		})
	}
}

// TestOnPluginClusterEventAgentUpdateRefreshesRenamedAgent delivers an agent
// update to a peer whose registry still lists a renamed agent under its old
// name and whose full bot reload fails. Only the agent ID in the payload lets
// the peer list the agent under its new name; the old name, which the bot
// account released, is refused either way.
func TestOnPluginClusterEventAgentUpdateRefreshesRenamedAgent(t *testing.T) {
	tests := []struct {
		name            string
		event           model.PluginClusterEvent
		expectRefreshed bool
	}{
		{
			name:            "event naming the renamed agent",
			event:           publishedAgentUpdate(t, "a1"),
			expectRefreshed: true,
		},
		{
			name:  "event without an agent",
			event: publishedAgentUpdate(t, ""),
		},
		{
			name:  "malformed payload",
			event: model.PluginClusterEvent{Id: clusterEventAgentUpdate, Data: []byte("not json")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			client := pluginapi.NewClient(mockAPI, nil)
			mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
			mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterprise, Features: &model.Features{}}).Maybe()
			mockAPI.On("GetBot", "bot-1", true).Return(&model.Bot{UserId: "bot-1", Username: "new-name", OwnerId: "mattermost-ai"}, nil).Maybe()
			mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{UserId: "bot-1", Username: "new-name"}, nil).Maybe()
			mockAPI.On("UpdateBotActive", "bot-1", true).Return(&model.Bot{}, nil).Maybe()
			mockAPI.On("GetUser", "bot-1").Return(&model.User{Id: "bot-1", Username: "new-name", IsBot: true, LastPictureUpdate: 1}, nil).Maybe()
			mockAPI.On("GetUserByUsername", "old-name").
				Return(nil, model.NewAppError("GetUserByUsername", "app.user.missing_account.const", nil, "", http.StatusNotFound))
			mockAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
			mockAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()
			mockAPI.On("GetBots", mock.AnythingOfType("*model.BotGetOptions")).
				Return(nil, model.NewAppError("GetBots", "app.bot.get_bots.app_error", nil, "", http.StatusInternalServerError)).Once()
			mockAPI.On("PublishWebSocketEvent", api.WebsocketEventBotsInvalidate, mock.Anything, mock.Anything).Once()
			for arity := 1; arity <= 9; arity++ {
				args := make([]any, arity)
				for i := range args {
					args[i] = mock.Anything
				}
				mockAPI.On("LogError", args...).Maybe()
				mockAPI.On("LogDebug", args...).Maybe()
			}
			defer mockAPI.AssertExpectations(t)

			var cfg config.Container
			cfg.Update(&config.Config{Services: []llm.ServiceConfig{{ID: "svc1", Type: llm.ServiceTypeOpenAI, APIKey: "k", DefaultModel: "gpt-4o"}}})
			store := agentListStub{{ID: "a1", BotUserID: "bot-1", Name: "new-name", DisplayName: "Agent", ServiceID: "svc1"}}
			accessChecker := accesscontrol.New(accesscontrol.PassthroughClient{}, nil, accesscontrol.NoMCPServerIDs, nil)
			mmBots := bots.New(mockAPI, client, enterprise.NewLicenseChecker(client), &cfg, store, accessChecker, &http.Client{}, nil)
			t.Cleanup(mmBots.ShutdownServiceLLMs)
			mmBots.SetBotsForTesting([]*bots.Bot{
				bots.NewBot(llm.BotConfig{ID: "a1", BotUserID: "bot-1", Name: "old-name", ServiceID: "svc1"}, llm.ServiceConfig{}, &model.Bot{UserId: "bot-1", Username: "old-name"}, nil),
			})

			p := &Plugin{pluginAPI: client, bots: mmBots}
			p.SetAPI(mockAPI)
			p.OnPluginClusterEvent(&plugin.Context{}, test.event)

			require.Nil(t, mmBots.GetBotMentioned("@old-name hi"), "the peer must not answer to the name the agent's account released")
			require.Nil(t, mmBots.GetBotByUsername("old-name"), "the peer must not select the agent by the name its account released")
			registered := mmBots.GetBotByID("bot-1")
			require.NotNil(t, registered)
			newMention := mmBots.GetBotMentioned("@new-name hi")
			if test.expectRefreshed {
				require.Equal(t, "new-name", registered.GetMMBot().Username)
				require.NotNil(t, newMention)
				require.Equal(t, "a1", newMention.GetConfig().ID)
				return
			}
			require.Equal(t, "old-name", registered.GetMMBot().Username, "without an agent ID the peer keeps its previous registry when the full reload fails")
			require.Nil(t, newMention)
		})
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
