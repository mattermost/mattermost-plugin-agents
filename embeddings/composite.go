// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"context"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/chunking"
)

// CompositeSearch implements EmbeddingSearch using separate vector store and embedding provider
type CompositeSearch struct {
	store    VectorStore
	provider EmbeddingProvider
	options  chunking.Options
	recency  RecencyBiasSettings
}

// NewCompositeSearch creates a new CompositeSearch with required chunking options
func NewCompositeSearch(store VectorStore, provider EmbeddingProvider, options chunking.Options, recency RecencyBiasSettings) *CompositeSearch {
	return &CompositeSearch{
		store:    store,
		provider: provider,
		options:  options,
		recency:  recency,
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

	// Generate embeddings for all chunks
	embeddings, err := c.provider.BatchCreateEmbeddings(ctx, texts)
	if err != nil {
		return err
	}

	// Validate embedding count matches document count
	if len(embeddings) != len(chunkedDocs) {
		return fmt.Errorf("embedding count mismatch: got %d embeddings for %d documents", len(embeddings), len(chunkedDocs))
	}

	// Store the chunks and their embeddings
	return c.store.Store(ctx, chunkedDocs, embeddings)
}

// Search performs a semantic search and merges results from chunks of the same document
func (c *CompositeSearch) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	// Generate embedding for the query
	embedding, err := c.provider.CreateEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}

	if !c.recency.Enabled {
		return c.store.Search(ctx, embedding, opts)
	}

	// Recency bias: over-fetch candidates by pure similarity (keeps the ANN
	// index usable), rerank in memory with the time decay, then apply the
	// caller's offset/limit window to the reranked order.
	fetchOpts := opts
	fetchOpts.Offset = 0
	fetchOpts.Limit = recencyFetchLimit(opts.Limit, opts.Offset)

	// The store caps a single search at MaxSearchResults rows, so a window
	// that extends past the cap cannot be served from the reranked candidate
	// pool. Fall back to store-side pagination in raw similarity order rather
	// than silently truncating deep pages.
	if fetchOpts.Limit > MaxSearchResults || (fetchOpts.Limit == 0 && opts.Offset > 0) {
		return c.store.Search(ctx, embedding, opts)
	}

	results, err := c.store.Search(ctx, embedding, fetchOpts)
	if err != nil {
		return nil, err
	}

	rerankByRecency(results, time.Now().UnixMilli(), c.recency)

	if opts.Offset > 0 {
		if opts.Offset >= len(results) {
			return nil, nil
		}
		results = results[opts.Offset:]
	}
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
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

// BulkIndexer returns the store's bulk control, or nil.
func (c *CompositeSearch) BulkIndexer() BulkIndexer {
	if bulk, ok := c.store.(BulkIndexer); ok {
		return bulk
	}
	return nil
}
