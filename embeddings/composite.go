// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/chunking"
)

// Per-request limits for embedding APIs. OpenAI and Azure OpenAI cap a single
// embeddings request at 2,048 inputs and 300,000 tokens summed across inputs;
// stay conservatively below both, estimating tokens at ~4 characters each.
const (
	maxEmbeddingRequestInputs = 1024
	maxEmbeddingRequestChars  = 1_000_000 // ~250k tokens
)

// CompositeSearch implements EmbeddingSearch using separate vector store and embedding provider
type CompositeSearch struct {
	store    VectorStore
	provider EmbeddingProvider
	options  chunking.Options
}

// NewCompositeSearch creates a new CompositeSearch with required chunking options
func NewCompositeSearch(store VectorStore, provider EmbeddingProvider, options chunking.Options) *CompositeSearch {
	return &CompositeSearch{
		store:    store,
		provider: provider,
		options:  options,
	}
}

// Store chunks documents, generates embeddings, and stores them
func (c *CompositeSearch) Store(ctx context.Context, docs []PostDocument) error {
	// Apply chunking to each document
	var chunkedDocs []PostDocument
	for _, doc := range docs {
		chunks := chunking.ChunkText(doc.Content, c.options)

		for _, chunk := range chunks {
			// Create a new document for each chunk
			chunkDoc := doc // Copy all metadata
			chunkDoc.Content = chunk.Content
			chunkDoc.ChunkInfo = chunk.ChunkInfo // Assign chunk metadata

			chunkedDocs = append(chunkedDocs, chunkDoc)
		}
	}

	// Early return if no documents after chunking (all filtered or empty input)
	if len(chunkedDocs) == 0 {
		return nil
	}

	// Extract texts for embedding
	texts := make([]string, len(chunkedDocs))
	for i, doc := range chunkedDocs {
		texts[i] = doc.Content
	}

	// Generate embeddings for all chunks, splitting into multiple requests
	// when the batch would exceed provider per-request limits
	embeddings := make([][]float32, 0, len(texts))
	for _, batch := range splitEmbeddingBatches(texts) {
		batchEmbeddings, err := c.provider.BatchCreateEmbeddings(ctx, batch)
		if err != nil {
			return err
		}
		embeddings = append(embeddings, batchEmbeddings...)
	}

	// Validate embedding count matches document count
	if len(embeddings) != len(chunkedDocs) {
		return fmt.Errorf("embedding count mismatch: got %d embeddings for %d documents", len(embeddings), len(chunkedDocs))
	}

	// Store the chunks and their embeddings
	return c.store.Store(ctx, chunkedDocs, embeddings)
}

// splitEmbeddingBatches partitions texts into consecutive sub-batches that
// each respect the per-request input-count and size limits. A single text
// larger than the size limit still gets its own batch.
func splitEmbeddingBatches(texts []string) [][]string {
	var batches [][]string
	start := 0
	chars := 0
	for i, text := range texts {
		if i > start && (i-start >= maxEmbeddingRequestInputs || chars+len(text) > maxEmbeddingRequestChars) {
			batches = append(batches, texts[start:i])
			start = i
			chars = 0
		}
		chars += len(text)
	}
	if start < len(texts) {
		batches = append(batches, texts[start:])
	}
	return batches
}

// Search performs a semantic search and merges results from chunks of the same document
func (c *CompositeSearch) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	// Generate embedding for the query
	embedding, err := c.provider.CreateEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}

	// Search for matching chunks
	results, err := c.store.Search(ctx, embedding, opts)
	if err != nil {
		return nil, err
	}

	return results, nil
}

// Delete removes documents and their chunks
func (c *CompositeSearch) Delete(ctx context.Context, postIDs []string) error {
	return c.store.Delete(ctx, postIDs)
}

// Clear removes all documents and chunks
func (c *CompositeSearch) Clear(ctx context.Context) error {
	return c.store.Clear(ctx)
}

// DeleteOrphaned removes embeddings whose posts no longer exist or are past retention.
func (c *CompositeSearch) DeleteOrphaned(ctx context.Context, nowTime, batchSize int64) (int64, error) {
	return c.store.DeleteOrphaned(ctx, nowTime, batchSize)
}
