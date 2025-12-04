// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
)

// setupBenchmarkMock creates a MockClient configured for benchmark use.
// Uses Maybe() to allow any number of calls without strict expectations.
func setupBenchmarkMock() *mocks.MockClient {
	client := &mocks.MockClient{}
	client.EXPECT().PublishWebSocketEvent(mock.Anything, mock.Anything, mock.Anything).Maybe()
	client.EXPECT().UpdatePost(mock.Anything).Return(nil).Maybe()
	client.EXPECT().LogDebug(mock.Anything, mock.Anything).Maybe()
	client.EXPECT().LogWarn(mock.Anything, mock.Anything).Maybe()
	client.EXPECT().LogError(mock.Anything, mock.Anything).Maybe()
	return client
}

// BenchmarkStreamToPost benchmarks the core StreamToPost function with varying sizes.
func BenchmarkStreamToPost(b *testing.B) {
	bundle := i18n.Init()
	scenarios := llm.BenchmarkScenarios()

	for _, sc := range scenarios {
		b.Run(sc.Name, func(b *testing.B) {
			client := setupBenchmarkMock()
			service := NewMMPostStreamService(client, bundle)
			ctx := context.Background()

			for b.Loop() {
				stream := sc.Generator.Generate()
				post := &model.Post{
					Id:        "bench-post-id",
					ChannelId: "bench-channel-id",
					Message:   "",
				}

				service.StreamToPost(ctx, stream, post, "en")
			}
		})
	}
}
