// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-ai/chunking"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/postgres"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These integration tests require PostgreSQL with pgvector extension installed.
// Tests verify the full embedding search system working together.

var rootDSN = "postgres://mmuser:mostest@localhost:5432/postgres?sslmode=disable"

// testDB creates a test database and returns a connection to it.
func testDB(t *testing.T) *sqlx.DB {
	rootDB, err := sqlx.Connect("postgres", rootDSN)
	if err != nil {
		t.Skipf("Skipping integration test: PostgreSQL not available: %v", err)
	}
	defer rootDB.Close()

	// Check if pgvector extension is available
	var hasVector bool
	err = rootDB.Get(&hasVector, "SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE name = 'vector')")
	if err != nil {
		t.Skipf("Skipping integration test: failed to check for vector extension: %v", err)
	}
	if !hasVector {
		t.Skip("Skipping integration test: pgvector extension not available in PostgreSQL")
	}

	// Create a unique database name
	dbName := fmt.Sprintf("integration_test_%d", model.GetMillis())

	_, err = rootDB.Exec("CREATE DATABASE " + dbName)
	require.NoError(t, err, "Failed to create test database")
	t.Logf("Created test database: %s", dbName)

	testDSN := fmt.Sprintf("postgres://mmuser:mostest@localhost:5432/%s?sslmode=disable", dbName)
	db, err := sqlx.Connect("postgres", testDSN)
	if err != nil {
		_, _ = rootDB.Exec("DROP DATABASE " + dbName)
		require.NoError(t, err, "Failed to connect to test database")
	}

	t.Setenv("INTEGRATION_TEST_DB", dbName)

	// Enable pgvector extension
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		db.Close()
		dropTestDB(t)
		require.NoError(t, err, "Failed to create vector extension")
	}

	// Create mock tables
	tables := []string{
		`CREATE TABLE IF NOT EXISTS Posts (
			Id TEXT PRIMARY KEY,
			CreateAt BIGINT NOT NULL,
			DeleteAt BIGINT NOT NULL DEFAULT 0,
			Message TEXT NOT NULL DEFAULT '',
			UserId TEXT NOT NULL DEFAULT '',
			ChannelId TEXT NOT NULL DEFAULT '',
			Type TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS Channels (
			Id TEXT PRIMARY KEY,
			Name TEXT NOT NULL,
			DisplayName TEXT NOT NULL,
			Type TEXT NOT NULL,
			TeamId TEXT NOT NULL DEFAULT '',
			DeleteAt BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS ChannelMembers (
			ChannelId TEXT NOT NULL,
			UserId TEXT NOT NULL,
			PRIMARY KEY(ChannelId, UserId)
		)`,
	}

	for _, tableSQL := range tables {
		_, err = db.Exec(tableSQL)
		if err != nil {
			db.Close()
			dropTestDB(t)
			require.NoError(t, err, "Failed to create test tables")
		}
	}

	return db
}

func dropTestDB(t *testing.T) {
	dbName := os.Getenv("INTEGRATION_TEST_DB")
	if dbName == "" {
		return
	}

	rootDB, err := sqlx.Connect("postgres", rootDSN)
	require.NoError(t, err, "Failed to connect to PostgreSQL to drop test database")
	defer rootDB.Close()

	if !t.Failed() {
		_, err = rootDB.Exec("DROP DATABASE " + dbName)
		require.NoError(t, err, "Failed to drop test database")
	}
}

func cleanupDB(t *testing.T, db *sqlx.DB) {
	if db == nil {
		return
	}
	err := db.Close()
	require.NoError(t, err, "Failed to close database connection")
	dropTestDB(t)
}

