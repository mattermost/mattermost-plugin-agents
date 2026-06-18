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
			name: "OpenAI compatible respects toggle",
			service: ServiceConfig{
				Type:            ServiceTypeOpenAICompatible,
				UseResponsesAPI: false,
			},
			expected: false,
		},
		{
			name: "Azure respects toggle",
			service: ServiceConfig{
				Type:            ServiceTypeAzure,
				UseResponsesAPI: true,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ServiceUsesResponsesAPI(tt.service))
		})
	}
}
