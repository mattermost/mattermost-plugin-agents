// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"context"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/chunking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test doubles for VectorStore and EmbeddingProvider interfaces

// stubVectorStore is a simple test double for VectorStore
type stubVectorStore struct {
	storeFunc  func(ctx context.Context, docs []PostDocument, embeddings [][]float32) error
	searchFunc func(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error)
	deleteFunc func(ctx context.Context, postIDs []string) error
	clearFunc  func(ctx context.Context) error

	// Record calls for verification
	storeCalls  []storeCall
	searchCalls []searchCall
	deleteCalls []deleteCall
	clearCalls  int
}

type storeCall struct {
	docs       []PostDocument
	embeddings [][]float32
}

type searchCall struct {
	embedding []float32
	opts      SearchOptions
}

type deleteCall struct {
	postIDs []string
}

func (s *stubVectorStore) Store(ctx context.Context, docs []PostDocument, embeddings [][]float32) error {
	s.storeCalls = append(s.storeCalls, storeCall{docs: docs, embeddings: embeddings})
	if s.storeFunc != nil {
		return s.storeFunc(ctx, docs, embeddings)
	}
	return nil
}

func (s *stubVectorStore) Search(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error) {
	s.searchCalls = append(s.searchCalls, searchCall{embedding: embedding, opts: opts})
	if s.searchFunc != nil {
		return s.searchFunc(ctx, embedding, opts)
	}
	return nil, nil
}

func (s *stubVectorStore) Delete(ctx context.Context, postIDs []string) error {
	s.deleteCalls = append(s.deleteCalls, deleteCall{postIDs: postIDs})
	if s.deleteFunc != nil {
		return s.deleteFunc(ctx, postIDs)
	}
	return nil
}

func (s *stubVectorStore) Clear(ctx context.Context) error {
	s.clearCalls++
	if s.clearFunc != nil {
		return s.clearFunc(ctx)
	}
	return nil
}

func (s *stubVectorStore) DeleteOrphaned(ctx context.Context, nowTime, batchSize int64) (int64, error) {
	return 0, nil
}

// stubEmbeddingProvider is a simple test double for EmbeddingProvider
type stubEmbeddingProvider struct {
	createEmbeddingFunc       func(ctx context.Context, text string) ([]float32, error)
	batchCreateEmbeddingsFunc func(ctx context.Context, texts []string) ([][]float32, error)
	dimensions                int

	// Record calls for verification
	createEmbeddingCalls       []string
	batchCreateEmbeddingsCalls [][]string
}

func (p *stubEmbeddingProvider) CreateEmbedding(ctx context.Context, text string) ([]float32, error) {
	p.createEmbeddingCalls = append(p.createEmbeddingCalls, text)
	if p.createEmbeddingFunc != nil {
		return p.createEmbeddingFunc(ctx, text)
	}
	return []float32{0.1, 0.2, 0.3}, nil
}

func (p *stubEmbeddingProvider) BatchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	p.batchCreateEmbeddingsCalls = append(p.batchCreateEmbeddingsCalls, texts)
	if p.batchCreateEmbeddingsFunc != nil {
		return p.batchCreateEmbeddingsFunc(ctx, texts)
	}
	embeddings := make([][]float32, len(texts))
	for i := range texts {
		embeddings[i] = []float32{0.1, 0.2, 0.3}
	}
	return embeddings, nil
}

func (p *stubEmbeddingProvider) Dimensions() int {
	if p.dimensions > 0 {
		return p.dimensions
	}
	return 3
}

