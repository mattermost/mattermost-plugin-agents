// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/chunking"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
)

// These tests require PostgreSQL with pgvector extension installed.
// Tests will fail if the database connection fails or if pgvector is not available.

// testDB creates a test database and returns a connection to it.
// This function will automatically create a temporary database for testing.
// If PG_ROOT_DSN environment variable is set, it will be used as the root connection.
// Default: "postgres://root:mostest@localhost:5432/postgres?sslmode=disable"
var rootDSN = "postgres://mmuser:mostest@localhost:5432/postgres?sslmode=disable"

func testDB(t *testing.T) *sqlx.DB {
	rootDB, err := sqlx.Connect("postgres", rootDSN)
	require.NoError(t, err, "Failed to connect to PostgreSQL. Is PostgreSQL running?")
	defer rootDB.Close()

	// Check if pgvector extension is available
	var hasVector bool
	err = rootDB.Get(&hasVector, "SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE name = 'vector')")
	require.NoError(t, err, "Failed to check for vector extension")
	require.True(t, hasVector, "pgvector extension not available in PostgreSQL. Please install it to run these tests.")

	// Create a unique database name with a timestamp
	dbName := fmt.Sprintf("pgvector_test_%d", model.GetMillis())

	// Create the test database
	_, err = rootDB.Exec("CREATE DATABASE " + dbName)
	require.NoError(t, err, "Failed to create test database")
	t.Logf("Created test database: %s", dbName)

	// Connect to the new database
	testDSN := fmt.Sprintf("postgres://mmuser:mostest@localhost:5432/%s?sslmode=disable", dbName)
	db, err := sqlx.Connect("postgres", testDSN)
	if err != nil {
		// Try to clean up the database even if connection fails
		_, _ = rootDB.Exec("DROP DATABASE " + dbName)
		require.NoError(t, err, "Failed to connect to test database")
	}

	// Store the database name for cleanup
	t.Setenv("PGVECTOR_TEST_DB", dbName)

	// Enable the pgvector extension
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		db.Close()
		dropTestDB(t)
		require.NoError(t, err, "Failed to create vector extension in test database")
	}

	// Create mock tables for tests to satisfy foreign key constraints and permission checks
	tables := []string{
		`CREATE TABLE IF NOT EXISTS Posts (
			Id TEXT PRIMARY KEY,
			CreateAt BIGINT NOT NULL,
			DeleteAt BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS Channels (
			Id TEXT PRIMARY KEY,
			Name TEXT NOT NULL,
			DisplayName TEXT NOT NULL,
			Type TEXT NOT NULL,
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

// dropTestDB drops the temporary test database
func dropTestDB(t *testing.T) {
	dbName := os.Getenv("PGVECTOR_TEST_DB")
	if dbName == "" {
		return
	}

	rootDB, err := sqlx.Connect("postgres", rootDSN)
	require.NoError(t, err, "Failed to connect to PostgreSQL to drop test database")
	defer rootDB.Close()

	// Drop the test database
	if !t.Failed() {
		_, err = rootDB.Exec("DROP DATABASE " + dbName)
		require.NoError(t, err, "Failed to drop test database")
	}
}

// cleanupDB cleans up test database state and drops the database
func cleanupDB(t *testing.T, db *sqlx.DB) {
	if db == nil {
		return
	}

	err := db.Close()
	require.NoError(t, err, "Failed to close database connection")

	dropTestDB(t)
}

// addTestPosts adds test posts to the Posts table
func addTestPosts(t *testing.T, db *sqlx.DB, postIDs []string, createAts []int64) {
	for i, postID := range postIDs {
		_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt) VALUES ($1, $2, $3) ON CONFLICT (Id) DO NOTHING",
			postID, createAts[i], 0)
		require.NoError(t, err, "Failed to insert test post")
	}
}

// addTestDeletedPosts adds test posts with DeleteAt set to the Posts table
func addTestDeletedPosts(t *testing.T, db *sqlx.DB, postIDs []string, createAts []int64, deleteAts []int64) {
	for i, postID := range postIDs {
		_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt) VALUES ($1, $2, $3) ON CONFLICT (Id) DO NOTHING",
			postID, createAts[i], deleteAts[i])
		require.NoError(t, err, "Failed to insert test deleted post")
	}
}

// addTestChannels adds test channels to the Channels table
func addTestChannels(t *testing.T, db *sqlx.DB, channelIDs []string, isDeleted bool) {
	for _, channelID := range channelIDs {
		deleteAt := int64(0)
		if isDeleted {
			deleteAt = model.GetMillis()
		}

		_, err := db.Exec(
			"INSERT INTO Channels (Id, Name, DisplayName, Type, DeleteAt) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (Id) DO NOTHING",
			channelID,
			fmt.Sprintf("name-%s", channelID),
			fmt.Sprintf("display-%s", channelID),
			"O", // Open channel
			deleteAt,
		)
		require.NoError(t, err, "Failed to insert test channel")
	}
}

// addTestChannelMembers adds test channel memberships
func addTestChannelMembers(t *testing.T, db *sqlx.DB, channelID string, userIDs []string) {
	for _, userID := range userIDs {
		_, err := db.Exec(
			"INSERT INTO ChannelMembers (ChannelId, UserId) VALUES ($1, $2) ON CONFLICT (ChannelId, UserId) DO NOTHING",
			channelID,
			userID,
		)
		require.NoError(t, err, "Failed to insert test channel member")
	}
}

func TestNewPGVector(t *testing.T) {
	t.Run("successfully creates PGVector instance and table", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		config := PGVectorConfig{
			Dimensions: 1536,
		}

		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)
		assert.NotNil(t, pgVector)

		// Verify the table was created
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'llm_posts_embeddings'")
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

func TestStore(t *testing.T) {
	t.Run("successfully stores documents and their embeddings", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		// Set up PGVector
		config := PGVectorConfig{
			Dimensions: 3, // Small dimensions for test
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// Create test data
		now := model.GetMillis()
		postIDs := []string{"post1", "post2"}
		createAts := []int64{now, now}
		addTestPosts(t, db, postIDs, createAts)

		docs := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "This is test content 1",
			},
			{
				PostID:    "post2",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel2",
				UserID:    "user2",
				Content:   "This is test content 2",
			},
		}

		embedVectors := [][]float32{
			{0.1, 0.2, 0.3},
			{0.4, 0.5, 0.6},
		}

		ctx := context.Background()

		// Store the documents
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Verify documents were stored
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 2, count)
	})

	t.Run("successfully stores chunks", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		// Set up PGVector
		config := PGVectorConfig{
			Dimensions: 3, // Small dimensions for test
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// Create test data
		now := model.GetMillis()
		postIDs := []string{"post1"}
		createAts := []int64{now}
		addTestPosts(t, db, postIDs, createAts)

		docs := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "This is ",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  0,
					TotalChunks: 2,
				},
			},
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "the full content",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  1,
					TotalChunks: 2,
				},
			},
		}

		embedVectors := [][]float32{
			{0.1, 0.2, 0.3},
			{0.4, 0.5, 0.6},
		}

		ctx := context.Background()

		// Store the documents
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Verify documents were stored
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 2, count)

		// Verify chunk data
		var chunkCount int
		err = db.Get(&chunkCount, "SELECT COUNT(*) FROM llm_posts_embeddings WHERE is_chunk = true")
		require.NoError(t, err)
		assert.Equal(t, 2, chunkCount)
	})
}

func TestStoreUpdate(t *testing.T) {
	t.Run("updates existing document when storing with same ID", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		// Set up PGVector
		config := PGVectorConfig{
			Dimensions: 3, // Small dimensions for test
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// Create test data
		now := model.GetMillis()
		postIDs := []string{"post1"}
		createAts := []int64{now}
		addTestPosts(t, db, postIDs, createAts)

		// First document version
		docs1 := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Original content",
			},
		}

		embedVectors1 := [][]float32{
			{0.1, 0.2, 0.3},
		}

		// Updated document version
		docs2 := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Updated content",
			},
		}

		embedVectors2 := [][]float32{
			{0.4, 0.5, 0.6},
		}

		ctx := context.Background()

		// Store the original document
		err = pgVector.Store(ctx, docs1, embedVectors1)
		require.NoError(t, err)

		// Store the updated document
		err = pgVector.Store(ctx, docs2, embedVectors2)
		require.NoError(t, err)

		// Verify we still have just one document (update instead of insert)
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		// Verify the content was updated
		var content string
		err = db.Get(&content, "SELECT content FROM llm_posts_embeddings WHERE id = 'post1'")
		require.NoError(t, err)
		assert.Equal(t, "Updated content", content)
	})
}

func TestSearch(t *testing.T) {
	// Setup test data with system user for non-permission tests
	setupSearchTest := func(t *testing.T) (context.Context, *PGVector, *sqlx.DB, []int64, []float32) {
		db := testDB(t)

		// Set up PGVector
		config := PGVectorConfig{
			Dimensions: 3, // Small dimensions for test
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// Create test data
		now := model.GetMillis()
		postIDs := []string{"post1", "post2", "post3", "post4"}
		createAts := []int64{now - 2000, now - 1500, now - 1000, now - 500}
		addTestPosts(t, db, postIDs, createAts)

		// Create the channels needed for our tests
		channelIDs := []string{"channel1", "channel2", "channel3", "channel4"}
		addTestChannels(t, db, channelIDs, false)

		// Add channel memberships for a test user that has access to all channels
		// This ensures that tests work with the new permission filtering
		systemUserID := "system_user"
		for _, channelID := range channelIDs {
			addTestChannelMembers(t, db, channelID, []string{systemUserID})
		}

		docs := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  createAts[0],
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Content for team 1 channel 1",
			},
			{
				PostID:    "post2",
				CreateAt:  createAts[1],
				TeamID:    "team1",
				ChannelID: "channel2",
				UserID:    "user1",
				Content:   "Content for team 1 channel 2",
			},
			{
				PostID:    "post3",
				CreateAt:  createAts[2],
				TeamID:    "team2",
				ChannelID: "channel3",
				UserID:    "user2",
				Content:   "Content for team 2 channel 3",
			},
			{
				PostID:    "post4",
				CreateAt:  createAts[3],
				TeamID:    "team2",
				ChannelID: "channel4",
				UserID:    "user2",
				Content:   "Content for team 2 channel 4",
			},
		}

		// Create vectors with varying similarity to search vector [1, 1, 1]
		// The closer the vector is to [1, 1, 1], the higher the similarity
		embedVectors := [][]float32{
			{0.7, 0.7, 0.7}, // post1: somewhat similar
			{0.9, 0.9, 0.9}, // post2: very similar
			{0.2, 0.2, 0.2}, // post3: not very similar
			{0.5, 0.5, 0.5}, // post4: moderately similar
		}

		ctx := context.Background()

		// Store the documents
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Search vector
		searchVector := []float32{1.0, 1.0, 1.0}

		return ctx, pgVector, db, createAts, searchVector
	}

	t.Run("basic search with limit", func(t *testing.T) {
		ctx, pgVector, db, _, searchVector := setupSearchTest(t)
		defer cleanupDB(t, db)

		// In the original test environment, we need permission filtering to work
		opts := embeddings.SearchOptions{
			Limit:  2,
			UserID: "system_user", // Use the system user that has access to all channels
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Len(t, results, 2)

		// Should return post2 and post1 in that order
		assert.Equal(t, "post2", results[0].Document.PostID)
		assert.Equal(t, "post1", results[1].Document.PostID)
	})

	t.Run("search with chunks", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		// Set up PGVector
		config := PGVectorConfig{
			Dimensions: 3, // Small dimensions for test
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// Create test data
		now := model.GetMillis()
		postIDs := []string{"post1"}
		createAts := []int64{now}
		addTestPosts(t, db, postIDs, createAts)

		// Create the channels needed for our tests
		channelIDs := []string{"channel1"}
		addTestChannels(t, db, channelIDs, false)

		// Add channel memberships for a test user that has access to all channels
		systemUserID := "system_user"
		addTestChannelMembers(t, db, "channel1", []string{systemUserID})

		docs := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "This is ",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  0,
					TotalChunks: 2,
				},
			},
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "the full content",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  1,
					TotalChunks: 2,
				},
			},
		}

		embedVectors := [][]float32{
			{0.9, 0.9, 0.9}, // post1_chunk_0 - most similar to search vector
			{0.5, 0.5, 0.5}, // post1_chunk_1
		}

		ctx := context.Background()

		// Store the documents
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Search vector - will match chunk0 closest
		searchVector := []float32{1.0, 1.0, 1.0}

		opts := embeddings.SearchOptions{
			Limit:  10,
			UserID: "system_user",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)

		// Should return all two documents
		assert.Len(t, results, 2)

		// The first result should be the chunk with highest similarity
		assert.Equal(t, "post1", results[0].Document.PostID)
		assert.Equal(t, "This is ", results[0].Document.Content)
		assert.True(t, results[0].Document.IsChunk)

		// Verify correct chunk metadata
		assert.Equal(t, 0, results[0].Document.ChunkIndex)
		assert.Equal(t, 2, results[0].Document.TotalChunks)
	})

	t.Run("search with team filter", func(t *testing.T) {
		ctx, pgVector, db, _, searchVector := setupSearchTest(t)
		defer cleanupDB(t, db)

		opts := embeddings.SearchOptions{
			TeamID: "team1",
			UserID: "system_user",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Len(t, results, 2)
		for _, result := range results {
			assert.Equal(t, "team1", result.Document.TeamID)
		}
	})

	t.Run("search with channel filter", func(t *testing.T) {
		ctx, pgVector, db, _, searchVector := setupSearchTest(t)
		defer cleanupDB(t, db)

		opts := embeddings.SearchOptions{
			ChannelID: "channel3",
			UserID:    "system_user",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, "post3", results[0].Document.PostID)
	})

	t.Run("search with min score filter", func(t *testing.T) {
		ctx, pgVector, db, _, searchVector := setupSearchTest(t)
		defer cleanupDB(t, db)

		// With correct L2-to-cosine-similarity conversion:
		// - post2 [0.9,0.9,0.9] vs [1,1,1]: L2 ≈ 0.173, score ≈ 0.985
		// - post1 [0.7,0.7,0.7] vs [1,1,1]: L2 ≈ 0.52, score ≈ 0.865
		// MinScore 0.9 should only match post2
		opts := embeddings.SearchOptions{
			MinScore: 0.9, // Only include very similar vectors
			UserID:   "system_user",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, "post2", results[0].Document.PostID)
	})

	t.Run("search with creation time filter", func(t *testing.T) {
		ctx, pgVector, db, createAts, searchVector := setupSearchTest(t)
		defer cleanupDB(t, db)

		opts := embeddings.SearchOptions{
			CreatedAfter: createAts[1], // After post2
			UserID:       "system_user",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Len(t, results, 2)
		// Should contain post3 and post4
		ids := []string{results[0].Document.PostID, results[1].Document.PostID}
		assert.Contains(t, ids, "post3")
		assert.Contains(t, ids, "post4")
	})

	t.Run("search with offset for pagination", func(t *testing.T) {
		ctx, pgVector, db, _, searchVector := setupSearchTest(t)
		defer cleanupDB(t, db)

		// First, get all results to establish the order
		allOpts := embeddings.SearchOptions{
			Limit:  10,
			UserID: "system_user",
		}
		allResults, err := pgVector.Search(ctx, searchVector, allOpts)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(allResults), 3)

		// Now get results with offset=2, should skip first 2
		offsetOpts := embeddings.SearchOptions{
			Limit:  10,
			Offset: 2,
			UserID: "system_user",
		}
		offsetResults, err := pgVector.Search(ctx, searchVector, offsetOpts)
		require.NoError(t, err)

		// First result with offset should equal third result without offset
		assert.Equal(t, allResults[2].Document.PostID, offsetResults[0].Document.PostID,
			"offset should skip first 2 results")
	})

	t.Run("offset beyond results returns empty", func(t *testing.T) {
		ctx, pgVector, db, _, searchVector := setupSearchTest(t)
		defer cleanupDB(t, db)

		opts := embeddings.SearchOptions{
			Limit:  10,
			Offset: 100, // Way beyond our 4 test posts
			UserID: "system_user",
		}
		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Empty(t, results, "offset beyond total should return empty")
	})

	t.Run("offset with limit for pagination", func(t *testing.T) {
		ctx, pgVector, db, _, searchVector := setupSearchTest(t)
		defer cleanupDB(t, db)

		// Get all results first
		allOpts := embeddings.SearchOptions{
			Limit:  10,
			UserID: "system_user",
		}
		allResults, err := pgVector.Search(ctx, searchVector, allOpts)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(allResults), 4)

		// Get first page (limit=2, offset=0)
		page1Opts := embeddings.SearchOptions{
			Limit:  2,
			Offset: 0,
			UserID: "system_user",
		}
		page1, err := pgVector.Search(ctx, searchVector, page1Opts)
		require.NoError(t, err)
		assert.Len(t, page1, 2)

		// Get second page (limit=2, offset=2)
		page2Opts := embeddings.SearchOptions{
			Limit:  2,
			Offset: 2,
			UserID: "system_user",
		}
		page2, err := pgVector.Search(ctx, searchVector, page2Opts)
		require.NoError(t, err)
		assert.Len(t, page2, 2)

		// Verify pages don't overlap
		page1IDs := map[string]bool{page1[0].Document.PostID: true, page1[1].Document.PostID: true}
		for _, result := range page2 {
			assert.False(t, page1IDs[result.Document.PostID], "page2 should not contain posts from page1")
		}

		// Verify page1 matches first 2 of allResults
		assert.Equal(t, allResults[0].Document.PostID, page1[0].Document.PostID)
		assert.Equal(t, allResults[1].Document.PostID, page1[1].Document.PostID)

		// Verify page2 matches next 2 of allResults
		assert.Equal(t, allResults[2].Document.PostID, page2[0].Document.PostID)
		assert.Equal(t, allResults[3].Document.PostID, page2[1].Document.PostID)
	})
}

func TestSearchWithPermissions(t *testing.T) {
	setupPermissionSearchTest := func(t *testing.T) (context.Context, *PGVector, *sqlx.DB, []float32) {
		db := testDB(t)

		// Set up PGVector
		config := PGVectorConfig{
			Dimensions: 3, // Small dimensions for test
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// Create test data
		now := model.GetMillis()

		// Create 6 posts across 5 channels
		postIDs := []string{"post1", "post2", "post3", "post4", "post5", "post6"}
		createAts := []int64{now, now, now, now, now, now}
		addTestPosts(t, db, postIDs, createAts)

		// Create channels
		channelIDs := []string{"channel1", "channel2", "channel3", "channel4", "channel5"}
		addTestChannels(t, db, channelIDs, false)

		// Channel5 is deleted
		_, err = db.Exec("UPDATE Channels SET DeleteAt = $1 WHERE Id = $2", now, "channel5")
		require.NoError(t, err)

		// Create channel memberships
		// user1 is a member of channels 1, 2, and 5 (deleted)
		addTestChannelMembers(t, db, "channel1", []string{"user1"})
		addTestChannelMembers(t, db, "channel2", []string{"user1"})
		addTestChannelMembers(t, db, "channel5", []string{"user1"})

		// user2 is a member of channels 3 and 4
		addTestChannelMembers(t, db, "channel3", []string{"user2"})
		addTestChannelMembers(t, db, "channel4", []string{"user2"})

		// user3 is a member of channels 1 and 3
		addTestChannelMembers(t, db, "channel1", []string{"user3"})
		addTestChannelMembers(t, db, "channel3", []string{"user3"})

		docs := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1", // Both user1 and user3 can access
				UserID:    "user1",
				Content:   "Content in channel 1",
			},
			{
				PostID:    "post2",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel2", // Only user1 can access
				UserID:    "user2",
				Content:   "Content in channel 2",
			},
			{
				PostID:    "post3",
				CreateAt:  now,
				TeamID:    "team2",
				ChannelID: "channel3", // Both user2 and user3 can access
				UserID:    "user3",
				Content:   "Content in channel 3",
			},
			{
				PostID:    "post4",
				CreateAt:  now,
				TeamID:    "team2",
				ChannelID: "channel4", // Only user2 can access
				UserID:    "user2",
				Content:   "Content in channel 4",
			},
			{
				PostID:    "post5",
				CreateAt:  now,
				TeamID:    "team3",
				ChannelID: "channel4", // Only user2 can access - different team
				UserID:    "user2",
				Content:   "Content in channel 4 team 3",
			},
			{
				PostID:    "post6",
				CreateAt:  now,
				TeamID:    "team3",
				ChannelID: "channel5", // Deleted channel
				UserID:    "user1",
				Content:   "Content in deleted channel 5",
			},
		}

		// Use identical vectors for simplicity in permission tests
		embedVectors := [][]float32{
			{0.5, 0.5, 0.5}, // post1
			{0.5, 0.5, 0.5}, // post2
			{0.5, 0.5, 0.5}, // post3
			{0.5, 0.5, 0.5}, // post4
			{0.5, 0.5, 0.5}, // post5
			{0.5, 0.5, 0.5}, // post6
		}

		ctx := context.Background()

		// Store the documents
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Search vector - exact match for simplicity
		searchVector := []float32{0.5, 0.5, 0.5}

		return ctx, pgVector, db, searchVector
	}

	t.Run("search without user ID fails", func(t *testing.T) {
		ctx, pgVector, db, searchVector := setupPermissionSearchTest(t)
		defer cleanupDB(t, db)

		opts := embeddings.SearchOptions{
			Limit: 10,
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.Error(t, err)
		assert.Len(t, results, 0, "Should return no posts when not specifying a user")
	})

	t.Run("search with user ID only returns posts from channels the user is a member of", func(t *testing.T) {
		ctx, pgVector, db, searchVector := setupPermissionSearchTest(t)
		defer cleanupDB(t, db)

		// Search as user1
		opts := embeddings.SearchOptions{
			Limit:  10,
			UserID: "user1",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Len(t, results, 2, "Should return only posts from channels user1 is a member of")

		// Verify we get the expected posts
		postIDs := []string{}
		for _, result := range results {
			postIDs = append(postIDs, result.Document.PostID)
		}
		assert.Contains(t, postIDs, "post1", "Should contain post1 (channel1)")
		assert.Contains(t, postIDs, "post2", "Should contain post2 (channel2)")
		assert.NotContains(t, postIDs, "post6", "Should not contain post6 (deleted channel5)")
	})

	t.Run("search with user ID and team filter", func(t *testing.T) {
		ctx, pgVector, db, searchVector := setupPermissionSearchTest(t)
		defer cleanupDB(t, db)

		// Search as user2 and filter by team2
		opts := embeddings.SearchOptions{
			Limit:  10,
			UserID: "user2",
			TeamID: "team2",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Len(t, results, 2, "Should return posts from channels user2 is a member of in team2")

		// Verify we only get posts from team2
		postIDs := []string{}
		for _, result := range results {
			postIDs = append(postIDs, result.Document.PostID)
			assert.Equal(t, "team2", result.Document.TeamID)
		}
		assert.Contains(t, postIDs, "post3", "Should contain post3 (channel3)")
		assert.Contains(t, postIDs, "post4", "Should contain post4 (channel4)")
		assert.NotContains(t, postIDs, "post5", "Should not contain post5 (channel4, but team3)")
	})

	t.Run("search with user ID and channel filter", func(t *testing.T) {
		ctx, pgVector, db, searchVector := setupPermissionSearchTest(t)
		defer cleanupDB(t, db)

		// Search as user3, who has access to channels 1 and 3, but filter to just channel3
		opts := embeddings.SearchOptions{
			Limit:     10,
			UserID:    "user3",
			ChannelID: "channel3",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Len(t, results, 1, "Should return only the post from channel3")
		assert.Equal(t, "post3", results[0].Document.PostID)
	})

	t.Run("search with multiple users having access to the same channel", func(t *testing.T) {
		ctx, pgVector, db, searchVector := setupPermissionSearchTest(t)
		defer cleanupDB(t, db)

		// Test that both user2 and user3 can access post3 in channel3
		opts1 := embeddings.SearchOptions{
			Limit:     10,
			UserID:    "user2",
			ChannelID: "channel3",
		}

		results1, err := pgVector.Search(ctx, searchVector, opts1)
		require.NoError(t, err)
		assert.Len(t, results1, 1, "user2 should be able to access post3")
		assert.Equal(t, "post3", results1[0].Document.PostID)

		opts2 := embeddings.SearchOptions{
			Limit:     10,
			UserID:    "user3",
			ChannelID: "channel3",
		}

		results2, err := pgVector.Search(ctx, searchVector, opts2)
		require.NoError(t, err)
		assert.Len(t, results2, 1, "user3 should be able to access post3")
		assert.Equal(t, "post3", results2[0].Document.PostID)
	})

	t.Run("deleted channels are excluded even if user is a member", func(t *testing.T) {
		ctx, pgVector, db, searchVector := setupPermissionSearchTest(t)
		defer cleanupDB(t, db)

		// user1 is a member of channel5 (deleted)
		opts := embeddings.SearchOptions{
			Limit:  10,
			UserID: "user1",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)

		// Should not include post6 from deleted channel5
		for _, result := range results {
			assert.NotEqual(t, "post6", result.Document.PostID, "Should not include posts from deleted channels")
		}
	})

	t.Run("deleted posts are excluded", func(t *testing.T) {
		ctx, pgVector, db, searchVector := setupPermissionSearchTest(t)
		defer cleanupDB(t, db)

		// Mark post1 as deleted
		now := model.GetMillis()
		_, err := db.Exec("UPDATE Posts SET DeleteAt = $1 WHERE Id = $2", now, "post1")
		require.NoError(t, err)

		// Search as user1 (should have access to channel1 and channel2)
		opts := embeddings.SearchOptions{
			Limit:  10,
			UserID: "user1",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)

		// Should not include deleted post1
		for _, result := range results {
			assert.NotEqual(t, "post1", result.Document.PostID, "Should not include deleted post1")
		}

		// Should still include post2 (user1 has access to channel2)
		found := false
		for _, result := range results {
			if result.Document.PostID == "post2" {
				found = true
				break
			}
		}
		assert.True(t, found, "Should still include non-deleted post2")
	})
}

func TestDeleteWithChunks(t *testing.T) {
	t.Run("deletes both posts and their chunks", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		// Set up PGVector
		config := PGVectorConfig{
			Dimensions: 3, // Small dimensions for test
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// Create test data
		now := model.GetMillis()
		postIDs := []string{"post1", "post2"}
		createAts := []int64{now, now}
		addTestPosts(t, db, postIDs, createAts)

		docs := []embeddings.PostDocument{
			// Post 1 and chunks
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Content 1",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  0,
					TotalChunks: 3,
				},
			},
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Chunk 1.1",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  1,
					TotalChunks: 3,
				},
			},
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Chunk 1.2",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  2,
					TotalChunks: 3,
				},
			},
			// Post 2 and chunks
			{
				PostID:    "post2",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Content 2",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  0,
					TotalChunks: 2,
				},
			},
			{
				PostID:    "post2",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Chunk 2.1",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  1,
					TotalChunks: 2,
				},
			},
		}

		embedVectors := [][]float32{
			{0.1, 0.2, 0.3},
			{0.4, 0.5, 0.6},
			{0.7, 0.8, 0.9},
			{0.1, 0.2, 0.3},
			{0.4, 0.5, 0.6},
		}

		ctx := context.Background()

		// Store the documents
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Verify initial count
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 5, count)

		// Delete post1 and its chunks
		err = pgVector.Delete(ctx, []string{"post1"})
		require.NoError(t, err)

		// Verify post1 and its chunks are gone, but post2 remains
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 2, count, "Should have only post2 and its chunk remaining")

		// Verify the remaining documents are post2 and its chunk
		var remainingIDs []string
		err = db.Select(&remainingIDs, "SELECT id FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Contains(t, remainingIDs, "post2_chunk_0")
		assert.Contains(t, remainingIDs, "post2_chunk_1")
	})
}

func TestClear(t *testing.T) {
	t.Run("successfully clears all documents", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		// Set up PGVector
		config := PGVectorConfig{
			Dimensions: 3, // Small dimensions for test
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// Create test data
		now := model.GetMillis()
		postIDs := []string{"post1", "post2"}
		createAts := []int64{now, now}
		addTestPosts(t, db, postIDs, createAts)

		docs := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Content 1",
			},
			{
				PostID:    "post2",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Content 2",
			},
		}

		embedVectors := [][]float32{
			{0.1, 0.2, 0.3},
			{0.4, 0.5, 0.6},
		}

		ctx := context.Background()

		// Store the documents
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Verify 2 documents were stored
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 2, count)

		// Clear all documents
		err = pgVector.Clear(ctx)
		require.NoError(t, err)

		// Verify no documents remain
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

func TestSearchExcludesDeletedPosts(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	// Set up PGVector
	config := PGVectorConfig{
		Dimensions: 3, // Small dimensions for test
	}
	pgVector, err := NewPGVector(db, config)
	require.NoError(t, err)

	// Create test data
	now := model.GetMillis()

	// Create 3 active posts and 2 deleted posts
	activePostIDs := []string{"active1", "active2", "active3"}
	deletedPostIDs := []string{"deleted1", "deleted2"}
	createAts := []int64{now, now, now, now, now}

	// Add active posts
	addTestPosts(t, db, activePostIDs, createAts[:3])

	// Add deleted posts
	addTestDeletedPosts(t, db, deletedPostIDs, createAts[3:], []int64{now, now})

	// Create test channel and user
	addTestChannels(t, db, []string{"channel1"}, false)
	addTestChannelMembers(t, db, "channel1", []string{"user1"})

	// Create documents for all posts (including deleted ones)
	docs := []embeddings.PostDocument{
		{
			PostID:    "active1",
			CreateAt:  now,
			TeamID:    "team1",
			ChannelID: "channel1",
			UserID:    "user1",
			Content:   "Active post 1",
		},
		{
			PostID:    "active2",
			CreateAt:  now,
			TeamID:    "team1",
			ChannelID: "channel1",
			UserID:    "user1",
			Content:   "Active post 2",
		},
		{
			PostID:    "active3",
			CreateAt:  now,
			TeamID:    "team1",
			ChannelID: "channel1",
			UserID:    "user1",
			Content:   "Active post 3",
		},
		{
			PostID:    "deleted1",
			CreateAt:  now,
			TeamID:    "team1",
			ChannelID: "channel1",
			UserID:    "user1",
			Content:   "Deleted post 1",
		},
		{
			PostID:    "deleted2",
			CreateAt:  now,
			TeamID:    "team1",
			ChannelID: "channel1",
			UserID:    "user1",
			Content:   "Deleted post 2",
		},
	}

	// Use similar vectors for all posts
	embedVectors := [][]float32{
		{0.5, 0.5, 0.5}, // active1
		{0.5, 0.5, 0.5}, // active2
		{0.5, 0.5, 0.5}, // active3
		{0.5, 0.5, 0.5}, // deleted1
		{0.5, 0.5, 0.5}, // deleted2
	}

	ctx := context.Background()

	// Store all documents (including those for deleted posts)
	err = pgVector.Store(ctx, docs, embedVectors)
	require.NoError(t, err)

	// Verify all 5 documents were stored in the embeddings table
	var totalCount int
	err = db.Get(&totalCount, "SELECT COUNT(*) FROM llm_posts_embeddings")
	require.NoError(t, err)
	assert.Equal(t, 5, totalCount, "All 5 embeddings should be stored")

	// Perform search - should only return active posts
	searchVector := []float32{0.5, 0.5, 0.5}
	opts := embeddings.SearchOptions{
		UserID: "user1",
		Limit:  10,
	}

	results, err := pgVector.Search(ctx, searchVector, opts)
	require.NoError(t, err)

	// Should only return 3 results (active posts only)
	assert.Equal(t, 3, len(results), "Should only return active posts, not deleted ones")

	// Verify only active posts are returned
	returnedPostIDs := make(map[string]bool)
	for _, result := range results {
		returnedPostIDs[result.Document.PostID] = true
	}

	// Check that all active posts are present
	for _, postID := range activePostIDs {
		assert.True(t, returnedPostIDs[postID], "Active post %s should be returned", postID)
	}

	// Check that no deleted posts are present
	for _, postID := range deletedPostIDs {
		assert.False(t, returnedPostIDs[postID], "Deleted post %s should NOT be returned", postID)
	}
}

func TestNewPGVectorValidation(t *testing.T) {
	tests := []struct {
		name       string
		dimensions int
		wantErr    bool
		errMsg     string
	}{
		{
			name:       "zero dimensions should error",
			dimensions: 0,
			wantErr:    true,
			errMsg:     "pgvector dimensions must be greater than 0, got 0",
		},
		{
			name:       "negative dimensions should error",
			dimensions: -10,
			wantErr:    true,
			errMsg:     "pgvector dimensions must be greater than 0, got -10",
		},
		{
			name:       "positive dimensions should succeed",
			dimensions: 128,
			wantErr:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			defer cleanupDB(t, db)

			config := PGVectorConfig{
				Dimensions: tc.dimensions,
			}

			pgVector, err := NewPGVector(db, config)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)
				assert.Nil(t, pgVector)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, pgVector)
			}
		})
	}
}

func TestStoreValidation(t *testing.T) {
	t.Run("mismatched docs and embeddings length returns error", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// Create test data
		now := model.GetMillis()
		postIDs := []string{"post1", "post2"}
		createAts := []int64{now, now}
		addTestPosts(t, db, postIDs, createAts)

		docs := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Content 1",
			},
			{
				PostID:    "post2",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Content 2",
			},
		}

		// Only provide one embedding for two docs - should return error
		embedVectors := [][]float32{
			{0.1, 0.2, 0.3},
		}

		ctx := context.Background()

		// Store should return an error when docs and embeddings lengths mismatch
		err = pgVector.Store(ctx, docs, embedVectors)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mismatched input lengths")
		assert.Contains(t, err.Error(), "2 documents")
		assert.Contains(t, err.Error(), "1 embeddings")
	})

	t.Run("unicode and special characters in content", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		now := model.GetMillis()
		postIDs := []string{"post_unicode"}
		createAts := []int64{now}
		addTestPosts(t, db, postIDs, createAts)

		// Test with various unicode characters, emojis, and special characters
		unicodeContent := "Hello 世界! 🎉🚀 Héllo Wörld! \n\t Special chars: <>\"'&;-- SQL injection test'; DROP TABLE--"

		docs := []embeddings.PostDocument{
			{
				PostID:    "post_unicode",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   unicodeContent,
			},
		}

		embedVectors := [][]float32{
			{0.1, 0.2, 0.3},
		}

		ctx := context.Background()

		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Verify the content was stored correctly
		var storedContent string
		err = db.Get(&storedContent, "SELECT content FROM llm_posts_embeddings WHERE id = 'post_unicode'")
		require.NoError(t, err)
		assert.Equal(t, unicodeContent, storedContent)
	})

	t.Run("reindexing post that changed from single to chunked", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		now := model.GetMillis()
		postIDs := []string{"post_reindex"}
		createAts := []int64{now}
		addTestPosts(t, db, postIDs, createAts)

		ctx := context.Background()

		// First, store as single document
		singleDoc := []embeddings.PostDocument{
			{
				PostID:    "post_reindex",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Short content",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     false,
					ChunkIndex:  0,
					TotalChunks: 1,
				},
			},
		}
		singleEmbed := [][]float32{{0.1, 0.2, 0.3}}

		err = pgVector.Store(ctx, singleDoc, singleEmbed)
		require.NoError(t, err)

		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings WHERE post_id = 'post_reindex'")
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		// Now reindex as multiple chunks
		chunkedDocs := []embeddings.PostDocument{
			{
				PostID:    "post_reindex",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Chunk 0",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  0,
					TotalChunks: 3,
				},
			},
			{
				PostID:    "post_reindex",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Chunk 1",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  1,
					TotalChunks: 3,
				},
			},
			{
				PostID:    "post_reindex",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Chunk 2",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  2,
					TotalChunks: 3,
				},
			},
		}
		chunkedEmbeds := [][]float32{
			{0.1, 0.2, 0.3},
			{0.4, 0.5, 0.6},
			{0.7, 0.8, 0.9},
		}

		err = pgVector.Store(ctx, chunkedDocs, chunkedEmbeds)
		require.NoError(t, err)

		// Verify old single entry was removed and new chunks exist
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings WHERE post_id = 'post_reindex'")
		require.NoError(t, err)
		assert.Equal(t, 3, count, "Should have 3 chunks after reindex")

		// Verify all are chunks
		var nonChunkCount int
		err = db.Get(&nonChunkCount, "SELECT COUNT(*) FROM llm_posts_embeddings WHERE post_id = 'post_reindex' AND is_chunk = false")
		require.NoError(t, err)
		assert.Equal(t, 0, nonChunkCount, "Original single document should have been removed")
	})

	t.Run("reindexing post that changed from chunked to single", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		now := model.GetMillis()
		postIDs := []string{"post_unchunk"}
		createAts := []int64{now}
		addTestPosts(t, db, postIDs, createAts)

		ctx := context.Background()

		// First, store as multiple chunks
		chunkedDocs := []embeddings.PostDocument{
			{
				PostID:    "post_unchunk",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Chunk 0",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  0,
					TotalChunks: 2,
				},
			},
			{
				PostID:    "post_unchunk",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Chunk 1",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     true,
					ChunkIndex:  1,
					TotalChunks: 2,
				},
			},
		}
		chunkedEmbeds := [][]float32{
			{0.1, 0.2, 0.3},
			{0.4, 0.5, 0.6},
		}

		err = pgVector.Store(ctx, chunkedDocs, chunkedEmbeds)
		require.NoError(t, err)

		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings WHERE post_id = 'post_unchunk'")
		require.NoError(t, err)
		assert.Equal(t, 2, count)

		// Now reindex as single document (content edited to be shorter)
		singleDoc := []embeddings.PostDocument{
			{
				PostID:    "post_unchunk",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Short content now",
				ChunkInfo: chunking.ChunkInfo{
					IsChunk:     false,
					ChunkIndex:  0,
					TotalChunks: 1,
				},
			},
		}
		singleEmbed := [][]float32{{0.9, 0.9, 0.9}}

		err = pgVector.Store(ctx, singleDoc, singleEmbed)
		require.NoError(t, err)

		// Verify old chunks were removed and new single entry exists
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings WHERE post_id = 'post_unchunk'")
		require.NoError(t, err)
		assert.Equal(t, 1, count, "Should have 1 document after reindex")

		// Verify it's not a chunk
		var isChunk bool
		err = db.Get(&isChunk, "SELECT is_chunk FROM llm_posts_embeddings WHERE post_id = 'post_unchunk'")
		require.NoError(t, err)
		assert.False(t, isChunk, "Should not be a chunk")
	})

	t.Run("very large document content", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		now := model.GetMillis()
		postIDs := []string{"post_large"}
		createAts := []int64{now}
		addTestPosts(t, db, postIDs, createAts)

		// Create a large document (1MB of content)
		largeContent := make([]byte, 1024*1024)
		for i := range largeContent {
			largeContent[i] = byte('A' + (i % 26))
		}

		docs := []embeddings.PostDocument{
			{
				PostID:    "post_large",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   string(largeContent),
			},
		}

		embedVectors := [][]float32{
			{0.1, 0.2, 0.3},
		}

		ctx := context.Background()

		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Verify the content was stored
		var storedLen int
		err = db.Get(&storedLen, "SELECT LENGTH(content) FROM llm_posts_embeddings WHERE id = 'post_large'")
		require.NoError(t, err)
		assert.Equal(t, len(largeContent), storedLen)
	})
}

func TestSearchValidation(t *testing.T) {
	setupSearchValidationTest := func(t *testing.T) (context.Context, *PGVector, *sqlx.DB, []int64) {
		db := testDB(t)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		now := model.GetMillis()
		postIDs := []string{"post1", "post2", "post3", "post4", "post5"}
		createAts := []int64{now - 4000, now - 3000, now - 2000, now - 1000, now}
		addTestPosts(t, db, postIDs, createAts)

		channelIDs := []string{"channel1"}
		addTestChannels(t, db, channelIDs, false)
		addTestChannelMembers(t, db, "channel1", []string{"user1"})

		docs := []embeddings.PostDocument{
			{PostID: "post1", CreateAt: createAts[0], TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "Content 1"},
			{PostID: "post2", CreateAt: createAts[1], TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "Content 2"},
			{PostID: "post3", CreateAt: createAts[2], TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: "Content 3"},
			{PostID: "post4", CreateAt: createAts[3], TeamID: "team2", ChannelID: "channel1", UserID: "user1", Content: "Content 4"},
			{PostID: "post5", CreateAt: createAts[4], TeamID: "team2", ChannelID: "channel1", UserID: "user1", Content: "Content 5"},
		}

		embedVectors := [][]float32{
			{0.1, 0.1, 0.1},
			{0.3, 0.3, 0.3},
			{0.5, 0.5, 0.5},
			{0.7, 0.7, 0.7},
			{0.9, 0.9, 0.9},
		}

		ctx := context.Background()
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		return ctx, pgVector, db, createAts
	}

	t.Run("limit zero uses maxSearchLimit default", func(t *testing.T) {
		ctx, pgVector, db, _ := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		searchVector := []float32{0.5, 0.5, 0.5}
		opts := embeddings.SearchOptions{
			Limit:  0,
			UserID: "user1",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		// With limit 0, all 5 posts should be returned (default to maxSearchLimit)
		assert.Len(t, results, 5)
	})

	t.Run("negative limit uses maxSearchLimit default", func(t *testing.T) {
		ctx, pgVector, db, _ := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		searchVector := []float32{0.5, 0.5, 0.5}
		opts := embeddings.SearchOptions{
			Limit:  -10,
			UserID: "user1",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		// With negative limit, all 5 posts should be returned (default to maxSearchLimit)
		assert.Len(t, results, 5)
	})

	t.Run("combined filters: team + channel + time range + min score", func(t *testing.T) {
		ctx, pgVector, db, createAts := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		searchVector := []float32{0.9, 0.9, 0.9}
		opts := embeddings.SearchOptions{
			Limit:         10,
			UserID:        "user1",
			TeamID:        "team2",
			ChannelID:     "channel1",
			CreatedAfter:  createAts[2], // After post3
			CreatedBefore: createAts[4], // Before post5
			MinScore:      0.5,
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		// Should only return post4 (team2, in time range, meets min score)
		assert.Len(t, results, 1)
		if len(results) > 0 {
			assert.Equal(t, "post4", results[0].Document.PostID)
		}
	})

	t.Run("CreatedBefore filter alone", func(t *testing.T) {
		ctx, pgVector, db, createAts := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		searchVector := []float32{0.5, 0.5, 0.5}
		opts := embeddings.SearchOptions{
			Limit:         10,
			UserID:        "user1",
			CreatedBefore: createAts[2], // Before post3
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		// Should return post1, post2 (created before createAts[2])
		assert.Len(t, results, 2)
		for _, result := range results {
			assert.True(t, result.Document.CreateAt < createAts[2])
		}
	})

	t.Run("CreatedAfter AND CreatedBefore together (time range query)", func(t *testing.T) {
		ctx, pgVector, db, createAts := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		searchVector := []float32{0.5, 0.5, 0.5}
		opts := embeddings.SearchOptions{
			Limit:         10,
			UserID:        "user1",
			CreatedAfter:  createAts[1], // After post2
			CreatedBefore: createAts[4], // Before post5
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		// Should return post3, post4 (between createAts[1] and createAts[4])
		assert.Len(t, results, 2)
		for _, result := range results {
			assert.True(t, result.Document.CreateAt > createAts[1])
			assert.True(t, result.Document.CreateAt < createAts[4])
		}
	})

	t.Run("zero MinScore value", func(t *testing.T) {
		ctx, pgVector, db, _ := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		searchVector := []float32{0.5, 0.5, 0.5}
		opts := embeddings.SearchOptions{
			Limit:    10,
			UserID:   "user1",
			MinScore: 0.0,
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		// MinScore 0 should not filter anything
		assert.Len(t, results, 5)
	})

	t.Run("negative MinScore value", func(t *testing.T) {
		ctx, pgVector, db, _ := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		searchVector := []float32{0.5, 0.5, 0.5}
		opts := embeddings.SearchOptions{
			Limit:    10,
			UserID:   "user1",
			MinScore: -0.5,
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		// Negative MinScore should not filter (condition is opts.MinScore > 0)
		assert.Len(t, results, 5)
	})

	t.Run("very high MinScore value greater than 1.0", func(t *testing.T) {
		ctx, pgVector, db, _ := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		searchVector := []float32{0.5, 0.5, 0.5}
		opts := embeddings.SearchOptions{
			Limit:    10,
			UserID:   "user1",
			MinScore: 1.5, // Score > 1.0 (impossible for normalized scores)
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		// With MinScore > 1.0, maxDistance = 1 - 1.5 = -0.5, so SQL filter should exclude everything
		// In the scanSearchResults, score < minScore check will also filter out results
		assert.Len(t, results, 0, "No results should have score > 1.0")
	})

	t.Run("empty embedding vector passed to search", func(t *testing.T) {
		ctx, pgVector, db, _ := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		emptyVector := []float32{}
		opts := embeddings.SearchOptions{
			Limit:  10,
			UserID: "user1",
		}

		_, err := pgVector.Search(ctx, emptyVector, opts)
		// pgvector should error on mismatched dimensions
		require.Error(t, err)
	})

	t.Run("malformed embedding vector (wrong dimensions)", func(t *testing.T) {
		ctx, pgVector, db, _ := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		// Table was created with 3 dimensions, search with 5
		wrongDimVector := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
		opts := embeddings.SearchOptions{
			Limit:  10,
			UserID: "user1",
		}

		_, err := pgVector.Search(ctx, wrongDimVector, opts)
		// pgvector should error on mismatched dimensions
		require.Error(t, err)
	})

	t.Run("user who is member of zero channels", func(t *testing.T) {
		ctx, pgVector, db, _ := setupSearchValidationTest(t)
		defer cleanupDB(t, db)

		searchVector := []float32{0.5, 0.5, 0.5}
		opts := embeddings.SearchOptions{
			Limit:  10,
			UserID: "user_no_channels", // This user has no channel memberships
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		// User with no channel memberships should see no results
		assert.Len(t, results, 0)
	})
}

func TestSearchLargeResultSets(t *testing.T) {
	t.Run("large result set approaching maxSearchLimit", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		now := model.GetMillis()
		numPosts := 100 // Create 100 posts to test with

		postIDs := make([]string, numPosts)
		createAts := make([]int64, numPosts)
		for i := 0; i < numPosts; i++ {
			postIDs[i] = fmt.Sprintf("post_%d", i)
			createAts[i] = now + int64(i)
		}
		addTestPosts(t, db, postIDs, createAts)

		channelIDs := []string{"channel1"}
		addTestChannels(t, db, channelIDs, false)
		addTestChannelMembers(t, db, "channel1", []string{"user1"})

		docs := make([]embeddings.PostDocument, numPosts)
		embedVectors := make([][]float32, numPosts)
		for i := 0; i < numPosts; i++ {
			docs[i] = embeddings.PostDocument{
				PostID:    postIDs[i],
				CreateAt:  createAts[i],
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   fmt.Sprintf("Content for post %d", i),
			}
			embedVectors[i] = []float32{0.5, 0.5, 0.5}
		}

		ctx := context.Background()
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Search with limit less than total posts
		searchVector := []float32{0.5, 0.5, 0.5}
		opts := embeddings.SearchOptions{
			Limit:  50,
			UserID: "user1",
		}

		results, err := pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Len(t, results, 50)

		// Search with limit greater than total posts (should return all)
		opts.Limit = 200
		results, err = pgVector.Search(ctx, searchVector, opts)
		require.NoError(t, err)
		assert.Len(t, results, numPosts)
	})
}

func TestDeleteValidation(t *testing.T) {
	t.Run("delete with empty postIDs slice succeeds without deleting anything", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// First add some data
		now := model.GetMillis()
		postIDs := []string{"post1"}
		createAts := []int64{now}
		addTestPosts(t, db, postIDs, createAts)

		docs := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Content 1",
			},
		}
		embedVectors := [][]float32{{0.1, 0.2, 0.3}}

		ctx := context.Background()
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Verify data exists
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		// Delete with empty slice - squirrel generates WHERE (1=0) which deletes nothing
		err = pgVector.Delete(ctx, []string{})
		require.NoError(t, err, "Delete with empty slice should succeed")

		// Data should remain unchanged
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 1, count, "No data should be deleted when postIDs is empty")
	})

	t.Run("delete non-existent post IDs should succeed silently", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		// First add some data
		now := model.GetMillis()
		postIDs := []string{"post1"}
		createAts := []int64{now}
		addTestPosts(t, db, postIDs, createAts)

		docs := []embeddings.PostDocument{
			{
				PostID:    "post1",
				CreateAt:  now,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "Content 1",
			},
		}
		embedVectors := [][]float32{{0.1, 0.2, 0.3}}

		ctx := context.Background()
		err = pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// Delete non-existent IDs
		err = pgVector.Delete(ctx, []string{"nonexistent1", "nonexistent2"})
		require.NoError(t, err, "Deleting non-existent posts should succeed silently")

		// Verify existing data is unchanged
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

func TestConcurrentStoreOperations(t *testing.T) {
	t.Run("concurrent store operations on same post_id", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		now := model.GetMillis()
		postIDs := []string{"concurrent_post"}
		createAts := []int64{now}
		addTestPosts(t, db, postIDs, createAts)

		ctx := context.Background()

		// Run multiple goroutines trying to store to the same post
		numGoroutines := 10
		errChan := make(chan error, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func(idx int) {
				docs := []embeddings.PostDocument{
					{
						PostID:    "concurrent_post",
						CreateAt:  now,
						TeamID:    "team1",
						ChannelID: "channel1",
						UserID:    "user1",
						Content:   fmt.Sprintf("Content from goroutine %d", idx),
					},
				}
				embedVectors := [][]float32{
					{float32(idx) * 0.1, float32(idx) * 0.2, float32(idx) * 0.3},
				}
				errChan <- pgVector.Store(ctx, docs, embedVectors)
			}(i)
		}

		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			// Just drain the channel - we don't need to track errors for this test
			<-errChan
		}

		// Some operations may fail due to race conditions, but the system should remain consistent
		// At minimum, there should be exactly one document for the post
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings WHERE post_id = 'concurrent_post'")
		require.NoError(t, err)
		assert.Equal(t, 1, count, "Should have exactly one document for the post after concurrent operations")
	})
}

func TestDeleteOrphaned(t *testing.T) {
	setupDeleteOrphanedTest := func(t *testing.T) (context.Context, *PGVector, *sqlx.DB) {
		db := testDB(t)

		config := PGVectorConfig{
			Dimensions: 3,
		}
		pgVector, err := NewPGVector(db, config)
		require.NoError(t, err)

		return context.Background(), pgVector, db
	}

	t.Run("deletes embeddings for soft-deleted posts past retention", func(t *testing.T) {
		ctx, pgVector, db := setupDeleteOrphanedTest(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()

		// Create posts: A (active), B (soft-deleted at 1000), C (soft-deleted at 2000)
		addTestPosts(t, db, []string{"postA"}, []int64{now})
		addTestDeletedPosts(t, db, []string{"postB", "postC"}, []int64{now, now}, []int64{1000, 2000})

		docs := []embeddings.PostDocument{
			{PostID: "postA", CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "Active post"},
			{PostID: "postB", CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "Deleted early"},
			{PostID: "postC", CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "Deleted later"},
		}
		embedVectors := [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}}

		err := pgVector.Store(ctx, docs, embedVectors)
		require.NoError(t, err)

		// nowTime=1500 means only postB (DeleteAt=1000) is past retention
		deleted, err := pgVector.DeleteOrphaned(ctx, 1500, 100)
		require.NoError(t, err)
		assert.Equal(t, int64(1), deleted)

		// Verify postA and postC remain
		var remaining []string
		err = db.Select(&remaining, "SELECT post_id FROM llm_posts_embeddings ORDER BY post_id")
		require.NoError(t, err)
		assert.Equal(t, []string{"postA", "postC"}, remaining)
	})

	t.Run("respects batchSize limit", func(t *testing.T) {
		ctx, pgVector, db := setupDeleteOrphanedTest(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()

		// Create 5 soft-deleted posts all past retention
		postIDs := []string{"p1", "p2", "p3", "p4", "p5"}
		createAts := []int64{now, now, now, now, now}
		deleteAts := []int64{100, 100, 100, 100, 100}
		addTestDeletedPosts(t, db, postIDs, createAts, deleteAts)

		docs := make([]embeddings.PostDocument, 5)
		vecs := make([][]float32, 5)
		for i, id := range postIDs {
			docs[i] = embeddings.PostDocument{PostID: id, CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "content"}
			vecs[i] = []float32{0.1, 0.2, 0.3}
		}

		err := pgVector.Store(ctx, docs, vecs)
		require.NoError(t, err)

		// Only delete 2 at a time
		deleted, err := pgVector.DeleteOrphaned(ctx, 200, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(2), deleted)

		// 3 should remain
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 3, count)
	})

	t.Run("returns zero when no orphaned embeddings", func(t *testing.T) {
		ctx, pgVector, db := setupDeleteOrphanedTest(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		addTestPosts(t, db, []string{"active1", "active2"}, []int64{now, now})

		docs := []embeddings.PostDocument{
			{PostID: "active1", CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "content 1"},
			{PostID: "active2", CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "content 2"},
		}
		vecs := [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}}

		err := pgVector.Store(ctx, docs, vecs)
		require.NoError(t, err)

		deleted, err := pgVector.DeleteOrphaned(ctx, now+1000, 100)
		require.NoError(t, err)
		assert.Equal(t, int64(0), deleted)

		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 2, count)
	})

	t.Run("deletes all chunks for orphaned posts", func(t *testing.T) {
		ctx, pgVector, db := setupDeleteOrphanedTest(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		addTestDeletedPosts(t, db, []string{"chunked_post"}, []int64{now}, []int64{500})

		docs := []embeddings.PostDocument{
			{PostID: "chunked_post", CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "chunk 0",
				ChunkInfo: chunking.ChunkInfo{IsChunk: true, ChunkIndex: 0, TotalChunks: 3}},
			{PostID: "chunked_post", CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "chunk 1",
				ChunkInfo: chunking.ChunkInfo{IsChunk: true, ChunkIndex: 1, TotalChunks: 3}},
			{PostID: "chunked_post", CreateAt: now, TeamID: "team1", ChannelID: "ch1", UserID: "user1", Content: "chunk 2",
				ChunkInfo: chunking.ChunkInfo{IsChunk: true, ChunkIndex: 2, TotalChunks: 3}},
		}
		vecs := [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}}

		err := pgVector.Store(ctx, docs, vecs)
		require.NoError(t, err)

		// Verify 3 chunks stored
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 3, count)

		// Delete orphaned - all 3 chunks should be removed
		deleted, err := pgVector.DeleteOrphaned(ctx, 1000, 100)
		require.NoError(t, err)
		assert.Equal(t, int64(3), deleted)

		err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

func TestSearchScoreCalculation(t *testing.T) {
	// This test verifies that similarity scores are calculated correctly from L2 distance.
	// For normalized vectors (unit length), L2 distance relates to cosine similarity:
	// L2² = 2(1 - cos(θ)), so cos(θ) = 1 - L2²/2
	//
	// L2 distance ranges from 0 (identical) to 2 (opposite) for unit vectors.
	// Expected scores:
	// - L2 = 0 → score = 1 (identical)
	// - L2 = 1 → score = 0.5 (60° angle)
	// - L2 = sqrt(2) ≈ 1.414 → score = 0 (orthogonal, 90° angle)
	// - L2 = 2 → score = -1 → clamped to 0 (opposite)

	db := testDB(t)
	defer cleanupDB(t, db)

	pgVectorConfig := PGVectorConfig{Dimensions: 3}
	pgVector, err := NewPGVector(db, pgVectorConfig)
	require.NoError(t, err)

	// Set up channel and membership
	addTestChannels(t, db, []string{"channel1"}, false)
	addTestChannelMembers(t, db, "channel1", []string{"user1"})

	now := model.GetMillis()

	// Create test posts with different embeddings
	// We'll use simple normalized vectors for predictable L2 distances
	testCases := []struct {
		postID           string
		embedding        []float32
		expectedMinScore float32 // minimum expected score
		expectedMaxScore float32 // maximum expected score
		description      string
	}{
		{
			postID:           "identical",
			embedding:        []float32{1, 0, 0}, // Same as query vector
			expectedMinScore: 0.99,
			expectedMaxScore: 1.01,
			description:      "identical vector should have score ~1",
		},
		{
			postID:           "similar",
			embedding:        []float32{0.9, 0.436, 0}, // ~26° angle, L2 ≈ 0.45
			expectedMinScore: 0.85,
			expectedMaxScore: 1.0,
			description:      "similar vector should have score > 0.85",
		},
		{
			postID:           "orthogonal",
			embedding:        []float32{0, 1, 0}, // 90° angle, L2 = sqrt(2)
			expectedMinScore: 0.0,
			expectedMaxScore: 0.1,
			description:      "orthogonal vector should have score ~0",
		},
		{
			postID:           "opposite",
			embedding:        []float32{-1, 0, 0}, // 180° angle, L2 = 2
			expectedMinScore: 0.0,
			expectedMaxScore: 0.01,
			description:      "opposite vector should have score 0 (clamped)",
		},
	}

	// Add posts and store embeddings
	for i, tc := range testCases {
		addTestPosts(t, db, []string{tc.postID}, []int64{now + int64(i)})
		docs := []embeddings.PostDocument{
			{PostID: tc.postID, CreateAt: now + int64(i), TeamID: "team1", ChannelID: "channel1", UserID: "user1", Content: tc.description},
		}
		err = pgVector.Store(context.Background(), docs, [][]float32{tc.embedding})
		require.NoError(t, err)
	}

	// Query with [1, 0, 0] vector
	queryEmbedding := []float32{1, 0, 0}
	results, err := pgVector.Search(context.Background(), queryEmbedding, embeddings.SearchOptions{
		Limit:  10,
		UserID: "user1",
	})
	require.NoError(t, err)
	require.Len(t, results, len(testCases))

	// Verify scores
	scoreMap := make(map[string]float32)
	for _, r := range results {
		scoreMap[r.Document.PostID] = r.Score
		t.Logf("Post %s: score=%.4f", r.Document.PostID, r.Score)
	}

	for _, tc := range testCases {
		score, found := scoreMap[tc.postID]
		require.True(t, found, "Post %s not found in results", tc.postID)
		assert.GreaterOrEqual(t, score, tc.expectedMinScore, "%s: score %.4f below min %.4f", tc.description, score, tc.expectedMinScore)
		assert.LessOrEqual(t, score, tc.expectedMaxScore, "%s: score %.4f above max %.4f", tc.description, score, tc.expectedMaxScore)
	}

	// Verify ordering: identical > similar > orthogonal >= opposite
	assert.Greater(t, scoreMap["identical"], scoreMap["similar"], "identical should score higher than similar")
	assert.Greater(t, scoreMap["similar"], scoreMap["orthogonal"], "similar should score higher than orthogonal")
	assert.GreaterOrEqual(t, scoreMap["orthogonal"], scoreMap["opposite"], "orthogonal should score >= opposite")
}
