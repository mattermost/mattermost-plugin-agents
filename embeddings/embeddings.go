// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"context"
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/v2/chunking"
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

// SearchResult represents a single search result with its similarity score
type SearchResult struct {
	Document PostDocument
	Score    float32
}

// MaxSearchResults is the hard cap on rows a vector store returns for a
// single search; stores clamp unset (<=0) or larger limits to this value.
const MaxSearchResults = 1000

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

	// Search performs a similarity search using the query text
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)

	// Delete removes documents
	Delete(ctx context.Context, postIDs []string) error

	// Clear removes all documents
	Clear(ctx context.Context) error

	// DeleteOrphaned removes embeddings whose posts no longer exist or are past retention.
	// nowTime is the retention cutoff (Unix millis), batchSize limits rows deleted per call.
	// Returns the number of rows deleted.
	DeleteOrphaned(ctx context.Context, nowTime, batchSize int64) (int64, error)
}

// VectorStore defines the interface for vector storage and search operations
type VectorStore interface {
	// Store stores documents and their embeddings
	Store(ctx context.Context, docs []PostDocument, embeddings [][]float32) error

	// Search performs a similarity search using the provided embedding
	Search(ctx context.Context, embedding []float32, opts SearchOptions) ([]SearchResult, error)

	// Delete removes documents from the vector store
	Delete(ctx context.Context, postIDs []string) error

	// Clear removes all documents from the vector store
	Clear(ctx context.Context) error

	// DeleteOrphaned removes embeddings whose posts no longer exist or are past retention.
	// nowTime is the retention cutoff (Unix millis), batchSize limits rows deleted per call.
	// Returns the number of rows deleted.
	DeleteOrphaned(ctx context.Context, nowTime, batchSize int64) (int64, error)
}

// SchemaChecker is an optional VectorStore preflight. CompositeSearch calls it
// before generating embeddings so a column-type mismatch does not bill the
// provider. Clear must still succeed so Full Reindex can repair the schema.
type SchemaChecker interface {
	CheckSchema(ctx context.Context) error
}

// BulkIndexer drops/rebuilds the ANN index around bulk loads.
type BulkIndexer interface {
	PrepareBulkIndex(ctx context.Context) error
	FinalizeBulkIndex(ctx context.Context) error
	VectorIndexExists(ctx context.Context) (bool, error)
}

// BulkIndexerProvider exposes bulk index control from a search service.
type BulkIndexerProvider interface {
	// BulkIndexer returns control, or nil if unsupported.
	BulkIndexer() BulkIndexer
}

// EmbeddingProvider defines the interface for embedding generation
type EmbeddingProvider interface {
	// CreateEmbedding generates embedding for the given text
	CreateEmbedding(ctx context.Context, text string) ([]float32, error)

	// BatchCreateEmbeddings generates embeddings for any number of texts;
	// implementations are responsible for splitting the batch to respect
	// their provider's per-request limits
	BatchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the dimensionality of the embeddings
	Dimensions() int
}

// UpstreamConfig holds configuration for the upstream service
type UpstreamConfig struct {
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters"`
}

// Reindex throughput defaults and bounds. Workers are concurrent embedding
// pipelines; batch size is posts fetched/embedded per request. Defaults sit
// comfortably inside OpenAI Tier 1 rate limits.
const (
	DefaultReindexWorkers   = 4
	MaxReindexWorkers       = 32
	DefaultReindexBatchSize = 200
	MaxReindexBatchSize     = 1000
)

// HNSW m (graph connections per row). pgvector accepts 2–100; 8 is a
// lower-RAM default than pgvector's own 16.
const (
	DefaultHNSWM = 8
	MinHNSWM     = 2
	MaxHNSWM     = 100
)

// Embedding column element types. "vector" is float32; "halfvec" is float16
// (pgvector 0.7+). Unset or unknown values normalize to vector.
const (
	VectorElementTypeVector  = "vector"
	VectorElementTypeHalfvec = "halfvec"
)

// ReindexIndexStrategy*: maintain updates ANN during load; defer rebuilds after.
const (
	ReindexIndexStrategyMaintain = "maintain"
	ReindexIndexStrategyDefer    = "defer"
)

