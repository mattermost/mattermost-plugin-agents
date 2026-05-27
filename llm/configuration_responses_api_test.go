// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceUsesResponsesAPI(t *testing.T) {
	tests := []struct {
		name     string
		service  ServiceConfig
		expected bool
	}{
		{
			name: "direct OpenAI always uses responses",
			service: ServiceConfig{
				Type:            ServiceTypeOpenAI,
				UseResponsesAPI: false,
			},
			expected: true,
		},
		{
			name: "OpenAI compatible with toggle off",
			service: ServiceConfig{
				Type:            ServiceTypeOpenAICompatible,
				UseResponsesAPI: false,
			},
			expected: false,
		},
		{
			name: "OpenAI compatible with toggle on",
			service: ServiceConfig{
				Type:            ServiceTypeOpenAICompatible,
				UseResponsesAPI: true,
			},
			expected: true,
		},
		{
			name: "Azure with toggle on",
			service: ServiceConfig{
				Type:            ServiceTypeAzure,
				UseResponsesAPI: true,
			},
			expected: true,
		},
		// The admin UI hides the Responses-API toggle for these service types,
		// but the persisted flag can carry over when an operator switches types.
		// The runtime must ignore it.
		{
			name: "Mistral ignores stale toggle",
			service: ServiceConfig{
				Type:            ServiceTypeMistral,
				UseResponsesAPI: true,
			},
			expected: false,
		},
		{
			name: "Cohere ignores stale toggle",
			service: ServiceConfig{
				Type:            ServiceTypeCohere,
				UseResponsesAPI: true,
			},
			expected: false,
		},
		{
			name: "Bedrock ignores stale toggle",
			service: ServiceConfig{
				Type:            ServiceTypeBedrock,
				UseResponsesAPI: true,
			},
			expected: false,
		},
		{
			name: "Anthropic ignores stale toggle",
			service: ServiceConfig{
				Type:            ServiceTypeAnthropic,
				UseResponsesAPI: true,
			},
			expected: false,
		},
		{
			name: "Gemini ignores stale toggle",
			service: ServiceConfig{
				Type:            ServiceTypeGemini,
				UseResponsesAPI: true,
			},
			expected: false,
		},
		{
			name: "Vertex ignores stale toggle",
			service: ServiceConfig{
				Type:            ServiceTypeVertex,
				UseResponsesAPI: true,
			},
			expected: false,
		},
		{
			name: "Scale ignores stale toggle",
			service: ServiceConfig{
				Type:            ServiceTypeScale,
				UseResponsesAPI: true,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ServiceUsesResponsesAPI(tt.service))
		})
	}
}
