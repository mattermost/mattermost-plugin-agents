// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
)

// TestMessageRulesFor covers hops the failover tests cannot reach: Vertex has
// no test endpoint, and the registration name or image count alone decides the
// rest.
func TestMessageRulesFor(t *testing.T) {
	b := &LLM{account: testAccount(
		&providerAccount{Provider: schemas.OpenAI},
		&providerAccount{Provider: schemas.Vertex},
		&providerAccount{Provider: schemas.Azure},
		&providerAccount{Provider: schemas.Bedrock},
		customAccount(schemas.Anthropic, "svc2"),
	)}

	tests := []struct {
		name       string
		provider   schemas.ModelProvider
		model      string
		imageCount int
		want       messageRules
	}{
		{
			name:     "custom Anthropic registration follows Anthropic",
			provider: customProviderName(schemas.Anthropic, "svc2"), model: "claude-sonnet-4-6",
			want: messageRules{imageDimensionLimit: anthropicMaxImageDimension, strictMessages: true, promptCaching: true},
		},
		{
			name:     "many images tighten Anthropic's limit",
			provider: customProviderName(schemas.Anthropic, "svc2"), model: "claude-sonnet-4-6", imageCount: anthropicManyImageThreshold + 1,
			want: messageRules{imageDimensionLimit: anthropicManyImageDimension, strictMessages: true, promptCaching: true},
		},
		{
			name:     "Claude on Vertex follows Anthropic's messages API without prompt caching",
			provider: schemas.Vertex, model: "claude-sonnet-4-6@20250929",
			want: messageRules{imageDimensionLimit: anthropicMaxImageDimension, strictMessages: true},
		},
		{
			name:     "Claude on Azure follows Anthropic's messages API without prompt caching",
			provider: schemas.Azure, model: "claude-sonnet-4-6",
			want: messageRules{imageDimensionLimit: anthropicMaxImageDimension, strictMessages: true},
		},
		{
			name:     "Gemini on Vertex has no extra rules",
			provider: schemas.Vertex, model: "gemini-2.5-pro",
		},
		{
			name:     "GPT on Azure follows OpenAI's image limit",
			provider: schemas.Azure, model: "gpt-5",
			want: messageRules{imageDimensionLimit: openaiMaxImageDimension},
		},
		{
			name:     "Bedrock is strict without prompt caching",
			provider: schemas.Bedrock, model: "anthropic.claude-sonnet-4-6",
			want: messageRules{imageDimensionLimit: anthropicMaxImageDimension, strictMessages: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, b.messageRulesFor(tt.provider, tt.model, tt.imageCount))
		})
	}
}
