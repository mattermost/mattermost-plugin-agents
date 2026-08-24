// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package postgres

import (
	"context"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-agents/v2/chunking"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func embeddingsTableOID(t *testing.T, db *sqlx.DB) int64 {
	t.Helper()
	var oid int64
	err := db.Get(&oid, `
		SELECT c.oid
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = 'llm_posts_embeddings'
			AND n.nspname = current_schema()`)
	require.NoError(t, err)
	return oid
}

func TestHalfvecTableAndIndex(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	ctx := context.Background()
	store, err := NewPGVector(db, PGVectorConfig{
		Dimensions:        3,
		HNSWM:             embeddings.DefaultHNSWM,
		VectorElementType: embeddings.VectorElementTypeHalfvec,
	})
	require.NoError(t, err)

	elementType, dims, err := store.embeddingColumnTypmod(ctx)
	require.NoError(t, err)
	assert.Equal(t, embeddings.VectorElementTypeHalfvec, elementType)
	assert.Equal(t, 3, dims)

	var typeName string
	require.NoError(t, db.Get(&typeName, `
		SELECT format_type(a.atttypid, a.atttypmod)
		FROM pg_catalog.pg_attribute a
		JOIN pg_catalog.pg_class c ON a.attrelid = c.oid
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = 'llm_posts_embeddings'
			AND n.nspname = current_schema()
			AND a.attname = 'embedding'
			AND NOT a.attisdropped`))
	assert.Equal(t, "halfvec(3)", typeName)

	var indexdef string
	require.NoError(t, db.Get(&indexdef, "SELECT indexdef FROM pg_indexes WHERE indexname = $1", vectorIndexName))
	assert.Contains(t, indexdef, "halfvec_l2_ops")
	m, ok := parseHNSWMFromIndexDef(indexdef)
	require.True(t, ok)
	assert.Equal(t, embeddings.DefaultHNSWM, m)
}

func TestHalfvecStoreSearchRoundTrip(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	store, err := NewPGVector(db, PGVectorConfig{
		Dimensions:        3,
		VectorElementType: embeddings.VectorElementTypeHalfvec,
	})
	require.NoError(t, err)

	now := model.GetMillis()
	addTestPosts(t, db, []string{"post1", "post2"}, []int64{now, now})
	addTestChannels(t, db, []string{"channel1"}, false)
	addTestChannelMembers(t, db, "channel1", []string{"system_user"})

	ctx := context.Background()
	require.NoError(t, store.Store(ctx, []embeddings.PostDocument{
		{PostID: "post1", CreateAt: now, TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "one"},
		{PostID: "post2", CreateAt: now, TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "two"},
	}, [][]float32{
		{0.7, 0.7, 0.7},
		{0.9, 0.9, 0.9},
	}))

	results, err := store.Search(ctx, []float32{1, 1, 1}, embeddings.SearchOptions{
		Limit:  2,
		UserID: "system_user",
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "post2", results[0].Document.PostID)
	assert.Equal(t, "post1", results[1].Document.PostID)
}

func TestClearTypeChangeDropsTable(t *testing.T) {
	tests := []struct {
		name        string
		fromType    string
		toType      string
		wantOpClass string
	}{
		{
			name:        "vector to halfvec",
			fromType:    embeddings.VectorElementTypeVector,
			toType:      embeddings.VectorElementTypeHalfvec,
			wantOpClass: "halfvec_l2_ops",
		},
		{
			name:        "halfvec to vector",
			fromType:    embeddings.VectorElementTypeHalfvec,
			toType:      embeddings.VectorElementTypeVector,
			wantOpClass: "vector_l2_ops",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			defer cleanupDB(t, db)

			ctx := context.Background()
			oldStore, err := NewPGVector(db, PGVectorConfig{
				Dimensions:        3,
				VectorElementType: tt.fromType,
			})
			require.NoError(t, err)

			now := model.GetMillis()
			addTestPosts(t, db, []string{"post1"}, []int64{now})
			require.NoError(t, oldStore.Store(ctx, []embeddings.PostDocument{{
				PostID: "post1", CreateAt: now, TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "old",
			}}, [][]float32{{0.1, 0.2, 0.3}}))

			oldOID := embeddingsTableOID(t, db)

			newStore, err := NewPGVector(db, PGVectorConfig{
				Dimensions:        3,
				VectorElementType: tt.toType,
			})
			require.NoError(t, err)

			elementType, dims, err := newStore.embeddingColumnTypmod(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.fromType, elementType)
			assert.Equal(t, 3, dims)

			require.NoError(t, newStore.Clear(ctx))

			elementType, dims, err = newStore.embeddingColumnTypmod(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.toType, elementType)
			assert.Equal(t, 3, dims)
			assert.NotEqual(t, oldOID, embeddingsTableOID(t, db), "type change must DROP TABLE, not TRUNCATE")

			var indexdef string
			require.NoError(t, db.Get(&indexdef, "SELECT indexdef FROM pg_indexes WHERE indexname = $1", vectorIndexName))
			assert.Contains(t, indexdef, tt.wantOpClass)
		})
	}
}

