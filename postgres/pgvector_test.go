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

		opts := embeddings.SearchOptions{
			MinScore: 0.8, // Only include very similar vectors
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
