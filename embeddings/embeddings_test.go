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

func TestEmbeddingSearchConfig_ReindexThroughputSettings(t *testing.T) {
	tests := []struct {
		name          string
		config        EmbeddingSearchConfig
		wantWorkers   int
		wantBatchSize int
	}{
		{
			name:          "unset values fall back to defaults",
			config:        EmbeddingSearchConfig{},
			wantWorkers:   DefaultReindexWorkers,
			wantBatchSize: DefaultReindexBatchSize,
		},
		{
			name:          "negative values fall back to defaults",
			config:        EmbeddingSearchConfig{ReindexWorkers: -1, ReindexBatchSize: -5},
			wantWorkers:   DefaultReindexWorkers,
			wantBatchSize: DefaultReindexBatchSize,
		},
		{
			name:          "configured values within bounds are used",
			config:        EmbeddingSearchConfig{ReindexWorkers: 8, ReindexBatchSize: 500},
			wantWorkers:   8,
			wantBatchSize: 500,
		},
		{
			name:          "values above maximum are clamped",
			config:        EmbeddingSearchConfig{ReindexWorkers: 1000, ReindexBatchSize: 100000},
			wantWorkers:   MaxReindexWorkers,
			wantBatchSize: MaxReindexBatchSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantWorkers, tt.config.GetReindexWorkers())
			assert.Equal(t, tt.wantBatchSize, tt.config.GetReindexBatchSize())
		})
	}
}

func TestEmbeddingSearchConfig_GetHNSWM(t *testing.T) {
	tests := []struct {
		name  string
		hnswM int
		want  int
	}{
		{name: "unset falls back to default", hnswM: 0, want: DefaultHNSWM},
		{name: "negative falls back to default", hnswM: -1, want: DefaultHNSWM},
		{name: "configured default is kept", hnswM: 8, want: 8},
		{name: "legacy pgvector default is kept", hnswM: 16, want: 16},
		{name: "maximum is kept", hnswM: 100, want: MaxHNSWM},
		{name: "above maximum is clamped", hnswM: 101, want: MaxHNSWM},
		{name: "below minimum is clamped", hnswM: 1, want: MinHNSWM},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := EmbeddingSearchConfig{HNSWM: tt.hnswM}
			assert.Equal(t, tt.want, config.GetHNSWM())
		})
	}
}

func TestEmbeddingSearchConfig_GetVectorElementType(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "unset falls back to vector", value: "", want: VectorElementTypeVector},
		{name: "vector is kept", value: VectorElementTypeVector, want: VectorElementTypeVector},
		{name: "halfvec is kept", value: VectorElementTypeHalfvec, want: VectorElementTypeHalfvec},
		{name: "unknown falls back to vector", value: "int8", want: VectorElementTypeVector},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeVectorElementType(tt.value))
			config := EmbeddingSearchConfig{VectorElementType: tt.value}
			assert.Equal(t, tt.want, config.GetVectorElementType())
		})
	}
}

func TestEmbeddingSearchConfig_EffectiveReindexIndexStrategy(t *testing.T) {
	tests := []struct {
		name     string
		strategy string
		want     string
	}{
		{
			name:     "unset falls back to maintain",
			strategy: "",
			want:     ReindexIndexStrategyMaintain,
		},
		{
			name:     "explicit maintain is kept",
			strategy: ReindexIndexStrategyMaintain,
			want:     ReindexIndexStrategyMaintain,
		},
		{
			name:     "explicit defer is kept",
			strategy: ReindexIndexStrategyDefer,
			want:     ReindexIndexStrategyDefer,
		},
		{
			name:     "unknown value normalizes to maintain",
			strategy: "bogus",
			want:     ReindexIndexStrategyMaintain,
		},
		{
			name:     "case-sensitive: uppercase DEFER normalizes to maintain",
			strategy: "DEFER",
			want:     ReindexIndexStrategyMaintain,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := EmbeddingSearchConfig{ReindexIndexStrategy: tt.strategy}
			assert.Equal(t, tt.want, config.EffectiveReindexIndexStrategy())
		})
	}
}
