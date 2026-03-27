// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

// createLicenseChecker creates a LicenseChecker with the specified license state
func createLicenseChecker(t *testing.T, licensed bool) *enterprise.LicenseChecker {
	t.Helper()
	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)

	config := &model.Config{}
	mockAPI.On("GetConfig").Return(config).Maybe()

	if licensed {
		license := &model.License{}
		license.Features = &model.Features{}
		license.Features.SetDefaults()
		license.SkuShortName = model.LicenseShortSkuEnterprise
		mockAPI.On("GetLicense").Return(license).Maybe()
	} else {
		mockAPI.On("GetLicense").Return((*model.License)(nil)).Maybe()
	}

	return enterprise.NewLicenseChecker(client)
}

func TestInitEmbeddingsSearch(t *testing.T) {
	tests := []struct {
		name          string
		cfg           embeddings.EmbeddingSearchConfig
		licensed      bool
		expectError   bool
		errorContains string
		validate      func(t *testing.T, search embeddings.EmbeddingSearch)
	}{
		{
			name: "cfg.Type empty returns nil without error",
			cfg: embeddings.EmbeddingSearchConfig{
				Type:       "",
				Dimensions: 1536,
			},
			licensed:    true,
			expectError: false,
			validate: func(t *testing.T, search embeddings.EmbeddingSearch) {
				require.Nil(t, search)
			},
		},
		{
			name: "missing license returns license error",
			cfg: embeddings.EmbeddingSearchConfig{
				Type:       embeddings.SearchTypeComposite,
				Dimensions: 1536,
			},
			licensed:      false,
			expectError:   true,
			errorContains: "without a valid license",
		},
		{
			name: "zero dimensions returns dimension error",
			cfg: embeddings.EmbeddingSearchConfig{
				Type:       embeddings.SearchTypeComposite,
				Dimensions: 0,
			},
			licensed:      true,
			expectError:   true,
			errorContains: "embedding dimensions must be greater than 0",
		},
		{
			name: "negative dimensions returns dimension error",
			cfg: embeddings.EmbeddingSearchConfig{
				Type:       embeddings.SearchTypeComposite,
				Dimensions: -100,
			},
			licensed:      true,
			expectError:   true,
			errorContains: "embedding dimensions must be greater than 0",
		},
		{
			name: "unsupported search type returns error",
			cfg: embeddings.EmbeddingSearchConfig{
				Type:       "unknown-search-type",
				Dimensions: 1536,
			},
			licensed:      true,
			expectError:   true,
			errorContains: "unsupported search type",
		},
		// Note: Testing default chunking options being applied requires a real database
		// connection. The chunking defaults test is covered in TestChunkingOptionsDefault
		// which verifies the DefaultOptions() function returns proper values.
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			licenseChecker := createLicenseChecker(t, tc.licensed)

			search, err := InitEmbeddingsSearch(nil, &http.Client{}, tc.cfg, licenseChecker)

			if tc.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errorContains)
				require.Nil(t, search)
			} else {
				require.NoError(t, err)
				if tc.validate != nil {
					tc.validate(t, search)
				} else {
					require.NotNil(t, search)
				}
			}
		})
	}
}

func TestNewVectorStore(t *testing.T) {
	tests := []struct {
		name          string
		config        embeddings.UpstreamConfig
		dimensions    int
		expectError   bool
		errorContains string
	}{
		{
			name: "invalid JSON in parameters returns unmarshal error",
			config: embeddings.UpstreamConfig{
				Type:       embeddings.VectorStoreTypePGVector,
				Parameters: json.RawMessage(`{invalid json`),
			},
			dimensions:    1536,
			expectError:   true,
			errorContains: "failed to unmarshal pgvector config",
		},
		{
			name: "unsupported vector store type returns error",
			config: embeddings.UpstreamConfig{
				Type:       "unknown-store",
				Parameters: json.RawMessage(`{}`),
			},
			dimensions:    1536,
			expectError:   true,
			errorContains: "unsupported vector store type",
		},
		// Note: Testing pgvector with nil db would cause a panic (nil pointer dereference)
		// since pgvector.NewPGVector executes SQL immediately. This is expected behavior
		// as the function requires a valid database connection.
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := newVectorStore(nil, tc.config, tc.dimensions)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorContains != "" {
					require.Contains(t, err.Error(), tc.errorContains)
				}
				require.Nil(t, store)
			} else {
				require.NoError(t, err)
				require.NotNil(t, store)
			}
		})
	}
}