// addTestChannel adds a channel and optionally its members
func addTestChannel(t *testing.T, db *sqlx.DB, channelID, teamID, channelType string, memberUserIDs []string) {
	_, err := db.Exec(
		"INSERT INTO Channels (Id, Name, DisplayName, Type, TeamId, DeleteAt) VALUES ($1, $2, $3, $4, $5, 0) ON CONFLICT (Id) DO NOTHING",
		channelID, "channel-"+channelID, "Channel "+channelID, channelType, teamID)
	require.NoError(t, err)

	for _, userID := range memberUserIDs {
		_, err := db.Exec(
			"INSERT INTO ChannelMembers (ChannelId, UserId) VALUES ($1, $2) ON CONFLICT (ChannelId, UserId) DO NOTHING",
			channelID, userID)
		require.NoError(t, err)
	}
}

// addTestPost adds a post to the Posts table
func addTestPost(t *testing.T, db *sqlx.DB, postID, userID, channelID, message string, createAt int64) {
	_, err := db.Exec(
		"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, UserId, ChannelId, Type) VALUES ($1, $2, 0, $3, $4, $5, '') ON CONFLICT (Id) DO NOTHING",
		postID, createAt, message, userID, channelID)
	require.NoError(t, err)
}

// createFullSearchSystem creates a CompositeSearch with mock provider and real PGVector
func createFullSearchSystem(t *testing.T, db *sqlx.DB, dimensions int) embeddings.EmbeddingSearch {
	provider := embeddings.NewMockEmbeddingProvider(dimensions)

	pgVectorConfig := postgres.PGVectorConfig{
		Dimensions: dimensions,
	}
	vectorStore, err := postgres.NewPGVector(db, pgVectorConfig)
	require.NoError(t, err)

	chunkingOpts := chunking.Options{
		ChunkSize:        500,
		ChunkOverlap:     50,
		ChunkingStrategy: "sentences",
	}

	return embeddings.NewCompositeSearch(vectorStore, provider, chunkingOpts)
}

// TestBasicIndexAndSearchMechanics tests that the indexing and search plumbing works
// Note: This uses mock embeddings, so it tests mechanics, not semantic relevance.
// For semantic search quality tests, see search/search_eval_test.go
func TestBasicIndexAndSearchMechanics(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	const dimensions = 64 // Small dimensions for faster testing
	search := createFullSearchSystem(t, db, dimensions)
	ctx := context.Background()

	// Set up test channel with a user who has access
	addTestChannel(t, db, "channel1", "team1", "O", []string{"user1"})

	// Create test posts
	now := model.GetMillis()
	testPosts := []struct {
		id      string
		message string
		offset  int64
	}{
		{"post1", "The quick brown fox jumps over the lazy dog", 0},
		{"post2", "A fast orange fox leaps across a sleepy hound", 1000},
		{"post3", "Database performance optimization techniques for PostgreSQL", 2000},
	}

	// Add posts to database and index them
	var docs []embeddings.PostDocument
	for _, p := range testPosts {
		addTestPost(t, db, p.id, "user1", "channel1", p.message, now+p.offset)
		docs = append(docs, embeddings.PostDocument{
			PostID:    p.id,
			CreateAt:  now + p.offset,
			TeamID:    "team1",
			ChannelID: "channel1",
			UserID:    "user1",
			Content:   p.message,
		})
	}

	// Store all documents
	err := search.Store(ctx, docs)
	require.NoError(t, err)

	// Verify documents are stored
	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, len(testPosts), "All posts should be indexed")

	// Search returns results (mechanics work, but scores are meaningless with mock embeddings)
	results, err := search.Search(ctx, "any query", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user1",
	})
	require.NoError(t, err)
	assert.Len(t, results, 3, "Should return all indexed posts")

	// Test time filter works
	results2, err := search.Search(ctx, "query", embeddings.SearchOptions{
		Limit:        5,
		UserID:       "user1",
		CreatedAfter: now + 500,
	})
	require.NoError(t, err)
	for _, result := range results2 {
		assert.Greater(t, result.Document.CreateAt, now+500, "All results should be after filter time")
	}
}

