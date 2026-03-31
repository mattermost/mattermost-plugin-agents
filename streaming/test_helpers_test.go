// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
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
