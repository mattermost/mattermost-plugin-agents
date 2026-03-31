// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-ai/bifrost"
	"github.com/mattermost/mattermost-plugin-ai/chunking"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/postgres"
	"github.com/maximhq/bifrost/core/schemas"
)

// newVectorStore creates a new vector store based on the provided configuration
func newVectorStore(db *sqlx.DB, config embeddings.UpstreamConfig, dimensions int) (embeddings.VectorStore, error) {
	switch config.Type { //nolint:gocritic
	case embeddings.VectorStoreTypePGVector:
		pgVectorConfig := postgres.PGVectorConfig{
			Dimensions: dimensions,
		}
		if err := json.Unmarshal(config.Parameters, &pgVectorConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal pgvector config: %w", err)
		}
		return postgres.NewPGVector(db, pgVectorConfig)
	}

	return nil, fmt.Errorf("unsupported vector store type: %s", config.Type)
}

// BifrostEmbeddingConfig holds configuration for Bifrost-based embeddings
type BifrostEmbeddingConfig struct {
	Provider string `json:"provider"` // e.g., "openai", "azure", "cohere", "bedrock"
	APIKey   string `json:"apiKey"`
	APIURL   string `json:"apiURL,omitempty"`
	Model    string `json:"model"` // e.g., "text-embedding-3-small"
}

// OpenAIEmbeddingConfig holds configuration for OpenAI-based embeddings (via Bifrost)
type OpenAIEmbeddingConfig struct {
	APIKey string `json:"apiKey"`
	APIURL string `json:"apiURL,omitempty"`
	Model  string `json:"defaultModel"` // e.g., "text-embedding-3-small"
}

// newEmbeddingProvider creates a new embedding provider based on the provided configuration
func newEmbeddingProvider(config embeddings.UpstreamConfig, dimensions int, httpClient *http.Client) (embeddings.EmbeddingProvider, error) {
	switch config.Type {
	case embeddings.ProviderTypeBifrost:
		var bifrostConfig BifrostEmbeddingConfig
		if err := json.Unmarshal(config.Parameters, &bifrostConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal Bifrost config: %w", err)
		}

		provider, err := mapEmbeddingProvider(bifrostConfig.Provider)
		if err != nil {
			return nil, err
		}

		return bifrost.NewEmbeddingProvider(bifrost.EmbeddingConfig{
			Provider:   provider,
			APIKey:     bifrostConfig.APIKey,
			APIURL:     bifrostConfig.APIURL,
			Model:      bifrostConfig.Model,
			Dimensions: dimensions,
		})
	case embeddings.ProviderTypeOpenAICompatible:
		var compatibleConfig OpenAIEmbeddingConfig
		if err := json.Unmarshal(config.Parameters, &compatibleConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal OpenAI-compatible config: %w", err)
		}
		return bifrost.NewEmbeddingProvider(bifrost.EmbeddingConfig{
			Provider:   schemas.OpenAI,
			APIKey:     compatibleConfig.APIKey,
			APIURL:     compatibleConfig.APIURL,
			Model:      compatibleConfig.Model,
			Dimensions: dimensions,
		})
	case embeddings.ProviderTypeOpenAI:
		var openaiConfig OpenAIEmbeddingConfig
		if err := json.Unmarshal(config.Parameters, &openaiConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal OpenAI config: %w", err)
		}
		return bifrost.NewEmbeddingProvider(bifrost.EmbeddingConfig{
			Provider:   schemas.OpenAI,
			APIKey:     openaiConfig.APIKey,
			APIURL:     openaiConfig.APIURL,
			Model:      openaiConfig.Model,
			Dimensions: dimensions,
		})
	case embeddings.ProviderTypeMock:
		return embeddings.NewMockEmbeddingProvider(dimensions), nil
	}

	return nil, fmt.Errorf("unsupported embedding provider type: %s", config.Type)
}

// mapEmbeddingProvider maps provider string to Bifrost ModelProvider
func mapEmbeddingProvider(provider string) (schemas.ModelProvider, error) {
	switch provider {
	case "openai":
		return schemas.OpenAI, nil
	case "azure":
		return schemas.Azure, nil
	case "cohere":
		return schemas.Cohere, nil
	case "bedrock":
		return schemas.Bedrock, nil
	default:
		return "", fmt.Errorf("unsupported embedding provider: %s", provider)
	}
}

// InitEmbeddingsSearch creates and initializes the embedding search system
func InitEmbeddingsSearch(db *sqlx.DB, httpClient *http.Client, cfg embeddings.EmbeddingSearchConfig, licenseChecker *enterprise.LicenseChecker) (embeddings.EmbeddingSearch, error) {
	if cfg.Type == "" {
		// Search is intentionally disabled, not an error
		return nil, nil
	}

	if !licenseChecker.IsBasicsLicensed() {
		return nil, fmt.Errorf("search is unavailable without a valid license")
	}

	if cfg.Dimensions <= 0 {
		return nil, fmt.Errorf("embedding dimensions must be greater than 0, got %d", cfg.Dimensions)
	}

	switch cfg.Type { //nolint:gocritic
	case embeddings.SearchTypeComposite:
		vector, err := newVectorStore(db, cfg.VectorStore, cfg.Dimensions)
		if err != nil {
			return nil, err
		}
		embeddor, err := newEmbeddingProvider(cfg.EmbeddingProvider, cfg.Dimensions, httpClient)
		if err != nil {
			return nil, err
		}

		// Check if we have specific chunking options configured
		chunkingOpts := cfg.ChunkingOptions
		if chunkingOpts.ChunkSize == 0 {
			chunkingOpts = chunking.DefaultOptions()
		}

		return embeddings.NewCompositeSearch(vector, embeddor, chunkingOpts), nil
	}

	return nil, fmt.Errorf("unsupported search type: %s", cfg.Type)
}
