// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/chunking"
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

// StoreFiles chunks file documents, generates embeddings, and stores them
func (c *CompositeSearch) StoreFiles(ctx context.Context, docs []FileDocument) error {
	// Apply chunking to each file document
	var chunkedDocs []FileDocument
	for _, doc := range docs {
		chunks := chunking.ChunkText(doc.Content, c.options)

		for _, chunk := range chunks {
			chunkDoc := doc // Copy all metadata
			chunkDoc.Content = chunk.Content
			chunkDoc.ChunkInfo = chunk.ChunkInfo

			chunkedDocs = append(chunkedDocs, chunkDoc)
		}
	}

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

	if len(embeddings) != len(chunkedDocs) {
		return fmt.Errorf("embedding count mismatch: got %d embeddings for %d file documents", len(embeddings), len(chunkedDocs))
	}

	return c.store.StoreFileDocuments(ctx, chunkedDocs, embeddings)
}

// DeleteFiles removes file documents by file IDs
func (c *CompositeSearch) DeleteFiles(ctx context.Context, fileIDs []string) error {
	return c.store.DeleteFileDocuments(ctx, fileIDs)
}

// ClearFiles removes all file documents
func (c *CompositeSearch) ClearFiles(ctx context.Context) error {
	return c.store.ClearFileDocuments(ctx)
}

// SearchAll performs a semantic search across both posts and files
func (c *CompositeSearch) SearchAll(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	embedding, err := c.provider.CreateEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}

	results, err := c.store.SearchAll(ctx, embedding, opts)
	if err != nil {
		return nil, err
	}

	return results, nil
}
