// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"context"
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-ai/chunking"
)

// Provider types
const (
	ProviderTypeOpenAI           = "openai"
	ProviderTypeOpenAICompatible = "openai-compatible"
	ProviderTypeBifrost          = "bifrost"
	ProviderTypeMock             = "mock"
)

// Vector store types
const (
	VectorStoreTypePGVector = "pgvector"
)

// Search types
const (
	SearchTypeComposite = "composite"
)

// PostDocument represents a Mattermost post with its metadata
type PostDocument struct {
	PostID    string // ID of the Mattermost post
	CreateAt  int64  // Creation timestamp of the referenced post, not when this was indexed
	TeamID    string
	ChannelID string
	UserID    string
	Content   string

	// Embed chunk info to track if this is a chunk
	chunking.ChunkInfo
}

// FileDocument represents a document extracted from a file attachment
type FileDocument struct {
	FileID    string // Mattermost file attachment ID
	PostID    string // Post the file was attached to
	FileName  string // Original filename
	FileType  string // "pdf", "docx", "xlsx"
	CreateAt  int64  // Upload timestamp
	TeamID    string
	ChannelID string
	UserID    string
	Content   string // Extracted text content
	PageNum   int    // Page/sheet number (0 for whole-doc chunks)

	// Embed chunk info to track if this is a chunk
	chunking.ChunkInfo
}

// Source types for search results
const (
	SourceTypePost = "post"
	SourceTypeFile = "file"
)

// SearchResult represents a single search result with its similarity score
type SearchResult struct {
	Document     PostDocument  // Post result (populated when SourceType == "post")
	FileDocument *FileDocument // File result (non-nil when SourceType == "file")
	Score        float32
	SourceType   string // "post" or "file"
}

// SearchOptions contains parameters for search operations
type SearchOptions struct {
	Limit         int
	Offset        int
	MinScore      float32
	TeamID        string
	ChannelID     string
	UserID        string // User ID for permission checks
	CreatedAfter  int64
	CreatedBefore int64
}

// EmbeddingSearch defines the high-level interface for storing and searching using embeddings
type EmbeddingSearch interface {
	// Store stores documents and handles embedding generation internally
	Store(ctx context.Context, docs []PostDocument) error

	// Search performs a similarity search using the query text (posts only)
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)

	// Delete removes documents
	Delete(ctx context.Context, postIDs []string) error

	// Clear removes all documents
	Clear(ctx context.Context) error

	// DeleteOrphaned removes embeddings whose posts no longer exist or are past retention.
	// nowTime is the retention cutoff (Unix millis), batchSize limits rows deleted per call.
	// Returns the number of rows deleted.
	DeleteOrphaned(ctx context.Context, nowTime, batchSize int64) (int64, error)

	// StoreFiles stores file documents and handles embedding generation internally
	StoreFiles(ctx context.Context, docs []FileDocument) error

	// DeleteFiles removes file documents by file IDs
	DeleteFiles(ctx context.Context, fileIDs []string) error

	// ClearFiles removes all file documents
	ClearFiles(ctx context.Context) error

	// SearchAll performs a similarity search across both posts and files
	SearchAll(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
}

// VectorStore defines the interface for vector storage and search operations
type VectorStore interface {
	// Store stores documents and their embeddings
	Store(ctx context.Context, docs []PostDocument, embeddings [][]float32) error

	// Search performs a similarity search using the provided embedding (posts only)
	Search(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error)

	// Delete removes documents from the vector store
	Delete(ctx context.Context, postIDs []string) error

	// Clear removes all documents from the vector store
	Clear(ctx context.Context) error

	// DeleteOrphaned removes embeddings whose posts no longer exist or are past retention.
	// nowTime is the retention cutoff (Unix millis), batchSize limits rows deleted per call.
	// Returns the number of rows deleted.
	DeleteOrphaned(ctx context.Context, nowTime, batchSize int64) (int64, error)

	// StoreFileDocuments stores file documents and their embeddings
	StoreFileDocuments(ctx context.Context, docs []FileDocument, embeddings [][]float32) error

	// DeleteFileDocuments removes file documents by file IDs
	DeleteFileDocuments(ctx context.Context, fileIDs []string) error

	// ClearFileDocuments removes all file documents
	ClearFileDocuments(ctx context.Context) error

	// SearchAll performs a similarity search across both posts and files
	SearchAll(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error)
}

// EmbeddingProvider defines the interface for embedding generation
type EmbeddingProvider interface {
	// CreateEmbedding generates embedding for the given text
	CreateEmbedding(ctx context.Context, text string) ([]float32, error)

	// BatchCreateEmbeddings generates embeddings for multiple texts
	BatchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the dimensionality of the embeddings
	Dimensions() int
}

// UpstreamConfig holds configuration for the upstream service
type UpstreamConfig struct {
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters"`
}

// EmbeddingSearchConfig holds configuration for the embedding search service
type EmbeddingSearchConfig struct {
	Type                   string           `json:"type"`
	VectorStore            UpstreamConfig   `json:"vectorStore"`
	EmbeddingProvider      UpstreamConfig   `json:"embeddingProvider"`
	Parameters             json.RawMessage  `json:"parameters"`
	Dimensions             int              `json:"dimensions"`
	ChunkingOptions        chunking.Options `json:"chunkingOptions"`
	EnableDocumentIndexing bool             `json:"enableDocumentIndexing"`
	MaxFileSizeMB          int              `json:"maxFileSizeMB"`
}

// GetProviderType returns the embedding provider type
func (c *EmbeddingSearchConfig) GetProviderType() string {
	return c.EmbeddingProvider.Type
}

// GetModelName extracts the model name from the embedding provider parameters
func (c *EmbeddingSearchConfig) GetModelName() string {
	if c.EmbeddingProvider.Parameters == nil {
		return ""
	}

	var params struct {
		EmbeddingModel string `json:"embeddingModel"`
	}
	if err := json.Unmarshal(c.EmbeddingProvider.Parameters, &params); err != nil {
		return ""
	}
	return params.EmbeddingModel
}
