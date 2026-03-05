// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

type publishedEvent struct {
	event     string
	payload   map[string]interface{}
	broadcast *model.WebsocketBroadcast
}

type fakeStreamingClient struct {
	channels     map[string]*model.Channel
	kv           map[string]interface{}
	updatedPosts []*model.Post
	events       []publishedEvent
}

func (c *fakeStreamingClient) PublishWebSocketEvent(event string, payload map[string]interface{}, broadcast *model.WebsocketBroadcast) {
	c.events = append(c.events, publishedEvent{
		event:     event,
		payload:   payload,
		broadcast: broadcast,
	})
}

func (c *fakeStreamingClient) UpdatePost(post *model.Post) error {
	c.updatedPosts = append(c.updatedPosts, post.Clone())
	return nil
}

func (c *fakeStreamingClient) CreatePost(_ *model.Post) error {
	return nil
}

func (c *fakeStreamingClient) DM(_, _ string, _ *model.Post) error {
	return nil
}

func (c *fakeStreamingClient) GetUser(_ string) (*model.User, error) {
	return &model.User{Locale: "en"}, nil
}

func (c *fakeStreamingClient) GetChannel(channelID string) (*model.Channel, error) {
	channel, ok := c.channels[channelID]
	if !ok {
		return nil, fmt.Errorf("channel not found")
	}
	return channel, nil
}

func (c *fakeStreamingClient) GetConfig() *model.Config {
	locale := "en"
	return &model.Config{
		LocalizationSettings: model.LocalizationSettings{
			DefaultServerLocale: &locale,
		},
	}
}

func (c *fakeStreamingClient) KVSet(key string, value interface{}) error {
	if c.kv == nil {
		c.kv = make(map[string]interface{})
	}
	c.kv[key] = value
	return nil
}

func (c *fakeStreamingClient) LogError(_ string, _ ...interface{}) {}

func (c *fakeStreamingClient) LogDebug(_ string, _ ...interface{}) {}

func findToolCallEvent(events []publishedEvent) (publishedEvent, bool) {
	for _, event := range events {
		if event.payload["control"] == "tool_call" {
			return event, true
		}
	}
	return publishedEvent{}, false
}

func TestStreamToPostToolCallRedaction(t *testing.T) {
	const (
		postID      = "post-id"
		channelID   = "channel-id"
		botID       = "bot-id"
		requesterID = "requester-id"
	)

	toolCalls := []llm.ToolCall{
		{
			ID:        "tool-1",
			Name:      "test_tool",
			Arguments: json.RawMessage(`{"secret":"value"}`),
			Result:    "sensitive-result",
		},
	}

	testCases := []struct {
		name           string
		channel        *model.Channel
		expectKV       bool
		expectRedacted bool
	}{
		{
			name:           "channel redacts and stores in KV",
			channel:        &model.Channel{Id: channelID, Type: model.ChannelTypeOpen},
			expectKV:       true,
			expectRedacted: true,
		},
		{
			name:           "dm keeps tool calls unredacted",
			channel:        &model.Channel{Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			expectKV:       false,
			expectRedacted: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			client := &fakeStreamingClient{
				channels: map[string]*model.Channel{
					channelID: testCase.channel,
				},
			}
			service := NewMMPostStreamService(client, i18n.Init())

			post := &model.Post{
				Id:        postID,
				ChannelId: channelID,
				UserId:    botID,
			}
			post.AddProp(LLMRequesterUserID, requesterID)

			streamChannel := make(chan llm.TextStreamEvent, 1)
			streamChannel <- llm.TextStreamEvent{
				Type:  llm.EventTypeToolCalls,
				Value: toolCalls,
			}
			close(streamChannel)

			service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

			require.Len(t, client.updatedPosts, 1)

			toolCallProp, ok := post.GetProp(ToolCallProp).(string)
			require.True(t, ok)

			var storedCalls []llm.ToolCall
			require.NoError(t, json.Unmarshal([]byte(toolCallProp), &storedCalls))
			require.Len(t, storedCalls, len(toolCalls))

			if testCase.expectRedacted {
				require.Equal(t, "true", post.GetProp(ToolCallRedactedProp))
				require.NotContains(t, toolCallProp, "secret")
				require.NotContains(t, toolCallProp, "sensitive-result")
				for _, call := range storedCalls {
					require.Equal(t, "{}", string(call.Arguments))
					require.Empty(t, call.Result)
				}
			} else {
				require.Nil(t, post.GetProp(ToolCallRedactedProp))
				require.Contains(t, toolCallProp, "secret")
				for _, call := range storedCalls {
					require.Contains(t, string(call.Arguments), "secret")
				}
			}

			if testCase.expectKV {
				kvKey := ToolCallPrivateKVKey(postID, requesterID)
				storedKV, kvFound := client.kv[kvKey]
				require.True(t, kvFound)
				kvCalls, kvCallsOK := storedKV.([]llm.ToolCall)
				require.True(t, kvCallsOK)
				require.Len(t, kvCalls, len(toolCalls))
				require.Contains(t, string(kvCalls[0].Arguments), "secret")
			} else {
				require.Empty(t, client.kv)
			}

			toolEvent, eventFound := findToolCallEvent(client.events)
			require.True(t, eventFound)
			toolCallPayload, payloadOK := toolEvent.payload["tool_call"].(string)
			require.True(t, payloadOK)
			if testCase.expectRedacted {
				require.NotContains(t, toolCallPayload, "secret")
				require.NotContains(t, toolCallPayload, "sensitive-result")
			} else {
				require.Contains(t, toolCallPayload, "secret")
			}
		})
	}
}
