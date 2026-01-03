// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import "github.com/mattermost/mattermost/server/public/model"

// benchmarkClient implements Client for benchmarks with zero overhead.
type benchmarkClient struct{}

func (c *benchmarkClient) PublishWebSocketEvent(_ string, _ map[string]interface{}, _ *model.WebsocketBroadcast) {
}

func (c *benchmarkClient) UpdatePost(_ *model.Post) error {
	return nil
}

func (c *benchmarkClient) CreatePost(_ *model.Post) error {
	return nil
}

func (c *benchmarkClient) DM(_, _ string, _ *model.Post) error {
	return nil
}

func (c *benchmarkClient) GetUser(_ string) (*model.User, error) {
	return &model.User{Locale: "en"}, nil
}

func (c *benchmarkClient) GetChannel(_ string) (*model.Channel, error) {
	return &model.Channel{}, nil
}

func (c *benchmarkClient) GetConfig() *model.Config {
	locale := "en"
	return &model.Config{
		LocalizationSettings: model.LocalizationSettings{
			DefaultServerLocale: &locale,
		},
	}
}

func (c *benchmarkClient) LogError(_ string, _ ...interface{}) {}

func (c *benchmarkClient) LogDebug(_ string, _ ...interface{}) {}
