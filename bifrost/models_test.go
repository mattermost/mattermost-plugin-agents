// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

func TestDefaultServiceAPIURL(t *testing.T) {
	tests := []struct {
		name    string
		service llm.ServiceConfig
		want    string
	}{
		{
			name: "openai compatible keeps explicit url",
			service: llm.ServiceConfig{
				Type:   llm.ServiceTypeOpenAICompatible,
				APIURL: "http://localhost:4000/v1",
			},
			want: "http://localhost:4000/v1",
		},
		{
			name: "cohere gets default url",
			service: llm.ServiceConfig{
				Type: llm.ServiceTypeCohere,
			},
			want: "https://api.cohere.ai/compatibility/v1",
		},
		{
			name: "mistral gets default url",
			service: llm.ServiceConfig{
				Type: llm.ServiceTypeMistral,
			},
			want: "https://api.mistral.ai/v1",
		},
		{
			name: "other provider leaves empty url empty",
			service: llm.ServiceConfig{
				Type: "vertex",
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, defaultServiceAPIURL(tt.service))
		})
	}
}

func TestNormalizeListedModelID(t *testing.T) {
	tests := []struct {
		name     string
		modelID  string
		provider schemas.ModelProvider
		want     string
	}{
		{
			name:     "strips matching provider prefix",
			modelID:  "anthropic/claude-sonnet-4-20250514",
			provider: schemas.Anthropic,
			want:     "claude-sonnet-4-20250514",
		},
		{
			name:     "preserves nested openrouter vendor model path",
			modelID:  "openrouter/openai/gpt-4o",
			provider: schemas.OpenRouter,
			want:     "openai/gpt-4o",
		},
		{
			name:     "preserves vendor namespace when prefix is not a known provider",
			modelID:  "meta-llama/Llama-3.1-8B-Instruct",
			provider: schemas.VLLM,
			want:     "meta-llama/Llama-3.1-8B-Instruct",
		},
		{
			name:     "preserves original id when leading provider does not match service provider",
			modelID:  "openai/gpt-4o",
			provider: schemas.OpenRouter,
			want:     "openai/gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeListedModelID(tt.modelID, tt.provider))
		})
	}
}
