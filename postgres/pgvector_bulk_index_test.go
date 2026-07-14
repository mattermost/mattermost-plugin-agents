// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package postgres

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-agents/v2/chunking"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// embeddingsTableIndexes returns the names of all indexes on
// llm_posts_embeddings, from the catalog.
func embeddingsTableIndexes(t *testing.T, db *sqlx.DB) []string {
	t.Helper()
	var names []string
	err := db.Select(&names, "SELECT indexname FROM pg_indexes WHERE tablename = 'llm_posts_embeddings' ORDER BY indexname")
	require.NoError(t, err)
	return names
}

// vectorIndexCatalogState returns (exists, valid) for the HNSW index.
func vectorIndexCatalogState(t *testing.T, db *sqlx.DB) (exists, valid bool) {
	t.Helper()
	var indisvalid []bool
	err := db.Select(&indisvalid, `
		SELECT i.indisvalid
		FROM pg_class c
		JOIN pg_index i ON i.indexrelid = c.oid
		WHERE c.relkind = 'i' AND c.relname = $1`, vectorIndexName)
	require.NoError(t, err)
	if len(indisvalid) == 0 {
		return false, false
	}
	return true, indisvalid[0]
}

func TestPrepareBulkIndex(t *testing.T) {
	t.Run("drops only the HNSW index, keeping B-tree indexes and the primary key", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		pgVector, err := NewPGVector(db, PGVectorConfig{Dimensions: 3})
		require.NoError(t, err)

		require.Contains(t, embeddingsTableIndexes(t, db), vectorIndexName)

		err = pgVector.PrepareBulkIndex(context.Background())
		require.NoError(t, err)

		indexes := embeddingsTableIndexes(t, db)
		assert.NotContains(t, indexes, vectorIndexName)
		assert.Contains(t, indexes, "llm_posts_embeddings_post_id_idx")
		assert.Contains(t, indexes, "llm_posts_embeddings_is_chunk_idx")
		assert.Contains(t, indexes, "llm_posts_embeddings_pkey")
	})

	t.Run("is idempotent when the index is already dropped", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		pgVector, err := NewPGVector(db, PGVectorConfig{Dimensions: 3})
		require.NoError(t, err)

		ctx := context.Background()
		require.NoError(t, pgVector.PrepareBulkIndex(ctx))
		require.NoError(t, pgVector.PrepareBulkIndex(ctx))

		assert.NotContains(t, embeddingsTableIndexes(t, db), vectorIndexName)
	})
}

func TestStoreAndSearchWithIndexDropped(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	pgVector, err := NewPGVector(db, PGVectorConfig{Dimensions: 3})
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, pgVector.PrepareBulkIndex(ctx))

	now := model.GetMillis()
	postIDs := []string{"post1", "post2"}
	addTestPosts(t, db, postIDs, []int64{now, now})
	addTestChannels(t, db, []string{"channel1"}, false)
	addTestChannelMembers(t, db, "channel1", []string{"user1"})

	docs := []embeddings.PostDocument{
		{PostID: "post1", CreateAt: now, TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "very similar"},
		{PostID: "post2", CreateAt: now, TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "less similar"},
	}
	embedVectors := [][]float32{
		{0.9, 0.9, 0.9},
		{0.1, 0.1, 0.1},
	}

	require.NoError(t, pgVector.Store(ctx, docs, embedVectors))

	results, err := pgVector.Search(ctx, []float32{1, 1, 1}, embeddings.SearchOptions{
		Limit:  10,
		UserID: "user1",
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "post1", results[0].Document.PostID)
	assert.Equal(t, "post2", results[1].Document.PostID)
}

