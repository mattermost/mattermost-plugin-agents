// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export interface UpstreamConfig {
    type: string;
    parameters: Record<string, unknown>;
}

export interface ChunkingOptions {
    chunkSize: number;
    chunkOverlap: number;
    minChunkSize: number;
    chunkingStrategy: string;
}

export interface EmbeddingSearchConfig {
    type: string;
    vectorStore: UpstreamConfig;
    embeddingProvider: UpstreamConfig;
    parameters: Record<string, unknown>;
    dimensions: number;
    chunkingOptions?: ChunkingOptions;
}

// Match the server's JobStatus struct field names
export interface JobStatusType {
    status: string; // 'running' | 'completed' | 'failed' | 'canceled' | 'no_job'
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
}

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
    stored_dimensions?: number;
    stored_model_name?: string;
}