func TestClearSameTypeAndDimsTruncates(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	ctx := context.Background()
	store, err := NewPGVector(db, PGVectorConfig{
		Dimensions:        3,
		VectorElementType: embeddings.VectorElementTypeHalfvec,
	})
	require.NoError(t, err)

	now := model.GetMillis()
	addTestPosts(t, db, []string{"post1"}, []int64{now})
	require.NoError(t, store.Store(ctx, []embeddings.PostDocument{{
		PostID: "post1", CreateAt: now, TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "row",
	}}, [][]float32{{0.1, 0.2, 0.3}}))

	oldOID := embeddingsTableOID(t, db)

	require.NoError(t, store.Clear(ctx))

	elementType, dims, err := store.embeddingColumnTypmod(ctx)
	require.NoError(t, err)
	assert.Equal(t, embeddings.VectorElementTypeHalfvec, elementType)
	assert.Equal(t, 3, dims)
	assert.Equal(t, oldOID, embeddingsTableOID(t, db), "same type and dims must TRUNCATE, not DROP")

	var count int
	require.NoError(t, db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings"))
	assert.Equal(t, 0, count)
}

func TestNewPGVectorTypeMismatchDoesNotSilentlyWrite(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	ctx := context.Background()
	oldStore, err := NewPGVector(db, PGVectorConfig{Dimensions: 3})
	require.NoError(t, err)

	now := model.GetMillis()
	addTestPosts(t, db, []string{"post1", "post2"}, []int64{now, now})
	require.NoError(t, oldStore.Store(ctx, []embeddings.PostDocument{{
		PostID: "post1", CreateAt: now, TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "keep",
	}}, [][]float32{{0.1, 0.2, 0.3}}))

	halfStore, err := NewPGVector(db, PGVectorConfig{
		Dimensions:        3,
		VectorElementType: embeddings.VectorElementTypeHalfvec,
	})
	require.NoError(t, err, "construction must succeed so Full Reindex Clear() can run")

	require.Error(t, halfStore.CheckSchema(ctx))

	elementType, dims, err := halfStore.embeddingColumnTypmod(ctx)
	require.NoError(t, err)
	assert.Equal(t, embeddings.VectorElementTypeVector, elementType)
	assert.Equal(t, 3, dims, "NewPGVector must not recreate the table on type mismatch")

	err = halfStore.Store(ctx, []embeddings.PostDocument{{
		PostID: "post2", CreateAt: now, TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "should not write",
	}}, [][]float32{{0.4, 0.5, 0.6}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Full Reindex")

	_, err = halfStore.Search(ctx, []float32{0.1, 0.2, 0.3}, embeddings.SearchOptions{UserID: "user1"})
	require.Error(t, err)

	var count int
	require.NoError(t, db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings"))
	assert.Equal(t, 1, count, "type mismatch must not insert into the existing vector column")

	var typeName string
	require.NoError(t, db.Get(&typeName, `
		SELECT format_type(a.atttypid, a.atttypmod)
		FROM pg_catalog.pg_attribute a
		JOIN pg_catalog.pg_class c ON a.attrelid = c.oid
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = 'llm_posts_embeddings'
			AND n.nspname = current_schema()
			AND a.attname = 'embedding'
			AND NOT a.attisdropped`))
	assert.Equal(t, "vector(3)", typeName)
}

type countingEmbeddingProvider struct {
	inner  embeddings.EmbeddingProvider
	create int
	batch  int
}

func (p *countingEmbeddingProvider) CreateEmbedding(ctx context.Context, text string) ([]float32, error) {
	p.create++
	return p.inner.CreateEmbedding(ctx, text)
}

func (p *countingEmbeddingProvider) BatchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	p.batch++
	return p.inner.BatchCreateEmbeddings(ctx, texts)
}

func (p *countingEmbeddingProvider) Dimensions() int {
	return p.inner.Dimensions()
}

func TestCompositeSearchSkipsProviderOnTypeMismatch(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	ctx := context.Background()
	_, err := NewPGVector(db, PGVectorConfig{Dimensions: 3})
	require.NoError(t, err)

	halfStore, err := NewPGVector(db, PGVectorConfig{
		Dimensions:        3,
		VectorElementType: embeddings.VectorElementTypeHalfvec,
	})
	require.NoError(t, err)

	provider := &countingEmbeddingProvider{inner: embeddings.NewMockEmbeddingProvider(3)}
	search := embeddings.NewCompositeSearch(halfStore, provider, chunking.DefaultOptions())

	err = search.Store(ctx, []embeddings.PostDocument{{
		PostID: "post1", Content: "would be billed if embeddings ran",
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Full Reindex")
	assert.Equal(t, 0, provider.batch, "schema mismatch must not call the embedding provider")

	_, err = search.Search(ctx, "query", embeddings.SearchOptions{UserID: "user1"})
	require.Error(t, err)
	assert.Equal(t, 0, provider.create, "schema mismatch must not embed a search query")

	require.NoError(t, search.Clear(ctx))
	require.NoError(t, halfStore.CheckSchema(ctx))
}

func TestSchemaMismatchConcurrentWithClear(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	ctx := context.Background()
	_, err := NewPGVector(db, PGVectorConfig{Dimensions: 3})
	require.NoError(t, err)

	halfStore, err := NewPGVector(db, PGVectorConfig{
		Dimensions:        3,
		VectorElementType: embeddings.VectorElementTypeHalfvec,
	})
	require.NoError(t, err)

	now := model.GetMillis()
	addTestPosts(t, db, []string{"post1"}, []int64{now})
	doc := []embeddings.PostDocument{{
		PostID: "post1", CreateAt: now, TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "race",
	}}
	vec := [][]float32{{0.1, 0.2, 0.3}}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				_ = halfStore.CheckSchema(ctx)
				_ = halfStore.Store(ctx, doc, vec)
				_, _ = halfStore.Search(ctx, vec[0], embeddings.SearchOptions{UserID: "user1"})
			}
		}()
	}

	require.NoError(t, halfStore.Clear(ctx))
	wg.Wait()
	require.NoError(t, halfStore.CheckSchema(ctx))
}