// ServiceConfig holds configuration for the embedding search service
type EmbeddingSearchConfig struct {
	Type                 string           `json:"type"`
	VectorStore          UpstreamConfig   `json:"vectorStore"`
	EmbeddingProvider    UpstreamConfig   `json:"embeddingProvider"`
	Parameters           json.RawMessage  `json:"parameters"`
	Dimensions           int              `json:"dimensions"`
	HNSWM                int              `json:"hnswM,omitempty"`
	VectorElementType    string           `json:"vectorElementType,omitempty"`
	ChunkingOptions      chunking.Options `json:"chunkingOptions"`
	ReindexWorkers       int              `json:"reindexWorkers,omitempty"`
	ReindexBatchSize     int              `json:"reindexBatchSize,omitempty"`
	ReindexIndexStrategy string           `json:"reindexIndexStrategy,omitempty"`
	RecencyBiasEnabled   bool             `json:"recencyBiasEnabled,omitempty"`
	RecencyHalfLifeDays  float64          `json:"recencyHalfLifeDays,omitempty"`
	RecencyFloor         float64          `json:"recencyFloor,omitempty"`
}

// GetReindexWorkers returns the configured reindex worker count, clamped to
// valid bounds, with unset (<=0) falling back to the default.
func (c *EmbeddingSearchConfig) GetReindexWorkers() int {
	if c.ReindexWorkers <= 0 {
		return DefaultReindexWorkers
	}
	return min(c.ReindexWorkers, MaxReindexWorkers)
}

// GetHNSWM returns the configured HNSW m, clamped to pgvector's [2, 100]
// range, with unset (<=0) falling back to the default.
func (c *EmbeddingSearchConfig) GetHNSWM() int {
	if c.HNSWM <= 0 {
		return DefaultHNSWM
	}
	if c.HNSWM < MinHNSWM {
		return MinHNSWM
	}
	return min(c.HNSWM, MaxHNSWM)
}

// NormalizeVectorElementType keeps only "halfvec"; anything else (including
// unset) is "vector".
func NormalizeVectorElementType(elementType string) string {
	if elementType == VectorElementTypeHalfvec {
		return VectorElementTypeHalfvec
	}
	return VectorElementTypeVector
}

// GetVectorElementType returns the configured embedding column type.
func (c *EmbeddingSearchConfig) GetVectorElementType() string {
	return NormalizeVectorElementType(c.VectorElementType)
}

// GetReindexBatchSize returns the configured reindex batch size, clamped to
// valid bounds, with unset (<=0) falling back to the default.
func (c *EmbeddingSearchConfig) GetReindexBatchSize() int {
	if c.ReindexBatchSize <= 0 {
		return DefaultReindexBatchSize
	}
	return min(c.ReindexBatchSize, MaxReindexBatchSize)
}

// GetRecencyBiasSettings resolves the recency bias config: unset (<=0)
// half-life and floor fall back to defaults; the floor is clamped to [0, 1].
// These are query-time-only settings; changing them never requires a reindex.
func (c *EmbeddingSearchConfig) GetRecencyBiasSettings() RecencyBiasSettings {
	settings := RecencyBiasSettings{
		Enabled:      c.RecencyBiasEnabled,
		HalfLifeDays: c.RecencyHalfLifeDays,
		Floor:        c.RecencyFloor,
	}
	if settings.HalfLifeDays <= 0 {
		settings.HalfLifeDays = DefaultRecencyHalfLifeDays
	}
	if settings.Floor <= 0 {
		settings.Floor = DefaultRecencyFloor
	}
	settings.Floor = min(settings.Floor, 1)
	return settings
}

// EffectiveReindexIndexStrategy: defer if set, otherwise maintain.
func (c *EmbeddingSearchConfig) EffectiveReindexIndexStrategy() string {
	switch c.ReindexIndexStrategy {
	case ReindexIndexStrategyDefer:
		return ReindexIndexStrategyDefer
	default:
		return ReindexIndexStrategyMaintain
	}
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