func TestCompositeSearch_Store(t *testing.T) {
	defaultOptions := chunking.Options{
		ChunkSize:        1000,
		ChunkOverlap:     200,
		ChunkingStrategy: "sentences",
	}

	tests := []struct {
		name           string
		docs           []PostDocument
		options        chunking.Options
		storeFunc      func(ctx context.Context, docs []PostDocument, embeddings [][]float32) error
		batchEmbedFunc func(ctx context.Context, texts []string) ([][]float32, error)
		wantErr        bool
		errContains    string
		verify         func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider)
	}{
		{
			name:    "empty docs input returns nil without error",
			docs:    []PostDocument{},
			options: defaultOptions,
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, store.storeCalls, 0, "store should not be called for empty input")
				assert.Len(t, provider.batchCreateEmbeddingsCalls, 0, "embedding provider should not be called for empty input")
			},
		},
		{
			name:    "nil docs input returns nil without error",
			docs:    nil,
			options: defaultOptions,
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, store.storeCalls, 0, "store should not be called for nil input")
				assert.Len(t, provider.batchCreateEmbeddingsCalls, 0, "embedding provider should not be called for nil input")
			},
		},
		{
			name: "embedding provider failure during BatchCreateEmbeddings",
			docs: []PostDocument{
				{PostID: "post1", Content: "test content"},
			},
			options: defaultOptions,
			batchEmbedFunc: func(ctx context.Context, texts []string) ([][]float32, error) {
				return nil, errors.New("embedding service unavailable")
			},
			wantErr:     true,
			errContains: "embedding service unavailable",
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, store.storeCalls, 0, "store should not be called when embedding fails")
				assert.Len(t, provider.batchCreateEmbeddingsCalls, 1, "embedding provider should be called once")
			},
		},
		{
			name: "vector store failure during Store",
			docs: []PostDocument{
				{PostID: "post1", Content: "test content"},
			},
			options: defaultOptions,
			storeFunc: func(ctx context.Context, docs []PostDocument, embeddings [][]float32) error {
				return errors.New("database connection failed")
			},
			wantErr:     true,
			errContains: "database connection failed",
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, store.storeCalls, 1, "store should be called once")
				assert.Len(t, provider.batchCreateEmbeddingsCalls, 1, "embedding provider should be called once")
			},
		},
		{
			name: "partial failures in batch embedding generation - provider returns fewer embeddings than texts",
			docs: []PostDocument{
				{PostID: "post1", Content: "content one"},
				{PostID: "post2", Content: "content two"},
				{PostID: "post3", Content: "content three"},
			},
			options: defaultOptions,
			batchEmbedFunc: func(ctx context.Context, texts []string) ([][]float32, error) {
				// Return only 2 embeddings for 3 texts - simulating partial failure
				return [][]float32{
					{0.1, 0.2},
					{0.3, 0.4},
				}, nil
			},
			wantErr:     true, // The code now validates embedding count matches doc count
			errContains: "embedding count mismatch",
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				// Store should not be called when embedding count doesn't match doc count
				assert.Len(t, store.storeCalls, 0, "store should not be called when embedding count mismatches")
				assert.Len(t, provider.batchCreateEmbeddingsCalls, 1, "embedding provider should be called once")
			},
		},
		{
			name: "documents that chunk into zero content - whitespace only content",
			docs: []PostDocument{
				{PostID: "post1", Content: "   "},
			},
			options: defaultOptions,
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				// Chunker returns a single chunk for whitespace-only content
				// So embeddings will be generated and stored
				assert.Len(t, provider.batchCreateEmbeddingsCalls, 1, "embedding should be generated")
				assert.Len(t, store.storeCalls, 1, "store should be called")
			},
		},
		{
			name: "documents with empty string content",
			docs: []PostDocument{
				{PostID: "post1", Content: ""},
			},
			options: defaultOptions,
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				// Chunker returns a single chunk for empty content
				assert.Len(t, provider.batchCreateEmbeddingsCalls, 1, "embedding should be generated for empty content")
				assert.Len(t, store.storeCalls, 1, "store should be called")
			},
		},
		{
			name: "successful store with multiple documents",
			docs: []PostDocument{
				{PostID: "post1", Content: "first document content", TeamID: "team1", ChannelID: "channel1"},
				{PostID: "post2", Content: "second document content", TeamID: "team1", ChannelID: "channel2"},
			},
			options: defaultOptions,
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, store.storeCalls, 1, "store should be called once")
				assert.Len(t, provider.batchCreateEmbeddingsCalls, 1, "embedding provider should be called once")
				if len(store.storeCalls) > 0 {
					assert.Len(t, store.storeCalls[0].docs, 2, "should have 2 docs")
					assert.Len(t, store.storeCalls[0].embeddings, 2, "should have 2 embeddings")
				}
			},
		},
		{
			name: "chunking splits large document into multiple chunks",
			docs: []PostDocument{
				{PostID: "post1", Content: "This is sentence one. This is sentence two. This is sentence three. This is sentence four. This is sentence five."},
			},
			options: chunking.Options{
				ChunkSize:        50,
				ChunkOverlap:     10,
				ChunkingStrategy: "sentences",
			},
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, store.storeCalls, 1, "store should be called once")
				if len(store.storeCalls) > 0 {
					// Document should be chunked into multiple pieces
					assert.Greater(t, len(store.storeCalls[0].docs), 1, "document should be chunked into multiple pieces")
					// All chunks should have the same PostID
					for _, doc := range store.storeCalls[0].docs {
						assert.Equal(t, "post1", doc.PostID, "all chunks should preserve PostID")
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubVectorStore{storeFunc: tt.storeFunc}
			provider := &stubEmbeddingProvider{batchCreateEmbeddingsFunc: tt.batchEmbedFunc}

			cs := NewCompositeSearch(store, provider, tt.options)

			err := cs.Store(context.Background(), tt.docs)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, store, provider)
			}
		})
	}
}

