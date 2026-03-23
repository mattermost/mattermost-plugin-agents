// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

func TestMapServiceTypeToProvider(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		expected    schemas.ModelProvider
		expectError bool
	}{
		{
			name:        "openai maps to OpenAI",
			serviceType: llm.ServiceTypeOpenAI,
			expected:    schemas.OpenAI,
		},
		{
			name:        "vertex maps to Vertex",
			serviceType: llm.ServiceTypeVertex,
			expected:    schemas.Vertex,
		},
		{
			name:        "unsupported service returns error",
			serviceType: "unsupported",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := MapServiceTypeToProvider(tt.serviceType)
			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, provider)
		})
	}
}

func TestIsSupported(t *testing.T) {
	assert.True(t, IsSupported(llm.ServiceTypeVertex))
	assert.False(t, IsSupported("unsupported"))
}

func TestProviderAccountGetKeysForProviderVertex(t *testing.T) {
	account := &providerAccount{
		provider:  schemas.Vertex,
		apiKey:    "{\"type\":\"service_account\"}",
		projectID: "my-gcp-project",
		region:    "us-central1",
	}

	keys, err := account.GetKeysForProvider(context.Background(), schemas.Vertex)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.NotNil(t, keys[0].VertexKeyConfig)

	assert.Equal(t, account.projectID, keys[0].VertexKeyConfig.ProjectID.GetValue())
	assert.Equal(t, account.region, keys[0].VertexKeyConfig.Region.GetValue())
	assert.Equal(t, account.apiKey, keys[0].VertexKeyConfig.AuthCredentials.GetValue())
}
