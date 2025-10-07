// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package postgres

import (
	"context"
	"fmt"
	"strconv"

	sq "github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-ai/chunking"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/pgvector/pgvector-go"
)

type PGVector struct {
	db *sqlx.DB
}

type PGVectorConfig struct {
	Dimensions int `json:"dimensions"`
}

func NewPGVector(db *sqlx.DB, config PGVectorConfig) (*PGVector, error) {
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
		// Index for similarity search using HNSW
		"CREATE INDEX IF NOT EXISTS llm_posts_embeddings_embedding_idx ON llm_posts_embeddings USING hnsw (embedding vector_l2_ops)",
		// Index on post_id for efficient lookups and deletions
		"CREATE INDEX IF NOT EXISTS llm_posts_embeddings_post_id_idx ON llm_posts_embeddings(post_id)",
		// Index on is_chunk to filter by chunks
		"CREATE INDEX IF NOT EXISTS llm_posts_embeddings_is_chunk_idx ON llm_posts_embeddings(is_chunk)",
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			return nil, fmt.Errorf("failed to create index: %w", err)
		}
	}

	return &PGVector{db: db}, nil
}

func (pv *PGVector) Store(ctx context.Context, docs []embeddings.PostDocument, embeddings [][]float32) error {
	for i, doc := range docs {
		id := doc.PostID
		if doc.IsChunk {
			id = fmt.Sprintf("%s_chunk_%d", doc.PostID, doc.ChunkIndex)
		}
		_, err := pv.db.NamedExecContext(ctx, `
			INSERT INTO llm_posts_embeddings (
				id, post_id, team_id, channel_id, user_id, content, embedding, created_at,
				is_chunk, chunk_index, total_chunks
			)
			VALUES (
				:id, :post_id, :team_id, :channel_id, :user_id, :content, :embedding, :created_at,
				:is_chunk, :chunk_index, :total_chunks
			)
			ON CONFLICT (id) DO UPDATE SET
				content = EXCLUDED.content,
				embedding = EXCLUDED.embedding,
				is_chunk = EXCLUDED.is_chunk,
				chunk_index = EXCLUDED.chunk_index,
				total_chunks = EXCLUDED.total_chunks`,
			map[string]interface{}{
				"id":           id,
				"post_id":      doc.PostID,
				"team_id":      doc.TeamID,
				"channel_id":   doc.ChannelID,
				"user_id":      doc.UserID,
				"content":      doc.Content,
				"embedding":    pgvector.NewVector(embeddings[i]),
				"created_at":   doc.CreateAt,
				"is_chunk":     doc.IsChunk,
				"chunk_index":  sqlNullInt(doc.IsChunk, doc.ChunkIndex),
				"total_chunks": sqlNullInt(doc.IsChunk, doc.TotalChunks),
			},
		)
		if err != nil {
			return fmt.Errorf("failed to insert vector: %w", err)
		}
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

	queryBuilder = queryBuilder.OrderBy("similarity ASC")

	if opts.Limit > 0 && opts.Limit < 100000 {
		queryBuilder = queryBuilder.Limit(uint64(opts.Limit)) //nolint:gosec
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

		score := 1 - similarity
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