func TestCompositeSearch_Search(t *testing.T) {
	defaultOptions := chunking.Options{
		ChunkSize:        1000,
		ChunkOverlap:     200,
		ChunkingStrategy: "sentences",
	}

	tests := []struct {
		name            string
		query           string
		searchOpts      SearchOptions
		createEmbedFunc func(ctx context.Context, text string) ([]float32, error)
		searchFunc      func(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error)
		wantErr         bool
		errContains     string
		expectedResults []SearchResult
		verify          func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider)
	}{
		{
			name:  "embedding provider failure during CreateEmbedding",
			query: "search query",
			createEmbedFunc: func(ctx context.Context, text string) ([]float32, error) {
				return nil, errors.New("embedding API rate limited")
			},
			wantErr:     true,
			errContains: "embedding API rate limited",
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, provider.createEmbeddingCalls, 1, "embedding should be attempted")
				assert.Len(t, store.searchCalls, 0, "search should not be called when embedding fails")
			},
		},
		{
			name:  "vector store failure during Search",
			query: "search query",
			searchFunc: func(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error) {
				return nil, errors.New("vector store search timeout")
			},
			wantErr:     true,
			errContains: "vector store search timeout",
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, provider.createEmbeddingCalls, 1, "embedding should be created")
				assert.Len(t, store.searchCalls, 1, "search should be attempted")
			},
		},
		{
			name:  "context cancellation mid-operation - during embedding",
			query: "search query",
			createEmbedFunc: func(ctx context.Context, text string) ([]float32, error) {
				return nil, context.Canceled
			},
			wantErr:     true,
			errContains: "context canceled",
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, store.searchCalls, 0, "search should not be called after context cancellation")
			},
		},
		{
			name:  "context cancellation mid-operation - during search",
			query: "search query",
			searchFunc: func(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error) {
				return nil, context.Canceled
			},
			wantErr:     true,
			errContains: "context canceled",
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, provider.createEmbeddingCalls, 1, "embedding should be created before cancellation")
			},
		},
		{
			name:       "successful search with results",
			query:      "find documents about testing",
			searchOpts: SearchOptions{Limit: 10, MinScore: 0.5},
			searchFunc: func(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error) {
				return []SearchResult{
					{Document: PostDocument{PostID: "post1", Content: "testing content"}, Score: 0.9},
					{Document: PostDocument{PostID: "post2", Content: "more testing"}, Score: 0.7},
				}, nil
			},
			expectedResults: []SearchResult{
				{Document: PostDocument{PostID: "post1", Content: "testing content"}, Score: 0.9},
				{Document: PostDocument{PostID: "post2", Content: "more testing"}, Score: 0.7},
			},
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				assert.Len(t, provider.createEmbeddingCalls, 1)
				assert.Equal(t, "find documents about testing", provider.createEmbeddingCalls[0])
				assert.Len(t, store.searchCalls, 1)
				assert.Equal(t, 10, store.searchCalls[0].opts.Limit)
				assert.Equal(t, float32(0.5), store.searchCalls[0].opts.MinScore)
			},
		},
		{
			name:       "search with empty query",
			query:      "",
			searchOpts: SearchOptions{Limit: 5},
			verify: func(t *testing.T, store *stubVectorStore, provider *stubEmbeddingProvider) {
				// Empty query is still processed - embedding is generated for empty string
				assert.Len(t, provider.createEmbeddingCalls, 1)
				assert.Equal(t, "", provider.createEmbeddingCalls[0])
			},
		},
		{
			name:  "search returns empty results",
			query: "no matching content",
			searchFunc: func(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error) {
				return []SearchResult{}, nil
			},
			expectedResults: []SearchResult{},
		},
		{
			name:  "search returns nil results",
			query: "query",
			searchFunc: func(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error) {
				return nil, nil
			},
			expectedResults: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubVectorStore{searchFunc: tt.searchFunc}
			provider := &stubEmbeddingProvider{createEmbeddingFunc: tt.createEmbedFunc}

			cs := NewCompositeSearch(store, provider, defaultOptions)

			results, err := cs.Search(context.Background(), tt.query, tt.searchOpts)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResults, results)
			}

			if tt.verify != nil {
				tt.verify(t, store, provider)
			}
		})
	}
}

