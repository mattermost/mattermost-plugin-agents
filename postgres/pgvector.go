// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package postgres

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/v2/chunking"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/pgvector/pgvector-go"
)

// postIDs must be sorted to avoid deadlocks across batches with overlapping posts.
func lockPostIDs(ctx context.Context, tx *sqlx.Tx, postIDs []string) error {
	_, err := tx.ExecContext(ctx,
		"SELECT pg_advisory_xact_lock(hashtext(p)) FROM unnest($1::text[]) AS t(p)",
		pq.Array(postIDs),
	)
	if err != nil {
		return fmt.Errorf("failed to acquire per-post advisory lock: %w", err)
	}
	return nil
}

func uniqueSortedPostIDs(docs []embeddings.PostDocument) []string {
	seen := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		seen[doc.PostID] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// vectorIndexName is the HNSW ANN index; dropped/rebuilt around deferred bulk loads.
const vectorIndexName = "llm_posts_embeddings_embedding_idx"

// Shared by constructor and FinalizeBulkIndex so both build the same index.
const createVectorIndexQuery = "CREATE INDEX IF NOT EXISTS " + vectorIndexName +
	" ON llm_posts_embeddings USING hnsw (embedding vector_l2_ops)"

// Canonical pg_get_indexdef suffix for the expected HNSW L2 index. Same-named
// B-tree/cosine/partial/expression indexes would silently break <-> queries.
const expectedVectorIndexDefSuffix = "USING hnsw (embedding vector_l2_ops)"

type PGVector struct {
	db *sqlx.DB
}

// Compile-time check that PGVector supports deferred bulk indexing.
var _ embeddings.BulkIndexer = (*PGVector)(nil)

type PGVectorConfig struct {
	Dimensions int `json:"dimensions"`

	// SkipVectorIndex: skip constructor CREATE while a deferred reindex owns
	// the lifecycle (avoids sync rebuild of an intentionally dropped index).
	SkipVectorIndex bool `json:"-"`
}

func NewPGVector(db *sqlx.DB, config PGVectorConfig) (*PGVector, error) {
	if config.Dimensions <= 0 {
		return nil, fmt.Errorf("pgvector dimensions must be greater than 0, got %d", config.Dimensions)
	}

	// Enable pgvector extension if not already enabled
	if _, err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return nil, fmt.Errorf("failed to create vector extension: %w", err)
	}

	// Create the llm_posts_embeddings table if it doesn't exist
	createTableQuery := `
		CREATE TABLE IF NOT EXISTS llm_posts_embeddings (
			id TEXT PRIMARY KEY,             								-- Post ID or chunk ID (post_id_chunk_N)
			post_id TEXT NOT NULL REFERENCES Posts(Id) ON DELETE CASCADE,   -- Original post ID (same as id for non-chunks)
			team_id TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			content TEXT NOT NULL,
			embedding vector(` + strconv.Itoa(config.Dimensions) + `),
			created_at BIGINT NOT NULL,
			is_chunk BOOLEAN NOT NULL DEFAULT FALSE,
			chunk_index INTEGER,              -- NULL for non-chunks
			total_chunks INTEGER             -- NULL for non-chunks
		)`
	if _, err := db.Exec(createTableQuery); err != nil {
		return nil, fmt.Errorf("failed to create llm_posts_embeddings table: %w", err)
	}

	// Create indexes
	queries := []string{
		// Index on post_id for efficient lookups and deletions
		"CREATE INDEX IF NOT EXISTS llm_posts_embeddings_post_id_idx ON llm_posts_embeddings(post_id)",
		// Index on is_chunk to filter by chunks
		"CREATE INDEX IF NOT EXISTS llm_posts_embeddings_is_chunk_idx ON llm_posts_embeddings(is_chunk)",
	}
	if !config.SkipVectorIndex {
		// Index for similarity search using HNSW
		queries = append(queries, createVectorIndexQuery)
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			return nil, fmt.Errorf("failed to create index: %w", err)
		}
	}

	return &PGVector{db: db}, nil
}

