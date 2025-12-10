// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-ai/chunking"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/openai"
	"github.com/mattermost/mattermost-plugin-ai/postgres"
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

// newEmbeddingProvider creates a new embedding provider based on the provided configuration
func newEmbeddingProvider(config embeddings.UpstreamConfig, dimensions int, httpClient *http.Client) (embeddings.EmbeddingProvider, error) {
	switch config.Type {
	case embeddings.ProviderTypeOpenAICompatible:
		compatibleConfig := openai.Config{}
		if err := json.Unmarshal(config.Parameters, &compatibleConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal OpenAI-compatible config: %w", err)
		}
		compatibleConfig.EmbeddingDimensions = dimensions
		return openai.NewCompatibleEmbeddings(compatibleConfig, httpClient), nil
	case embeddings.ProviderTypeOpenAI:
		var openaiConfig openai.Config
		if err := json.Unmarshal(config.Parameters, &openaiConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal OpenAI config: %w", err)
		}
		openaiConfig.EmbeddingDimensions = dimensions
		return openai.NewEmbeddings(openaiConfig, httpClient), nil
	case embeddings.ProviderTypeMock:
		return embeddings.NewMockEmbeddingProvider(dimensions), nil
	}

	return nil, fmt.Errorf("unsupported embedding provider type: %s", config.Type)
}

// InitEmbeddingsSearch creates and initializes the embedding search system
func InitEmbeddingsSearch(db *sqlx.DB, httpClient *http.Client, cfg embeddings.EmbeddingSearchConfig, licenseChecker *enterprise.LicenseChecker) (embeddings.EmbeddingSearch, error) {
	if cfg.Type == "" {
		return nil, fmt.Errorf("search is disabled")
	}

	if !licenseChecker.IsBasicsLicensed() {
		return nil, fmt.Errorf("search is unavailable without a valid license")
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