// TestReindexWithDimensionMismatch tests behavior when dimension changes between indexes
func TestReindexWithDimensionMismatch(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	ctx := context.Background()

	// Set up test channel
	addTestChannel(t, db, "channel1", "team1", "O", []string{"user1"})

	// First, create search with 64 dimensions and index some posts
	now := model.GetMillis()
	addTestPost(t, db, "post1", "user1", "channel1", "Test content for dimension testing", now)

	search64 := createFullSearchSystem(t, db, 64)

	doc := embeddings.PostDocument{
		PostID:    "post1",
		CreateAt:  now,
		TeamID:    "team1",
		ChannelID: "channel1",
		UserID:    "user1",
		Content:   "Test content for dimension testing",
	}

	err := search64.Store(ctx, []embeddings.PostDocument{doc})
	require.NoError(t, err)

	// Verify post was indexed with 64 dimensions
	var storedDim int
	err = db.Get(&storedDim, "SELECT vector_dims(embedding) FROM llm_posts_embeddings LIMIT 1")
	require.NoError(t, err)
	assert.Equal(t, 64, storedDim)

	// Search works with matching dimensions
	results, err := search64.Search(ctx, "test content", embeddings.SearchOptions{
		Limit:  5,
		UserID: "user1",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, results)

	// Now attempt to create a new search system with different dimensions
	// This simulates a model configuration change
	// The pgvector table already exists with 64 dimensions, so this should fail
	// when trying to store new embeddings with different dimensions

	// First, clear the index to simulate a reindex scenario
	err = search64.Clear(ctx)
	require.NoError(t, err)

	// Verify index is cleared
	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
	require.NoError(t, err)
	assert.Equal(t, 0, count, "Index should be empty after clear")

	// The vector store was created with 64 dimensions and the table column is fixed
	// A proper reindex requires creating a new vector store with correct dimensions

	// Create search with 128 dimensions - this will fail because the table column is 64 dims
	search128 := createFullSearchSystem(t, db, 128)

	doc128 := embeddings.PostDocument{
		PostID:    "post2",
		CreateAt:  now + 1000,
		TeamID:    "team1",
		ChannelID: "channel1",
		UserID:    "user1",
		Content:   "New content with different dimensions",
	}

	// This should error because embedding dimensions don't match table
	addTestPost(t, db, "post2", "user1", "channel1", "New content with different dimensions", now+1000)
	err = search128.Store(ctx, []embeddings.PostDocument{doc128})
	assert.Error(t, err, "Storing with mismatched dimensions should fail")
	assert.Contains(t, err.Error(), "dimensions", "Error should mention dimension mismatch")
}

// Note: Semantic relevance testing with real embeddings is in search/search_eval_test.go
// The mock embedding provider cannot test semantic similarity.

// TestConcurrentIndexingAndSearching tests performance under concurrent load
func TestConcurrentIndexingAndSearching(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	const dimensions = 64
	search := createFullSearchSystem(t, db, dimensions)
	ctx := context.Background()

	// Set up multiple channels
	for i := 0; i < 5; i++ {
		addTestChannel(t, db, fmt.Sprintf("channel%d", i), "team1", "O", []string{"user1"})
	}

	now := model.GetMillis()

	// Create initial posts
	var initialDocs []embeddings.PostDocument
	for i := 0; i < 50; i++ {
		postID := fmt.Sprintf("initial_post_%d", i)
		channelID := fmt.Sprintf("channel%d", i%5)
		message := fmt.Sprintf("Initial post content number %d about various topics", i)
		addTestPost(t, db, postID, "user1", channelID, message, now+int64(i))

		initialDocs = append(initialDocs, embeddings.PostDocument{
			PostID:    postID,
			CreateAt:  now + int64(i),
			TeamID:    "team1",
			ChannelID: channelID,
			UserID:    "user1",
			Content:   message,
		})
	}

	err := search.Store(ctx, initialDocs)
	require.NoError(t, err)

	// Concurrent operations
	var wg sync.WaitGroup
	var indexErrors int32
	var searchErrors int32
	var successfulSearches int32
	var successfulIndexes int32

	// Pre-create all concurrent posts in the database first (needed for foreign key)
	var concurrentDocs []embeddings.PostDocument
	for i := 0; i < 5; i++ {
		for j := 0; j < 10; j++ {
			postID := fmt.Sprintf("concurrent_post_%d_%d", i, j)
			channelID := fmt.Sprintf("channel%d", i%5)
			message := fmt.Sprintf("Concurrent indexed content %d-%d about testing", i, j)
			createAt := now + int64(1000+i*100+j)

			addTestPost(t, db, postID, "user1", channelID, message, createAt)

			concurrentDocs = append(concurrentDocs, embeddings.PostDocument{
				PostID:    postID,
				CreateAt:  createAt,
				TeamID:    "team1",
				ChannelID: channelID,
				UserID:    "user1",
				Content:   message,
			})
		}
	}

	// Start concurrent indexing goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				docIdx := idx*10 + j
				doc := concurrentDocs[docIdx]

				if storeErr := search.Store(ctx, []embeddings.PostDocument{doc}); storeErr != nil {
					atomic.AddInt32(&indexErrors, 1)
				} else {
					atomic.AddInt32(&successfulIndexes, 1)
				}
			}
		}(i)
	}

	// Start concurrent search goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			queries := []string{"initial content", "various topics", "testing concurrent", "post number"}
			for j := 0; j < 5; j++ {
				query := queries[j%len(queries)]
				_, searchErr := search.Search(ctx, query, embeddings.SearchOptions{
					Limit:  10,
					UserID: "user1",
				})
				if searchErr != nil {
					atomic.AddInt32(&searchErrors, 1)
				} else {
					atomic.AddInt32(&successfulSearches, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Concurrent indexing: %d successful, %d errors", successfulIndexes, indexErrors)
	t.Logf("Concurrent searching: %d successful, %d errors", successfulSearches, searchErrors)

	// Verify most operations succeeded
	assert.Zero(t, indexErrors, "No indexing errors expected")
	assert.Zero(t, searchErrors, "No search errors expected")
	assert.Equal(t, int32(50), successfulIndexes, "All indexing operations should succeed")
	assert.Equal(t, int32(50), successfulSearches, "All search operations should succeed")

	// Verify final state
	var totalPosts int
	err = db.Get(&totalPosts, "SELECT COUNT(DISTINCT post_id) FROM llm_posts_embeddings")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, totalPosts, 100, "Should have at least 100 unique posts indexed (50 initial + 50 concurrent)")
}

// TestMultipleChannelPermissionIsolation tests that users only see posts from channels they're members of
func TestMultipleChannelPermissionIsolation(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	const dimensions = 64
	search := createFullSearchSystem(t, db, dimensions)
	ctx := context.Background()

	// Create channels with different members
	addTestChannel(t, db, "public_channel", "team1", "O", []string{"user1", "user2", "user3"})
	addTestChannel(t, db, "private_channel_a", "team1", "P", []string{"user1"})
	addTestChannel(t, db, "private_channel_b", "team1", "P", []string{"user2"})
	addTestChannel(t, db, "shared_private", "team1", "P", []string{"user1", "user2"})

	now := model.GetMillis()

	// Create posts in each channel
	posts := []struct {
		id        string
		channelID string
		message   string
	}{
		{"public_1", "public_channel", "Public message visible to everyone"},
		{"private_a_1", "private_channel_a", "Secret message only for user1"},
		{"private_b_1", "private_channel_b", "Secret message only for user2"},
		{"shared_1", "shared_private", "Shared secret for user1 and user2"},
	}

	var docs []embeddings.PostDocument
	for i, p := range posts {
		addTestPost(t, db, p.id, "poster", p.channelID, p.message, now+int64(i))
		docs = append(docs, embeddings.PostDocument{
			PostID:    p.id,
			CreateAt:  now + int64(i),
			TeamID:    "team1",
			ChannelID: p.channelID,
			UserID:    "poster",
			Content:   p.message,
		})
	}

	err := search.Store(ctx, docs)
	require.NoError(t, err)

	// Test user1's view
	resultsUser1, err := search.Search(ctx, "message secret", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user1",
	})
	require.NoError(t, err)

	user1PostIDs := make(map[string]bool)
	for _, r := range resultsUser1 {
		user1PostIDs[r.Document.PostID] = true
	}

	assert.True(t, user1PostIDs["public_1"], "User1 should see public post")
	assert.True(t, user1PostIDs["private_a_1"], "User1 should see their private channel")
	assert.False(t, user1PostIDs["private_b_1"], "User1 should NOT see user2's private channel")
	assert.True(t, user1PostIDs["shared_1"], "User1 should see shared private channel")

	// Test user2's view
	resultsUser2, err := search.Search(ctx, "message secret", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user2",
	})
	require.NoError(t, err)

	user2PostIDs := make(map[string]bool)
	for _, r := range resultsUser2 {
		user2PostIDs[r.Document.PostID] = true
	}

	assert.True(t, user2PostIDs["public_1"], "User2 should see public post")
	assert.False(t, user2PostIDs["private_a_1"], "User2 should NOT see user1's private channel")
	assert.True(t, user2PostIDs["private_b_1"], "User2 should see their private channel")
	assert.True(t, user2PostIDs["shared_1"], "User2 should see shared private channel")

	// Test user3's view (only in public channel)
	resultsUser3, err := search.Search(ctx, "message secret", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user3",
	})
	require.NoError(t, err)

	user3PostIDs := make(map[string]bool)
	for _, r := range resultsUser3 {
		user3PostIDs[r.Document.PostID] = true
	}

	assert.True(t, user3PostIDs["public_1"], "User3 should see public post")
	assert.False(t, user3PostIDs["private_a_1"], "User3 should NOT see any private channels")
	assert.False(t, user3PostIDs["private_b_1"], "User3 should NOT see any private channels")
	assert.False(t, user3PostIDs["shared_1"], "User3 should NOT see shared private")
}