func TestCompositeSearch_Delete(t *testing.T) {
	defaultOptions := chunking.Options{
		ChunkSize:        1000,
		ChunkOverlap:     200,
		ChunkingStrategy: "sentences",
	}

	tests := []struct {
		name        string
		postIDs     []string
		deleteFunc  func(ctx context.Context, postIDs []string) error
		wantErr     bool
		errContains string
		verify      func(t *testing.T, store *stubVectorStore)
	}{
		{
			name:    "vector store failure during Delete",
			postIDs: []string{"post1", "post2"},
			deleteFunc: func(ctx context.Context, postIDs []string) error {
				return errors.New("database delete operation failed")
			},
			wantErr:     true,
			errContains: "database delete operation failed",
			verify: func(t *testing.T, store *stubVectorStore) {
				assert.Len(t, store.deleteCalls, 1, "delete should be attempted")
				assert.Equal(t, []string{"post1", "post2"}, store.deleteCalls[0].postIDs)
			},
		},
		{
			name:    "successful delete with multiple IDs",
			postIDs: []string{"post1", "post2", "post3"},
			verify: func(t *testing.T, store *stubVectorStore) {
				assert.Len(t, store.deleteCalls, 1)
				assert.Equal(t, []string{"post1", "post2", "post3"}, store.deleteCalls[0].postIDs)
			},
		},
		{
			name:    "delete with empty postIDs",
			postIDs: []string{},
			verify: func(t *testing.T, store *stubVectorStore) {
				assert.Len(t, store.deleteCalls, 1)
				assert.Empty(t, store.deleteCalls[0].postIDs)
			},
		},
		{
			name:    "delete with nil postIDs",
			postIDs: nil,
			verify: func(t *testing.T, store *stubVectorStore) {
				assert.Len(t, store.deleteCalls, 1)
				assert.Nil(t, store.deleteCalls[0].postIDs)
			},
		},
		{
			name:    "context error propagated from store",
			postIDs: []string{"post1"},
			deleteFunc: func(ctx context.Context, postIDs []string) error {
				return context.DeadlineExceeded
			},
			wantErr:     true,
			errContains: "context deadline exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubVectorStore{deleteFunc: tt.deleteFunc}
			provider := &stubEmbeddingProvider{}

			cs := NewCompositeSearch(store, provider, defaultOptions)

			err := cs.Delete(context.Background(), tt.postIDs)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, store)
			}
		})
	}
}

func TestCompositeSearch_Clear(t *testing.T) {
	defaultOptions := chunking.Options{
		ChunkSize:        1000,
		ChunkOverlap:     200,
		ChunkingStrategy: "sentences",
	}

	tests := []struct {
		name        string
		clearFunc   func(ctx context.Context) error
		wantErr     bool
		errContains string
		verify      func(t *testing.T, store *stubVectorStore)
	}{
		{
			name: "vector store failure during Clear",
			clearFunc: func(ctx context.Context) error {
				return errors.New("failed to clear vector store")
			},
			wantErr:     true,
			errContains: "failed to clear vector store",
			verify: func(t *testing.T, store *stubVectorStore) {
				assert.Equal(t, 1, store.clearCalls, "clear should be attempted")
			},
		},
		{
			name: "successful clear",
			verify: func(t *testing.T, store *stubVectorStore) {
				assert.Equal(t, 1, store.clearCalls)
			},
		},
		{
			name: "context error propagated from store",
			clearFunc: func(ctx context.Context) error {
				return context.Canceled
			},
			wantErr:     true,
			errContains: "context canceled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubVectorStore{clearFunc: tt.clearFunc}
			provider := &stubEmbeddingProvider{}

			cs := NewCompositeSearch(store, provider, defaultOptions)

			err := cs.Clear(context.Background())

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, store)
			}
		})
	}
}

func TestCompositeSearch_ContextCancellation(t *testing.T) {
	defaultOptions := chunking.Options{
		ChunkSize:        1000,
		ChunkOverlap:     200,
		ChunkingStrategy: "sentences",
	}

	t.Run("Store with pre-canceled context", func(t *testing.T) {
		store := &stubVectorStore{}
		provider := &stubEmbeddingProvider{
			batchCreateEmbeddingsFunc: func(ctx context.Context, texts []string) ([][]float32, error) {
				// Check if context is already canceled
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
					return [][]float32{{0.1, 0.2}}, nil
				}
			},
		}

		cs := NewCompositeSearch(store, provider, defaultOptions)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		docs := []PostDocument{{PostID: "post1", Content: "test content"}}
		err := cs.Store(ctx, docs)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	})

	t.Run("Search with pre-canceled context", func(t *testing.T) {
		store := &stubVectorStore{}
		provider := &stubEmbeddingProvider{
			createEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
					return []float32{0.1, 0.2}, nil
				}
			},
		}

		cs := NewCompositeSearch(store, provider, defaultOptions)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := cs.Search(ctx, "query", SearchOptions{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	})
}