func TestNewEmbeddingProvider(t *testing.T) {
	tests := []struct {
		name          string
		config        embeddings.UpstreamConfig
		dimensions    int
		expectError   bool
		errorContains string
		validate      func(t *testing.T, provider embeddings.EmbeddingProvider)
	}{
		{
			name: "OpenAI type with invalid JSON returns unmarshal error",
			config: embeddings.UpstreamConfig{
				Type:       embeddings.ProviderTypeOpenAI,
				Parameters: json.RawMessage(`{invalid json`),
			},
			dimensions:    1536,
			expectError:   true,
			errorContains: "failed to unmarshal OpenAI config",
		},
		{
			name: "OpenAI-compatible type with invalid JSON returns unmarshal error",
			config: embeddings.UpstreamConfig{
				Type:       embeddings.ProviderTypeOpenAICompatible,
				Parameters: json.RawMessage(`{not valid}`),
			},
			dimensions:    1536,
			expectError:   true,
			errorContains: "failed to unmarshal OpenAI-compatible config",
		},
		{
			name: "unsupported embedding provider type returns error",
			config: embeddings.UpstreamConfig{
				Type:       "unknown-provider",
				Parameters: json.RawMessage(`{}`),
			},
			dimensions:    1536,
			expectError:   true,
			errorContains: "unsupported embedding provider type",
		},
		{
			name: "mock provider type creates valid provider",
			config: embeddings.UpstreamConfig{
				Type:       embeddings.ProviderTypeMock,
				Parameters: json.RawMessage(`{}`),
			},
			dimensions:  1536,
			expectError: false,
			validate: func(t *testing.T, provider embeddings.EmbeddingProvider) {
				require.NotNil(t, provider)
				require.Equal(t, 1536, provider.Dimensions())
			},
		},
		{
			name: "mock provider with custom dimensions",
			config: embeddings.UpstreamConfig{
				Type:       embeddings.ProviderTypeMock,
				Parameters: json.RawMessage(`{}`),
			},
			dimensions:  3072,
			expectError: false,
			validate: func(t *testing.T, provider embeddings.EmbeddingProvider) {
				require.NotNil(t, provider)
				require.Equal(t, 3072, provider.Dimensions())
			},
		},
		{
			name: "OpenAI type with valid JSON creates provider",
			config: embeddings.UpstreamConfig{
				Type:       embeddings.ProviderTypeOpenAI,
				Parameters: json.RawMessage(`{"apiKey": "test-key", "embeddingModel": "text-embedding-3-small"}`),
			},
			dimensions:  1536,
			expectError: false,
			validate: func(t *testing.T, provider embeddings.EmbeddingProvider) {
				require.NotNil(t, provider)
			},
		},
		{
			name: "OpenAI-compatible type with valid JSON creates provider",
			config: embeddings.UpstreamConfig{
				Type:       embeddings.ProviderTypeOpenAICompatible,
				Parameters: json.RawMessage(`{"apiKey": "test-key", "apiURL": "http://localhost:8080"}`),
			},
			dimensions:  1536,
			expectError: false,
			validate: func(t *testing.T, provider embeddings.EmbeddingProvider) {
				require.NotNil(t, provider)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := newEmbeddingProvider(tc.config, tc.dimensions, &http.Client{})

			if tc.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errorContains)
				require.Nil(t, provider)
			} else {
				require.NoError(t, err)
				require.NotNil(t, provider)
				if tc.validate != nil {
					tc.validate(t, provider)
				}
			}
		})
	}
}

// Note: TestInitEmbeddingsSearchWithMockProvider is not included because it would
// require a real database connection. The mock provider path is tested through
// TestNewEmbeddingProvider which verifies the mock provider can be created correctly.