// TestDeleteAndReindex tests deleting posts and verifying they're removed from search
func TestDeleteAndReindex(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	const dimensions = 64
	search := createFullSearchSystem(t, db, dimensions)
	ctx := context.Background()

	addTestChannel(t, db, "channel1", "team1", "O", []string{"user1"})

	now := model.GetMillis()

	// Index some posts
	postIDs := []string{"keep1", "delete1", "keep2", "delete2", "keep3"}
	for i, postID := range postIDs {
		message := fmt.Sprintf("Content for post %s with index %d", postID, i)
		addTestPost(t, db, postID, "user1", "channel1", message, now+int64(i))

		doc := embeddings.PostDocument{
			PostID:    postID,
			CreateAt:  now + int64(i),
			TeamID:    "team1",
			ChannelID: "channel1",
			UserID:    "user1",
			Content:   message,
		}
		err := search.Store(ctx, []embeddings.PostDocument{doc})
		require.NoError(t, err)
	}

	// Verify all posts are indexed
	var countBefore int
	err := db.Get(&countBefore, "SELECT COUNT(DISTINCT post_id) FROM llm_posts_embeddings")
	require.NoError(t, err)
	assert.Equal(t, 5, countBefore)

	// Delete some posts
	err = search.Delete(ctx, []string{"delete1", "delete2"})
	require.NoError(t, err)

	// Verify deleted posts are removed
	var countAfter int
	err = db.Get(&countAfter, "SELECT COUNT(DISTINCT post_id) FROM llm_posts_embeddings")
	require.NoError(t, err)
	assert.Equal(t, 3, countAfter)

	// Search should not find deleted posts
	results, err := search.Search(ctx, "content post index", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user1",
	})
	require.NoError(t, err)

	for _, r := range results {
		assert.NotEqual(t, "delete1", r.Document.PostID, "Deleted post should not appear in results")
		assert.NotEqual(t, "delete2", r.Document.PostID, "Deleted post should not appear in results")
	}

	// Test that posts soft-deleted on the Mattermost server are excluded from search
	// Even if they remain in the embeddings table, the search query joins with Posts
	// and filters out posts where DeleteAt != 0
	_, err = db.Exec("UPDATE Posts SET DeleteAt = $1 WHERE Id = $2", model.GetMillis(), "keep1")
	require.NoError(t, err)

	// Verify the embedding still exists in the index
	var embeddingExists bool
	err = db.Get(&embeddingExists, "SELECT EXISTS(SELECT 1 FROM llm_posts_embeddings WHERE post_id = 'keep1')")
	require.NoError(t, err)
	assert.True(t, embeddingExists, "Embedding should still exist after server-side deletion")

	// But search should not return the soft-deleted post
	results, err = search.Search(ctx, "content post index", embeddings.SearchOptions{
		Limit:  10,
		UserID: "user1",
	})
	require.NoError(t, err)

	for _, r := range results {
		assert.NotEqual(t, "keep1", r.Document.PostID, "Server-deleted post should not appear in search results")
	}

	// Verify only keep2 and keep3 are returned
	foundPostIDs := make(map[string]bool)
	for _, r := range results {
		foundPostIDs[r.Document.PostID] = true
	}
	assert.True(t, foundPostIDs["keep2"], "keep2 should be in results")
	assert.True(t, foundPostIDs["keep3"], "keep3 should be in results")
	assert.Len(t, foundPostIDs, 2, "Should only have 2 posts in results")
}