func (pv *PGVector) Store(ctx context.Context, docs []embeddings.PostDocument, embeddings [][]float32) error {
	if len(docs) != len(embeddings) {
		return fmt.Errorf("mismatched input lengths: got %d documents but %d embeddings", len(docs), len(embeddings))
	}

	if len(docs) == 0 {
		return nil
	}

	postIDs := uniqueSortedPostIDs(docs)

	tx, err := pv.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if lockErr := lockPostIDs(ctx, tx, postIDs); lockErr != nil {
		return lockErr
	}

	// Drop any prior rows for these posts so a shrinking chunk count doesn't leave orphans.
	deleteQuery, deleteArgs, err := sq.
		Delete("llm_posts_embeddings").
		Where(sq.Eq{"post_id": postIDs}).
		PlaceholderFormat(sq.Dollar).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build delete query: %w", err)
	}
	if _, err := tx.ExecContext(ctx, deleteQuery, deleteArgs...); err != nil {
		return fmt.Errorf("failed to delete existing chunks: %w", err)
	}

	// Multi-row inserts, capped so 11 params/row stays well under the
	// 65,535 Postgres placeholder limit.
	const insertBatchRows = 500
	for start := 0; start < len(docs); start += insertBatchRows {
		end := min(start+insertBatchRows, len(docs))
		insert := sq.Insert("llm_posts_embeddings").Columns(
			"id", "post_id", "team_id", "channel_id", "user_id", "content", "embedding", "created_at",
			"is_chunk", "chunk_index", "total_chunks",
		)
		for i := start; i < end; i++ {
			doc := docs[i]
			id := doc.PostID
			if doc.IsChunk {
				id = fmt.Sprintf("%s_chunk_%d", doc.PostID, doc.ChunkIndex)
			}
			insert = insert.Values(
				id,
				doc.PostID,
				doc.TeamID,
				doc.ChannelID,
				doc.UserID,
				doc.Content,
				pgvector.NewVector(embeddings[i]),
				doc.CreateAt,
				doc.IsChunk,
				sqlNullInt(doc.IsChunk, doc.ChunkIndex),
				sqlNullInt(doc.IsChunk, doc.TotalChunks),
			)
		}
		insertQuery, insertArgs, err := insert.
			Suffix("ON CONFLICT (id) DO NOTHING").
			PlaceholderFormat(sq.Dollar).
			ToSql()
		if err != nil {
			return fmt.Errorf("failed to build insert query: %w", err)
		}
		if _, err := tx.ExecContext(ctx, insertQuery, insertArgs...); err != nil {
			return fmt.Errorf("failed to insert vectors: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// sqlNullInt returns NULL if the condition is false, otherwise the value
func sqlNullInt(condition bool, val int) interface{} {
	if !condition {
		return nil
	}
	return val
}

func (pv *PGVector) Search(ctx context.Context, embedding []float32, opts embeddings.SearchOptions) ([]embeddings.SearchResult, error) {
	if opts.UserID == "" {
		return nil, fmt.Errorf("user ID is required to validate permissions")
	}

	queryBuilder := sq.Select(
		"e.post_id",
		"e.team_id",
		"e.channel_id",
		"e.user_id",
		"e.created_at",
		"e.content",
		"e.is_chunk",
		"e.chunk_index",
		"e.total_chunks",
		"(e.embedding <-> ?) as similarity",
	).
		From("llm_posts_embeddings e").
		Join("Channels c ON e.channel_id = c.Id").
		Join("ChannelMembers cm ON e.channel_id = cm.ChannelId").
		Join("Posts p ON e.post_id = p.Id").
		Where("cm.UserId = ?", opts.UserID).
		Where("c.DeleteAt = 0").
		Where("p.DeleteAt = 0").
		PlaceholderFormat(sq.Dollar)

	if opts.TeamID != "" {
		queryBuilder = queryBuilder.Where(sq.Eq{"e.team_id": opts.TeamID})
	}

	if opts.ChannelID != "" {
		queryBuilder = queryBuilder.Where(sq.Eq{"e.channel_id": opts.ChannelID})
	}

	if opts.CreatedAfter != 0 {
		queryBuilder = queryBuilder.Where(sq.Gt{"e.created_at": opts.CreatedAfter})
	}

	if opts.CreatedBefore != 0 {
		queryBuilder = queryBuilder.Where(sq.Lt{"e.created_at": opts.CreatedBefore})
	}

	// Filter by MinScore in SQL when specified
	// Convert minScore to L2 distance threshold: L2 = sqrt(2(1 - score))
	if opts.MinScore > 0 {
		maxDistanceSquared := 2 * (1 - opts.MinScore)
		if maxDistanceSquared > 0 {
			maxDistance := float32(math.Sqrt(float64(maxDistanceSquared)))
			queryBuilder = queryBuilder.Where("(e.embedding <-> ?) < ?", pgvector.NewVector(embedding), maxDistance)
		}
	}

	queryBuilder = queryBuilder.OrderBy("similarity ASC")

	// Apply limit with sensible default/max
	const maxSearchLimit = 1000
	limit := opts.Limit
	if limit <= 0 || limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	queryBuilder = queryBuilder.Limit(uint64(limit)) //nolint:gosec

	if opts.Offset > 0 {
		queryBuilder = queryBuilder.Offset(uint64(opts.Offset)) //nolint:gosec
	}

	query, args, err := queryBuilder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build SQL: %w", err)
	}

	// Need to append the embedding to the args slice from the select
	args = append([]interface{}{pgvector.NewVector(embedding)}, args...)

	rows, err := pv.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query vectors with permissions: %w", err)
	}
	defer rows.Close()

	return scanSearchResults(rows, opts.MinScore)
}

// scanSearchResults extracts search results from query rows
func scanSearchResults(rows *sqlx.Rows, minScore float32) ([]embeddings.SearchResult, error) {
	var results []embeddings.SearchResult
	for rows.Next() {
		var postID, teamID, channelID, userID, content string
		var isChunk bool
		var chunkIndex, totalChunks *int
		var similarity float32
		var createAt int64

		if err := rows.Scan(
			&postID,
			&teamID,
			&channelID,
			&userID,
			&createAt,
			&content,
			&isChunk,
			&chunkIndex,
			&totalChunks,
			&similarity,
		); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Convert L2 distance to cosine similarity for normalized vectors
		// For unit vectors: L2² = 2(1 - cos(θ)), so cos(θ) = 1 - L2²/2
		// This gives a score from 1 (identical) to -1 (opposite)
		score := 1 - (similarity*similarity)/2
		if score < 0 {
			score = 0
		}

		if score < minScore {
			continue
		}

		doc := embeddings.PostDocument{
			PostID:    postID,
			CreateAt:  createAt,
			TeamID:    teamID,
			ChannelID: channelID,
			UserID:    userID,
			Content:   content,
			ChunkInfo: chunking.ChunkInfo{
				IsChunk: isChunk,
			},
		}

		if isChunk {
			if chunkIndex != nil {
				doc.ChunkIndex = *chunkIndex
			}
			if totalChunks != nil {
				doc.TotalChunks = *totalChunks
			}
		}

		results = append(results, embeddings.SearchResult{
			Document: doc,
			Score:    score,
		})
	}

	return results, nil
}

func (pv *PGVector) Delete(ctx context.Context, postIDs []string) error {
	query, args, err := sq.
		Delete("llm_posts_embeddings").
		Where(sq.Eq{"post_id": postIDs}).
		PlaceholderFormat(sq.Dollar).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to create query: %w", err)
	}
	_, err = pv.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete vectors: %w", err)
	}
	return nil
}

