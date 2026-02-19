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

// EmbeddingProvider implements the embeddings.EmbeddingProvider interface using Bifrost.
type EmbeddingProvider struct {
	client     *bifrostcore.Bifrost
	provider   schemas.ModelProvider
	model      string
	dimensions int
}

// EmbeddingConfig holds the configuration for creating a EmbeddingProvider.
type EmbeddingConfig struct {
	Provider   schemas.ModelProvider
	APIKey     string
	APIURL     string
	Model      string
	Dimensions int
}

// NewEmbeddingProvider creates a new EmbeddingProvider.
func NewEmbeddingProvider(cfg EmbeddingConfig) (*EmbeddingProvider, error) {
	account := &providerAccount{
		provider: cfg.Provider,
		apiKey:   cfg.APIKey,
		apiURL:   normalizeOpenAIBaseURL(cfg.Provider, cfg.APIURL),
	}

	bifrostConfig := schemas.BifrostConfig{
		Account: account,
	}

	client, err := bifrostcore.Init(context.Background(), bifrostConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Bifrost client for embeddings: %w", err)
	}

	return &EmbeddingProvider{
		client:     client,
		provider:   cfg.Provider,
		model:      cfg.Model,
		dimensions: cfg.Dimensions,
	}, nil
}

// CreateEmbedding generates an embedding for the given text.
func (p *EmbeddingProvider) CreateEmbedding(ctx context.Context, text string) ([]float32, error) {
	bifrostCtx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)

	req := &schemas.BifrostEmbeddingRequest{
		Provider: p.provider,
		Model:    p.model,
		Input: &schemas.EmbeddingInput{
			Text: Ptr(text),
		},
	}
	if p.dimensions > 0 {
		req.Params = &schemas.EmbeddingParameters{
			Dimensions: Ptr(p.dimensions),
		}
	}

	resp, bifrostErr := p.client.EmbeddingRequest(bifrostCtx, req)
	if bifrostErr != nil {
		return nil, fmt.Errorf("bifrost embedding error: %s", bifrostErr.Error.Message)
	}

	if resp == nil || len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	// Extract embedding from response
	embResp := resp.Data[0].Embedding
	if len(embResp.EmbeddingArray) == 0 {
		return nil, fmt.Errorf("no embedding array in response")
	}

	return embResp.EmbeddingArray, nil
}

// BatchCreateEmbeddings generates embeddings for multiple texts.
func (p *EmbeddingProvider) BatchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	bifrostCtx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)

	req := &schemas.BifrostEmbeddingRequest{
		Provider: p.provider,
		Model:    p.model,
		Input: &schemas.EmbeddingInput{
			Texts: texts,
		},
	}
	if p.dimensions > 0 {
		req.Params = &schemas.EmbeddingParameters{
			Dimensions: Ptr(p.dimensions),
		}
	}

	resp, bifrostErr := p.client.EmbeddingRequest(bifrostCtx, req)
	if bifrostErr != nil {
		return nil, fmt.Errorf("bifrost batch embedding error: %s", bifrostErr.Error.Message)
	}

	if resp == nil || len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	// Extract embeddings from response
	result := make([][]float32, len(resp.Data))
	for i, data := range resp.Data {
		if len(data.Embedding.EmbeddingArray) == 0 {
			return nil, fmt.Errorf("no embedding array in response for index %d", i)
		}
		result[i] = data.Embedding.EmbeddingArray
	}

	return result, nil
}

// Dimensions returns the dimensionality of the embeddings.
func (p *EmbeddingProvider) Dimensions() int {
	return p.dimensions
}

// Shutdown gracefully shuts down the Bifrost client.
func (p *EmbeddingProvider) Shutdown() {
	if p.client != nil {
		p.client.Shutdown()
	}
}

// Ensure EmbeddingProvider implements the interface.
var _ embeddings.EmbeddingProvider = (*EmbeddingProvider)(nil)