func TestEmbeddingProviderConfigUnmarshalEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		providerType  string
		parameters    json.RawMessage
		expectError   bool
		errorContains string
	}{
		{
			name:          "OpenAI with empty parameters succeeds",
			providerType:  embeddings.ProviderTypeOpenAI,
			parameters:    json.RawMessage(`{}`),
			expectError:   false,
			errorContains: "",
		},
		{
			name:          "OpenAI with null parameters fails",
			providerType:  embeddings.ProviderTypeOpenAI,
			parameters:    nil,
			expectError:   true,
			errorContains: "failed to unmarshal OpenAI config",
		},
		{
			name:          "OpenAI-compatible with empty parameters succeeds",
			providerType:  embeddings.ProviderTypeOpenAICompatible,
			parameters:    json.RawMessage(`{}`),
			expectError:   false,
			errorContains: "",
		},
		{
			name:          "OpenAI-compatible with null parameters fails",
			providerType:  embeddings.ProviderTypeOpenAICompatible,
			parameters:    nil,
			expectError:   true,
			errorContains: "failed to unmarshal OpenAI-compatible config",
		},
		{
			name:          "OpenAI with truncated JSON fails",
			providerType:  embeddings.ProviderTypeOpenAI,
			parameters:    json.RawMessage(`{"apiKey": "test`),
			expectError:   true,
			errorContains: "failed to unmarshal OpenAI config",
		},
		{
			name:          "OpenAI-compatible with array instead of object fails",
			providerType:  embeddings.ProviderTypeOpenAICompatible,
			parameters:    json.RawMessage(`["not", "an", "object"]`),
			expectError:   true,
			errorContains: "failed to unmarshal OpenAI-compatible config",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := embeddings.UpstreamConfig{
				Type:       tc.providerType,
				Parameters: tc.parameters,
			}

			provider, err := newEmbeddingProvider(config, 1536, &http.Client{})

			if tc.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errorContains)
				require.Nil(t, provider)
			} else {
				require.NoError(t, err)
				require.NotNil(t, provider)
			}
		})
	}
}

func TestVectorStoreConfigUnmarshalEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		storeType     string
		parameters    json.RawMessage
		expectError   bool
		errorContains string
	}{
		{
			name:          "pgvector with truncated JSON fails",
			storeType:     embeddings.VectorStoreTypePGVector,
			parameters:    json.RawMessage(`{"dimensions": 15`),
			expectError:   true,
			errorContains: "failed to unmarshal pgvector config",
		},
		{
			name:          "pgvector with array instead of object fails",
			storeType:     embeddings.VectorStoreTypePGVector,
			parameters:    json.RawMessage(`[1, 2, 3]`),
			expectError:   true,
			errorContains: "failed to unmarshal pgvector config",
		},
		// Note: pgvector with null parameters would fail at unmarshal before reaching db,
		// but we cannot test this without triggering the nil db panic later.
		// The unmarshal error test is covered by the truncated JSON test case.
		{
			name:          "empty store type returns unsupported error",
			storeType:     "",
			parameters:    json.RawMessage(`{}`),
			expectError:   true,
			errorContains: "unsupported vector store type",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := embeddings.UpstreamConfig{
				Type:       tc.storeType,
				Parameters: tc.parameters,
			}

			store, err := newVectorStore(nil, config, 1536)

			if tc.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errorContains)
				require.Nil(t, store)
			} else {
				require.NoError(t, err)
				require.NotNil(t, store)
			}
		})
	}
}

func TestMockProviderDimensions(t *testing.T) {
	// Test that the mock provider defaults to 1536 for invalid dimension values

	tests := []struct {
		name               string
		dimensions         int
		expectedDimensions int
	}{
		{
			name:               "zero dimensions uses default",
			dimensions:         0,
			expectedDimensions: 1536, // Mock provider defaults to 1536 when 0 is passed
		},
		{
			name:               "negative dimensions uses default",
			dimensions:         -100,
			expectedDimensions: 1536, // Mock provider defaults to 1536 when negative
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := embeddings.UpstreamConfig{
				Type:       embeddings.ProviderTypeMock,
				Parameters: json.RawMessage(`{}`),
			}

			provider, err := newEmbeddingProvider(config, tc.dimensions, &http.Client{})
			require.NoError(t, err)
			require.NotNil(t, provider)
			require.Equal(t, tc.expectedDimensions, provider.Dimensions())
		})
	}
}