func (pv *PGVector) Clear(ctx context.Context) error {
	_, err := pv.db.ExecContext(ctx, "TRUNCATE TABLE llm_posts_embeddings")
	if err != nil {
		return fmt.Errorf("failed to clear vectors: %w", err)
	}
	return nil
}

// PrepareBulkIndex drops the ANN index for bulk load. Keeps post_id B-tree
// (needed by catch-up's NOT EXISTS).
func (pv *PGVector) PrepareBulkIndex(ctx context.Context) error {
	if _, err := pv.db.ExecContext(ctx, "DROP INDEX IF EXISTS "+vectorIndexName); err != nil {
		return fmt.Errorf("failed to drop vector index: %w", err)
	}
	return nil
}

// FinalizeBulkIndex rebuilds the ANN index (same DDL as the constructor).
// Drops a same-named wrong/invalid leftover first so IF NOT EXISTS cannot
// keep it. Uses a pinned connection with statement_timeout=0; querying via
// pv.db while holding it would self-deadlock on a one-conn pool.
func (pv *PGVector) FinalizeBulkIndex(ctx context.Context) error {
	conn, err := pv.db.Connx(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection for index build: %w", err)
	}
	defer conn.Close()

	if _, timeoutErr := conn.ExecContext(ctx, "SET statement_timeout = 0"); timeoutErr != nil {
		return fmt.Errorf("failed to disable statement timeout for index build: %w", timeoutErr)
	}
	// Reset GUC on every exit; caller's ctx may already be done.
	defer func() {
		resetCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(resetCtx, "RESET statement_timeout")
	}()

	status, err := vectorIndexStatus(ctx, conn)
	if err != nil {
		return fmt.Errorf("failed to check vector index state: %w", err)
	}
	if status.exists && (!status.valid || !status.definitionOK) {
		if _, dropErr := conn.ExecContext(ctx, "DROP INDEX IF EXISTS "+vectorIndexName); dropErr != nil {
			return fmt.Errorf("failed to drop invalid vector index: %w", dropErr)
		}
	}

	if _, buildErr := conn.ExecContext(ctx, createVectorIndexQuery); buildErr != nil {
		return fmt.Errorf("failed to build vector index: %w", buildErr)
	}

	status, err = vectorIndexStatus(ctx, conn)
	if err != nil {
		return fmt.Errorf("failed to verify vector index after build: %w", err)
	}
	if !status.exists || !status.valid || !status.definitionOK {
		return fmt.Errorf("vector index %s is missing or invalid after build", vectorIndexName)
	}
	return nil
}

