// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// Per-request limits for embedding APIs, matching the documented OpenAI and
// Azure OpenAI caps of 2,048 inputs and 300,000 tokens summed across inputs.
// A BPE token is never shorter than one byte of input, so capping summed
// bytes at the token limit hard-guarantees the token cap for any content.
// (Per-input model context limits remain the caller's concern — chunking
// keeps individual texts small.)
const (
	maxEmbeddingRequestInputs = 2048
	maxEmbeddingRequestBytes  = 300_000
)

// EmbeddingProvider implements the embeddings.EmbeddingProvider interface using Bifrost.
type EmbeddingProvider struct {
	client     *bifrostcore.Bifrost
	provider   schemas.ModelProvider
	apiKey     string // used only to redact configured secrets from provider error surfaces
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
		Provider: cfg.Provider,
		APIKey:   cfg.APIKey,
		APIURL:   normalizeOpenAIBaseURL(cfg.Provider, cfg.APIURL),
	}

	client, err := newBifrostClient(account, cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Bifrost client for embeddings: %w", err)
	}

	return &EmbeddingProvider{
		client:     client,
		provider:   cfg.Provider,
		apiKey:     cfg.APIKey,
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
			Text: new(text),
		},
	}
	if p.dimensions > 0 {
		req.Params = &schemas.EmbeddingParameters{
			Dimensions: new(p.dimensions),
		}
	}

	resp, bifrostErr := p.client.EmbeddingRequest(bifrostCtx, req)
	if bifrostErr != nil {
		return nil, llm.SanitizeProviderError(fmt.Errorf("bifrost embedding error: %s", bifrostErrorString(bifrostErr)), p.apiKey)
	}

	if resp == nil || len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	// Extract embedding from response
	embResp := resp.Data[0].Embedding
	if len(embResp.EmbeddingArray) == 0 {
		return nil, fmt.Errorf("no embedding array in response")
	}

	return float64SliceToFloat32(embResp.EmbeddingArray), nil
}

// BatchCreateEmbeddings generates embeddings for any number of texts,
// transparently splitting into multiple requests to stay within the
// provider's per-request limits.
func (p *EmbeddingProvider) BatchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, 0, len(texts))
	for _, batch := range splitEmbeddingBatches(texts) {
		batchResult, err := p.batchCreateEmbeddings(ctx, batch)
		if err != nil {
			return nil, err
		}
		result = append(result, batchResult...)
	}
	return result, nil
}

// batchCreateEmbeddings issues a single embeddings request.
func (p *EmbeddingProvider) batchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
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
			Dimensions: new(p.dimensions),
		}
	}

	resp, bifrostErr := p.client.EmbeddingRequest(bifrostCtx, req)
	if bifrostErr != nil {
		return nil, llm.SanitizeProviderError(fmt.Errorf("bifrost batch embedding error: %s", bifrostErrorString(bifrostErr)), p.apiKey)
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
		result[i] = float64SliceToFloat32(data.Embedding.EmbeddingArray)
	}

	return result, nil
}

// splitEmbeddingBatches partitions texts into consecutive sub-batches that
// each respect the per-request input-count and size limits. A single text
// larger than the size limit still gets its own batch.
func splitEmbeddingBatches(texts []string) [][]string {
	var batches [][]string
	start := 0
	bytes := 0
	for i, text := range texts {
		if i > start && (i-start >= maxEmbeddingRequestInputs || bytes+len(text) > maxEmbeddingRequestBytes) {
			batches = append(batches, texts[start:i])
			start = i
			bytes = 0
		}
		bytes += len(text)
	}
	if start < len(texts) {
		batches = append(batches, texts[start:])
	}
	return batches
}

// float64SliceToFloat32 narrows an embedding array; bifrost v1.5+ returns
// float64 but our embeddings interface uses float32.
func float64SliceToFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
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
