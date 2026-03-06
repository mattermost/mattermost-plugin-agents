// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLLMForFallback struct {
	response string
}

func (f *fakeLLMForFallback) ChatCompletion(_ CompletionRequest, _ ...LanguageModelOption) (*TextStreamResult, error) {
	return nil, nil
}

func (f *fakeLLMForFallback) ChatCompletionNoStream(_ CompletionRequest, _ ...LanguageModelOption) (string, error) {
	return f.response, nil
}

func (f *fakeLLMForFallback) CountTokens(_ string) int { return 0 }
func (f *fakeLLMForFallback) InputTokenLimit() int     { return 4096 }

func TestStructuredOutputFallbackWrapper(t *testing.T) {
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()
	withSchema := func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	}

	tests := []struct {
		name                    string
		response                string
		structuredOutputEnabled bool
		opts                    []LanguageModelOption
		expected                string
	}{
		{
			name:                    "schema requested, structured output disabled: strips fencing",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                `{"name": "test"}`,
		},
		{
			name:                    "schema requested, structured output enabled: untouched",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: true,
			opts:                    []LanguageModelOption{withSchema},
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:                    "no schema, structured output disabled: untouched",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: false,
			opts:                    nil,
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:                    "no schema, structured output enabled: untouched",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: true,
			opts:                    nil,
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:                    "no fencing, schema requested, structured output disabled: untouched",
			response:                `{"name": "test"}`,
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                `{"name": "test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapper := NewStructuredOutputFallbackWrapper(
				&fakeLLMForFallback{response: tt.response},
				tt.structuredOutputEnabled,
			)
			result, err := wrapper.ChatCompletionNoStream(CompletionRequest{}, tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
