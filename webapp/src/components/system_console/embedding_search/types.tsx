// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export interface UpstreamConfig {
    type: string;
    parameters: Record<string, unknown> | null; // server sends nil json.RawMessage as JSON null
}

// Mirror the server's reindex throughput defaults and bounds
// (embeddings.DefaultReindexWorkers etc. in embeddings/embeddings.go).
export const REINDEX_DEFAULTS = {
    workers: 4,
    maxWorkers: 32,
    batchSize: 200,
    maxBatchSize: 1000,
} as const;

// Mirror the server's reindex index strategies
// (embeddings.ReindexIndexStrategy* in embeddings/embeddings.go).
export const REINDEX_INDEX_STRATEGY = {
    maintain: 'maintain',
    defer: 'defer',
} as const;

// Mirror the server's HNSW m defaults and bounds
// (embeddings.DefaultHNSWM etc. in embeddings/embeddings.go).
export const HNSW_DEFAULTS = {
    m: 8,
    min: 2,
    max: 100,
} as const;

export type ReindexIndexStrategy = typeof REINDEX_INDEX_STRATEGY[keyof typeof REINDEX_INDEX_STRATEGY];

export interface ChunkingOptions {
    chunkSize: number;
    chunkOverlap: number;
    chunkingStrategy: string;
}

export interface EmbeddingSearchConfig {
    type: string;
    vectorStore: UpstreamConfig;
    embeddingProvider: UpstreamConfig;
    parameters: Record<string, unknown> | null; // server sends nil json.RawMessage as JSON null
    dimensions: number;
    hnswM?: number;
    chunkingOptions?: ChunkingOptions;
    reindexWorkers?: number;
    reindexBatchSize?: number;
    reindexIndexStrategy?: ReindexIndexStrategy;
}

// Match the server's JobStatus struct field names
export interface JobStatusType {
    status: string; // 'running' | 'cancel_requested' | 'completed' | 'failed' | 'canceled' | 'no_job'
    error?: string;
    started_at: string; // ISO string from server's time.Time
    completed_at?: string;
    processed_rows: number;
    total_rows: number;
    resumable?: boolean;
    error_count?: number;
    node_id?: string;
    cutoff_at?: number;
    last_updated_at?: string;
    is_stale?: boolean;

    // Known values: 'building_index' (HNSW CREATE INDEX after bulk load).
    phase?: string;

    // Server JobStatus.operation: 'reindex' | 'rebuild_vector_index'
    operation?: string;
}

// Mirror the server's vector index phases
// (indexer.VectorIndexPhase* in indexer/vector_index.go).
export type VectorIndexPhase = 'dropped' | 'building' | 'repairing';

export interface StatusMessageType {
    success?: boolean;
    message?: string;
}

// Match the server's HealthCheckResult struct (includes model compatibility)
export interface HealthCheckResultType {
    db_post_count: number;
    indexed_post_count: number;
    missing_posts: number;
    status: string; // 'healthy' | 'needs_reindex' | 'mismatch' | 'error'
    checked_at: string;
    error?: string;

    // Model compatibility fields
    model_compatible: boolean;
    model_needs_reindex: boolean;
    model_compat_reason?: string;
    stored_provider_type?: string;
    stored_dimensions?: number;
    stored_model_name?: string;
    stored_hnsw_m?: number;

    // Deferred reindex owns the ANN lifecycle; search gated for dropped/building.
    vector_index_state?: {
        job_id: string;
        phase: VectorIndexPhase;
        build_started_at?: number;
    };
}