// VectorIndexExists is true only for a valid index with the expected definition.
func (pv *PGVector) VectorIndexExists(ctx context.Context) (bool, error) {
	status, err := vectorIndexStatus(ctx, pv.db)
	if err != nil {
		return false, err
	}
	return status.exists && status.valid && status.definitionOK, nil
}

type indexStatus struct {
	exists bool
	valid  bool
	// definitionOK: exact match for expected HNSW L2 def (see suffix const).
	definitionOK bool
}

// vectorIndexStatus looks up the ANN index on llm_posts_embeddings in the
// current schema. Same-named wrong defs do not count. q may be a pinned conn.
func vectorIndexStatus(ctx context.Context, q sqlx.QueryerContext) (indexStatus, error) {
	rows := []struct {
		Valid    bool   `db:"indisvalid"`
		IndexDef string `db:"indexdef"`
	}{}
	err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT i.indisvalid, pg_get_indexdef(i.indexrelid) AS indexdef
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		JOIN pg_class t ON t.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = $1
		  AND t.relname = 'llm_posts_embeddings'
		  AND n.nspname = current_schema()`, vectorIndexName)
	if err != nil {
		return indexStatus{}, err
	}
	if len(rows) == 0 {
		return indexStatus{}, nil
	}
	return indexStatus{
		exists:       true,
		valid:        rows[0].Valid,
		definitionOK: strings.HasSuffix(rows[0].IndexDef, expectedVectorIndexDefSuffix),
	}, nil
}

// DeleteOrphaned removes embeddings whose posts no longer exist or are soft-deleted past retention.
func (pv *PGVector) DeleteOrphaned(ctx context.Context, nowTime, batchSize int64) (int64, error) {
	query := `
		WITH orphaned AS (
			SELECT e.id FROM llm_posts_embeddings e
			LEFT JOIN Posts p ON e.post_id = p.Id
			WHERE p.Id IS NULL
			   OR (p.DeleteAt > 0 AND p.DeleteAt <= $1)
			LIMIT $2
		)
		DELETE FROM llm_posts_embeddings
		WHERE id IN (SELECT id FROM orphaned)`

	result, err := pv.db.ExecContext(ctx, query, nowTime, batchSize)
	if err != nil {
		return 0, fmt.Errorf("failed to delete orphaned embeddings: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}
