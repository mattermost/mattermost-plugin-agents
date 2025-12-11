// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// BenchmarkStreamToPost benchmarks the core StreamToPost function with varying sizes.
func BenchmarkStreamToPost(b *testing.B) {
	bundle := i18n.Init()
	scenarios := llm.BenchmarkScenarios()
	client := &benchmarkClient{}

	for _, sc := range scenarios {
		b.Run(sc.Name, func(b *testing.B) {
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
