// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestMaxImageDimension(t *testing.T) {
	tests := []struct {
		name       string
		provider   schemas.ModelProvider
		imageCount int
		want       int
	}{
		{
			name:       "Anthropic single image uses 8000",
			provider:   schemas.Anthropic,
			imageCount: 1,
			want:       anthropicMaxImageDimension,
		},
		{
			name:       "Anthropic at 20 images still uses 8000",
			provider:   schemas.Anthropic,
			imageCount: anthropicManyImageThreshold,
			want:       anthropicMaxImageDimension,
		},
		{
			name:       "Anthropic many-image requests drop to 2000",
			provider:   schemas.Anthropic,
			imageCount: anthropicManyImageThreshold + 1,
			want:       anthropicManyImageDimension,
		},
		{
			name:       "Bedrock follows Anthropic limits",
			provider:   schemas.Bedrock,
			imageCount: 1,
			want:       anthropicMaxImageDimension,
		},
		{
			name:       "Bedrock many-image requests drop to 2000",
			provider:   schemas.Bedrock,
			imageCount: anthropicManyImageThreshold + 1,
			want:       anthropicManyImageDimension,
		},
		{
			name:       "custom Anthropic provider name still matches",
			provider:   customProviderName(schemas.Anthropic, "svc2"),
			imageCount: 1,
			want:       anthropicMaxImageDimension,
		},
		{
			name:       "OpenAI uses the documented 65535 side cap",
			provider:   schemas.OpenAI,
			imageCount: 1,
			want:       openaiMaxImageDimension,
		},
		{
			name:       "Azure follows OpenAI",
			provider:   schemas.Azure,
			imageCount: 100,
			want:       openaiMaxImageDimension,
		},
		{
			name:       "Gemini has no pixel reject",
			provider:   schemas.Gemini,
			imageCount: 1,
			want:       0,
		},
		{
			name:       "Vertex has no pixel reject",
			provider:   schemas.Vertex,
			imageCount: 1,
			want:       0,
		},
		{
			name:       "Cohere has no pixel reject",
			provider:   schemas.Cohere,
			imageCount: 1,
			want:       0,
		},
		{
			name:       "Mistral has no pixel reject",
			provider:   schemas.Mistral,
			imageCount: 1,
			want:       0,
		},
		{
			name:       "unknown provider does not omit on dimensions",
			provider:   "",
			imageCount: 1,
			want:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, maxImageDimension(tt.provider, tt.imageCount))
		})
	}
}

func TestCountRequestImages(t *testing.T) {
	tests := []struct {
		name  string
		posts []llm.Post
		want  int
	}{
		{
			name:  "empty",
			posts: nil,
			want:  0,
		},
		{
			name: "counts supported images across posts",
			posts: []llm.Post{
				{Files: []llm.File{{MimeType: "image/png"}, {MimeType: "image/jpeg"}}},
				{Files: []llm.File{{MimeType: "text/plain"}, {MimeType: "image/webp"}}},
			},
			want: 3,
		},
		{
			name: "ignores unsupported mime types",
			posts: []llm.Post{
				{Files: []llm.File{{MimeType: "application/pdf"}}},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, countRequestImages(tt.posts))
		})
	}
}
