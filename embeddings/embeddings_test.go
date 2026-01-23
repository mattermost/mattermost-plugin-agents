// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmbeddingSearchConfig_GetModelName(t *testing.T) {
	tests := []struct {
		name     string
		config   EmbeddingSearchConfig
		expected string
	}{
		{
			name: "extracts model name from JSON parameters",
			config: EmbeddingSearchConfig{
				EmbeddingProvider: UpstreamConfig{
					Type:       ProviderTypeOpenAI,
					Parameters: json.RawMessage(`{"embeddingModel": "text-embedding-3-small"}`),
				},
			},
			expected: "text-embedding-3-small",
		},
		{
			name: "extracts different model name",
			config: EmbeddingSearchConfig{
				EmbeddingProvider: UpstreamConfig{
					Type:       ProviderTypeOpenAI,
					Parameters: json.RawMessage(`{"embeddingModel": "text-embedding-ada-002", "apiKey": "test-key"}`),
				},
			},
			expected: "text-embedding-ada-002",
		},
		{
			name: "returns empty string when parameters are nil",
			config: EmbeddingSearchConfig{
				EmbeddingProvider: UpstreamConfig{
					Type:       ProviderTypeOpenAI,
					Parameters: nil,
				},
			},
			expected: "",
		},
		{
			name: "returns empty string when embeddingModel field missing from JSON",
			config: EmbeddingSearchConfig{
				EmbeddingProvider: UpstreamConfig{
					Type:       ProviderTypeOpenAI,
					Parameters: json.RawMessage(`{"apiKey": "test-key"}`),
				},
			},
			expected: "",
		},
		{
			name: "returns empty string for empty JSON object",
			config: EmbeddingSearchConfig{
				EmbeddingProvider: UpstreamConfig{
					Type:       ProviderTypeOpenAI,
					Parameters: json.RawMessage(`{}`),
				},
			},
			expected: "",
		},
		{
			name: "handles malformed JSON gracefully",
			config: EmbeddingSearchConfig{
				EmbeddingProvider: UpstreamConfig{
					Type:       ProviderTypeOpenAI,
					Parameters: json.RawMessage(`{invalid json`),
				},
			},
			expected: "",
		},
		{
			name: "handles empty parameters array",
			config: EmbeddingSearchConfig{
				EmbeddingProvider: UpstreamConfig{
					Type:       ProviderTypeOpenAI,
					Parameters: json.RawMessage(`[]`),
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetModelName()
			assert.Equal(t, tt.expected, result)
		})
	}
}