func TestFinalizeBulkIndex(t *testing.T) {
	t.Run("recreates a valid index after prepare", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		pgVector, err := NewPGVector(db, PGVectorConfig{Dimensions: 3})
		require.NoError(t, err)

		ctx := context.Background()
		require.NoError(t, pgVector.PrepareBulkIndex(ctx))

		indexExists, err := pgVector.VectorIndexExists(ctx)
		require.NoError(t, err)
		require.False(t, indexExists)

		require.NoError(t, pgVector.FinalizeBulkIndex(ctx))

		catExists, catValid := vectorIndexCatalogState(t, db)
		assert.True(t, catExists)
		assert.True(t, catValid)

		indexExists, err = pgVector.VectorIndexExists(ctx)
		require.NoError(t, err)
		assert.True(t, indexExists)
	})

	t.Run("is idempotent when called twice", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		pgVector, err := NewPGVector(db, PGVectorConfig{Dimensions: 3})
		require.NoError(t, err)

		ctx := context.Background()
		require.NoError(t, pgVector.PrepareBulkIndex(ctx))
		require.NoError(t, pgVector.FinalizeBulkIndex(ctx))
		require.NoError(t, pgVector.FinalizeBulkIndex(ctx))

		catExists, catValid := vectorIndexCatalogState(t, db)
		assert.True(t, catExists)
		assert.True(t, catValid)
	})

	t.Run("recovers when an invalid index with the target name exists", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		pgVector, err := NewPGVector(db, PGVectorConfig{Dimensions: 3})
		require.NoError(t, err)

		ctx := context.Background()
		require.NoError(t, pgVector.PrepareBulkIndex(ctx))

		// Manufacture an INVALID leftover index with the target name: a
		// failing CREATE INDEX CONCURRENTLY leaves the index behind marked
		// invalid. A unique index over duplicate post_id values (two chunks
		// of the same post) fails deterministically.
		now := model.GetMillis()
		addTestPosts(t, db, []string{"dup"}, []int64{now})
		docs := []embeddings.PostDocument{
			{PostID: "dup", CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "chunk 0",
				ChunkInfo: chunking.ChunkInfo{IsChunk: true, ChunkIndex: 0, TotalChunks: 2}},
			{PostID: "dup", CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "chunk 1",
				ChunkInfo: chunking.ChunkInfo{IsChunk: true, ChunkIndex: 1, TotalChunks: 2}},
		}
		require.NoError(t, pgVector.Store(ctx, docs, [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}}))

		_, err = db.Exec("CREATE UNIQUE INDEX CONCURRENTLY " + vectorIndexName + " ON llm_posts_embeddings(post_id)")
		require.Error(t, err, "concurrent unique index over duplicate post_ids must fail")

		catExists, catValid := vectorIndexCatalogState(t, db)
		require.True(t, catExists, "failed CREATE INDEX CONCURRENTLY should leave an invalid index behind")
		require.False(t, catValid)

		// VectorIndexExists must not report the invalid leftover as present.
		indexExists, err := pgVector.VectorIndexExists(ctx)
		require.NoError(t, err)
		assert.False(t, indexExists)

		require.NoError(t, pgVector.FinalizeBulkIndex(ctx))

		catExists, catValid = vectorIndexCatalogState(t, db)
		assert.True(t, catExists)
		assert.True(t, catValid)

		// The rebuilt index must be the HNSW one, not the leftover B-tree.
		var indexdef string
		err = db.Get(&indexdef, "SELECT indexdef FROM pg_indexes WHERE indexname = $1", vectorIndexName)
		require.NoError(t, err)
		assert.Contains(t, indexdef, "hnsw")
	})
}

func TestNewPGVectorSkipVectorIndex(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	pgVector, err := NewPGVector(db, PGVectorConfig{Dimensions: 3, SkipVectorIndex: true})
	require.NoError(t, err)

	// Table and B-tree indexes exist, HNSW does not.
	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'llm_posts_embeddings'")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	indexes := embeddingsTableIndexes(t, db)
	assert.NotContains(t, indexes, vectorIndexName)
	assert.Contains(t, indexes, "llm_posts_embeddings_post_id_idx")
	assert.Contains(t, indexes, "llm_posts_embeddings_is_chunk_idx")

	indexExists, err := pgVector.VectorIndexExists(context.Background())
	require.NoError(t, err)
	assert.False(t, indexExists)

	// FinalizeBulkIndex can build the index later.
	require.NoError(t, pgVector.FinalizeBulkIndex(context.Background()))
	assert.Contains(t, embeddingsTableIndexes(t, db), vectorIndexName)
}
