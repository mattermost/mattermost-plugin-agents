// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

func TestConvertBifrostModels(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	strPtr := func(s string) *string { return &s }

	input := []schemas.Model{
		{
			ID:              "anthropic/claude-sonnet-4-5",
			Name:            strPtr("Claude Sonnet 4.5"),
			MaxInputTokens:  intPtr(200000),
			MaxOutputTokens: intPtr(8192),
			ContextLength:   intPtr(200000),
		},
		{
			// OpenAI only publishes ContextLength; the converter must use it
			// as the InputTokenLimit so the UI can auto-fill.
			ID:            "openai/gpt-4o",
			ContextLength: intPtr(128000),
		},
		{
			// Provider gave us nothing — pointers stay nil.
			ID: "custom-model",
		},
	}

	got := convertBifrostModels(input)
	require.Len(t, got, 3)

	assert.Equal(t, "claude-sonnet-4-5", got[0].ID)
	assert.Equal(t, "Claude Sonnet 4.5", got[0].DisplayName)
	require.NotNil(t, got[0].InputTokenLimit)
	assert.Equal(t, 200000, *got[0].InputTokenLimit)
	require.NotNil(t, got[0].OutputTokenLimit)
	assert.Equal(t, 8192, *got[0].OutputTokenLimit)
	require.NotNil(t, got[0].ContextLength)
	assert.Equal(t, 200000, *got[0].ContextLength)

	assert.Equal(t, "gpt-4o", got[1].ID)
	assert.Equal(t, "gpt-4o", got[1].DisplayName)
	require.NotNil(t, got[1].InputTokenLimit, "InputTokenLimit must fall back to ContextLength")
	assert.Equal(t, 128000, *got[1].InputTokenLimit)
	assert.Nil(t, got[1].OutputTokenLimit, "MaxOutputTokens not provided → nil")
	require.NotNil(t, got[1].ContextLength)
	assert.Equal(t, 128000, *got[1].ContextLength)

	assert.Equal(t, "custom-model", got[2].ID)
	assert.Nil(t, got[2].InputTokenLimit)
	assert.Nil(t, got[2].OutputTokenLimit)
	assert.Nil(t, got[2].ContextLength)
}

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
