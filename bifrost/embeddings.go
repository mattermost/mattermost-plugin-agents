// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-ai/embeddings"
)

// BifrostEmbeddingProvider implements the embeddings.EmbeddingProvider interface using Bifrost.
type BifrostEmbeddingProvider struct {
	client     *bifrostcore.Bifrost
	provider   schemas.ModelProvider
	model      string
	dimensions int
}

// EmbeddingConfig holds the configuration for creating a BifrostEmbeddingProvider.
type EmbeddingConfig struct {
	Provider   schemas.ModelProvider
	APIKey     string
	APIURL     string
	Model      string
	Dimensions int
}

// NewEmbeddingProvider creates a new BifrostEmbeddingProvider.
func NewEmbeddingProvider(cfg EmbeddingConfig) (*BifrostEmbeddingProvider, error) {
	account := &providerAccount{
		provider: cfg.Provider,
		apiKey:   cfg.APIKey,
		apiURL:   cfg.APIURL,
	}

	bifrostConfig := schemas.BifrostConfig{
		Account: account,
	}

	client, err := bifrostcore.Init(context.Background(), bifrostConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Bifrost client for embeddings: %w", err)
	}

	return &BifrostEmbeddingProvider{
		client:     client,
		provider:   cfg.Provider,
		model:      cfg.Model,
		dimensions: cfg.Dimensions,
	}, nil
}

// CreateEmbedding generates an embedding for the given text.
func (p *BifrostEmbeddingProvider) CreateEmbedding(ctx context.Context, text string) ([]float32, error) {
	req := &schemas.BifrostRequest{
		Provider: p.provider,
		Model:    p.model,
		Input: schemas.RequestInput{
			EmbeddingInput: &schemas.EmbeddingInput{
				Text: Ptr(text),
			},
		},
	}

	resp, bifrostErr := p.client.EmbeddingRequest(ctx, req)
	if bifrostErr != nil {
		return nil, fmt.Errorf("bifrost embedding error: %s", bifrostErr.Error.Message)
	}

	if resp == nil || len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	// Extract embedding from response
	embResp := resp.Data[0].Embedding
	if embResp.EmbeddingArray == nil {
		return nil, fmt.Errorf("no embedding array in response")
	}

	return *embResp.EmbeddingArray, nil
}

// BatchCreateEmbeddings generates embeddings for multiple texts.
func (p *BifrostEmbeddingProvider) BatchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	req := &schemas.BifrostRequest{
		Provider: p.provider,
		Model:    p.model,
		Input: schemas.RequestInput{
			EmbeddingInput: &schemas.EmbeddingInput{
				Texts: texts,
			},
		},
	}

	resp, bifrostErr := p.client.EmbeddingRequest(ctx, req)
	if bifrostErr != nil {
		return nil, fmt.Errorf("bifrost batch embedding error: %s", bifrostErr.Error.Message)
	}

	if resp == nil || len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	// Extract embeddings from response
	result := make([][]float32, len(resp.Data))
	for i, data := range resp.Data {
		if data.Embedding.EmbeddingArray == nil {
			return nil, fmt.Errorf("no embedding array in response for index %d", i)
		}
		result[i] = *data.Embedding.EmbeddingArray
	}

	return result, nil
}

// Dimensions returns the dimensionality of the embeddings.
func (p *BifrostEmbeddingProvider) Dimensions() int {
	return p.dimensions
}

// Shutdown gracefully shuts down the Bifrost client.
func (p *BifrostEmbeddingProvider) Shutdown() {
	if p.client != nil {
		p.client.Shutdown()
	}
}

// Ensure BifrostEmbeddingProvider implements the interface.
var _ embeddings.EmbeddingProvider = (*BifrostEmbeddingProvider)(nil)
