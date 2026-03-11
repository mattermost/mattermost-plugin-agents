// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

func TestNormalizeFetchModelsAPIURL(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		provider    schemas.ModelProvider
		apiURL      string
		expected    string
	}{
		{
			name:        "openai strips trailing /v1",
			serviceType: llm.ServiceTypeOpenAI,
			provider:    schemas.OpenAI,
			apiURL:      "https://api.openai.com/v1",
			expected:    "https://api.openai.com",
		},
		{
			name:        "openaicompatible strips trailing /v1",
			serviceType: llm.ServiceTypeOpenAICompatible,
			provider:    schemas.OpenAI,
			apiURL:      "https://api.openai.com/v1/",
			expected:    "https://api.openai.com",
		},
		{
			name:        "openaicompatible keeps proxy URL path",
			serviceType: llm.ServiceTypeOpenAICompatible,
			provider:    schemas.OpenAI,
			apiURL:      "http://localhost:4000/v1/proxy",
			expected:    "http://localhost:4000/v1/proxy",
		},
		{
			name:        "anthropic URL unchanged",
			serviceType: llm.ServiceTypeAnthropic,
			provider:    schemas.Anthropic,
			apiURL:      "https://api.anthropic.com",
			expected:    "https://api.anthropic.com",
		},
		{
			name:        "cohere default URL applied",
			serviceType: llm.ServiceTypeCohere,
			provider:    schemas.Cohere,
			apiURL:      "",
			expected:    "https://api.cohere.ai/compatibility/v1",
		},
		{
			name:        "mistral default URL applied",
			serviceType: llm.ServiceTypeMistral,
			provider:    schemas.Mistral,
			apiURL:      "",
			expected:    "https://api.mistral.ai/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := normalizeFetchModelsAPIURL(tt.serviceType, tt.provider, tt.apiURL)
			require.Equal(t, tt.expected, actual)
		})
	}
}