// TestChunkingBehavior tests that long posts are properly chunked and searchable
func TestChunkingBehavior(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	const dimensions = 64
	search := createFullSearchSystem(t, db, dimensions)
	ctx := context.Background()

	addTestChannel(t, db, "channel1", "team1", "O", []string{"user1"})

	now := model.GetMillis()

	// Create a long post that should be chunked
	longContent := `This is the first paragraph of a very long post about software development.
It discusses various programming concepts and best practices for writing clean code.

This is the second paragraph that talks about testing strategies and methodologies.
We cover unit testing, integration testing, and end-to-end testing approaches.

This is the third paragraph focusing on deployment and DevOps practices.
Continuous integration and continuous deployment are essential for modern software.

This is the fourth paragraph about monitoring and observability in production systems.
Proper logging, metrics, and tracing help diagnose issues quickly.

This is the fifth paragraph discussing team collaboration and code review processes.
Good communication and documentation are key to successful projects.`

	postID := "long_post"
	addTestPost(t, db, postID, "user1", "channel1", longContent, now)

	doc := embeddings.PostDocument{
		PostID:    postID,
		CreateAt:  now,
		TeamID:    "team1",
		ChannelID: "channel1",
		UserID:    "user1",
		Content:   longContent,
	}

	err := search.Store(ctx, []embeddings.PostDocument{doc})
	require.NoError(t, err)

	// Check that the post was chunked
	var chunkCount int
	err = db.Get(&chunkCount, "SELECT COUNT(*) FROM llm_posts_embeddings WHERE post_id = $1", postID)
	require.NoError(t, err)
	t.Logf("Long post was split into %d chunks", chunkCount)
	assert.Greater(t, chunkCount, 1, "Long post should be chunked into multiple parts")

	// Verify chunk metadata is stored correctly
	var chunks []struct {
		IsChunk    bool `db:"is_chunk"`
		ChunkIndex *int `db:"chunk_index"`
	}
	err = db.Select(&chunks, "SELECT is_chunk, chunk_index FROM llm_posts_embeddings WHERE post_id = $1 ORDER BY chunk_index", postID)
	require.NoError(t, err)

	for i, chunk := range chunks {
		assert.True(t, chunk.IsChunk, "All entries should be marked as chunks")
		require.NotNil(t, chunk.ChunkIndex, "Chunk index should not be nil")
		assert.Equal(t, i, *chunk.ChunkIndex, "Chunk indices should be sequential starting from 0")
	}

	// Note: Semantic search testing with chunked content requires real embeddings.
	// See search/search_eval_test.go for tests with real OpenAI embeddings.
}
