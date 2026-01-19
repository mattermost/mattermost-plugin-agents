// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	embeddingsmocks "github.com/mattermost/mattermost-plugin-ai/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestShouldIndexPost(t *testing.T) {
	tests := []struct {
		name     string
		post     *model.Post
		channel  *model.Channel
		expected bool
	}{
		{
			name: "should index regular post",
			post: &model.Post{
				Id:       "post1",
				Message:  "Hello world",
				Type:     model.PostTypeDefault,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			expected: true,
		},
		{
			name: "should not index deleted post",
			post: &model.Post{
				Id:       "post2",
				Message:  "Deleted message",
				Type:     model.PostTypeDefault,
				UserId:   "user1",
				DeleteAt: 123456789, // Non-zero DeleteAt means deleted
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			expected: false,
		},
		{
			name: "should not index empty message",
			post: &model.Post{
				Id:       "post3",
				Message:  "",
				Type:     model.PostTypeDefault,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			expected: false,
		},
		{
			name: "should not index non-default post type",
			post: &model.Post{
				Id:       "post4",
				Message:  "System message",
				Type:     model.PostTypeJoinChannel,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			expected: false,
		},
	}

	// Create indexer with empty bots
	mockBots := &bots.MMBots{}
	indexer := New(nil, nil, mockBots, nil, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := indexer.shouldIndexPost(tt.post, tt.channel)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDeletePost(t *testing.T) {
	mockBots := &bots.MMBots{}
	ctx := context.Background()
	postID := "test-post-id"

	t.Run("does nothing when search is nil", func(t *testing.T) {
		// Create indexer with nil search
		indexer := New(nil, nil, mockBots, nil, nil)

		// Should not panic and should return no error
		err := indexer.DeletePost(ctx, postID)
		require.NoError(t, err)
	})
}

func TestIndexPost(t *testing.T) {
	mockBots := &bots.MMBots{}
	ctx := context.Background()

	t.Run("does not index deleted post", func(t *testing.T) {
		indexer := New(nil, nil, mockBots, nil, nil)

		post := &model.Post{
			Id:       "post2",
			Message:  "Deleted message",
			Type:     model.PostTypeDefault,
			UserId:   "user1",
			DeleteAt: 123456789, // Deleted post
		}
		channel := &model.Channel{
			Id:     "channel1",
			TeamId: "team1",
			Type:   model.ChannelTypeOpen,
		}

		// Call the method - should not panic and return no error
		err := indexer.IndexPost(ctx, post, channel)

		// Verify no error (deleted posts are ignored, not errored)
		require.NoError(t, err)
	})

	t.Run("does nothing when search is nil", func(t *testing.T) {
		// Create indexer with nil search
		indexer := New(nil, nil, mockBots, nil, nil)

		post := &model.Post{
			Id:       "post1",
			Message:  "Test message",
			Type:     model.PostTypeDefault,
			UserId:   "user1",
			DeleteAt: 0,
		}
		channel := &model.Channel{
			Id:     "channel1",
			TeamId: "team1",
			Type:   model.ChannelTypeOpen,
		}

		// Should not panic and should return no error
		err := indexer.IndexPost(ctx, post, channel)
		require.NoError(t, err)
	})
}

func TestFilterAndCreateDocs(t *testing.T) {
	mockBots := &bots.MMBots{}
	indexer := New(nil, nil, mockBots, nil, nil)

	tests := []struct {
		name          string
		posts         []PostRecord
		expectedCount int
	}{
		{
			name: "filters out empty messages",
			posts: []PostRecord{
				{ID: "post1", Message: "Hello", UserID: "user1", CreateAt: 100, TeamID: "team1", ChannelID: "ch1", ChannelType: "O"},
				{ID: "post2", Message: "", UserID: "user1", CreateAt: 200, TeamID: "team1", ChannelID: "ch1", ChannelType: "O"},
			},
			expectedCount: 1,
		},
		{
			name: "creates docs for valid posts",
			posts: []PostRecord{
				{ID: "post1", Message: "Hello", UserID: "user1", CreateAt: 100, TeamID: "team1", ChannelID: "ch1", ChannelType: "O"},
				{ID: "post2", Message: "World", UserID: "user2", CreateAt: 200, TeamID: "team1", ChannelID: "ch2", ChannelType: "O"},
			},
			expectedCount: 2,
		},
		{
			name:          "handles empty input",
			posts:         []PostRecord{},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs := indexer.filterAndCreateDocs(tt.posts)
			assert.Equal(t, tt.expectedCount, len(docs))
		})
	}
}

func TestJobStatusConstants(t *testing.T) {
	// Verify job status constants are defined correctly
	assert.Equal(t, "running", JobStatusRunning)
	assert.Equal(t, "completed", JobStatusCompleted)
	assert.Equal(t, "failed", JobStatusFailed)
	assert.Equal(t, "canceled", JobStatusCanceled)
}

func TestCursorStruct(t *testing.T) {
	cursor := Cursor{
		LastCreateAt: 1234567890,
		LastID:       "test-post-id",
	}

	assert.Equal(t, int64(1234567890), cursor.LastCreateAt)
	assert.Equal(t, "test-post-id", cursor.LastID)
}

func TestModelInfoStruct(t *testing.T) {
	info := ModelInfo{
		ProviderType: "openai",
		ModelName:    "text-embedding-3-small",
		Dimensions:   1536,
		IndexedAt:    1234567890,
	}

	assert.Equal(t, "openai", info.ProviderType)
	assert.Equal(t, "text-embedding-3-small", info.ModelName)
	assert.Equal(t, 1536, info.Dimensions)
	assert.Equal(t, int64(1234567890), info.IndexedAt)
}

func TestHealthCheckResultStruct(t *testing.T) {
	result := HealthCheckResult{
		DBPostCount:      1000,
		IndexedPostCount: 950,
		MissingPosts:     50,
		Status:           "mismatch",
	}

	assert.Equal(t, int64(1000), result.DBPostCount)
	assert.Equal(t, int64(950), result.IndexedPostCount)
	assert.Equal(t, int64(50), result.MissingPosts)
	assert.Equal(t, "mismatch", result.Status)
}

func TestModelCompatibilityStruct(t *testing.T) {
	tests := []struct {
		name          string
		compatibility ModelCompatibility
		expected      bool
	}{
		{
			name: "compatible model",
			compatibility: ModelCompatibility{
				Compatible:   true,
				NeedsReindex: false,
				Reason:       "",
			},
			expected: true,
		},
		{
			name: "incompatible model needs reindex",
			compatibility: ModelCompatibility{
				Compatible:   false,
				NeedsReindex: true,
				Reason:       "dimension mismatch: stored=768, current=1536",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.compatibility.Compatible)
		})
	}
}

func TestCheckModelCompatibility(t *testing.T) {
	tests := []struct {
		name              string
		storedInfo        ModelInfo
		storedInfoErr     error
		currentDimensions int
		currentModelName  string
		expectedCompat    bool
		expectedReindex   bool
		expectedReason    string
	}{
		{
			name:              "fresh install with no stored info returns compatible",
			storedInfo:        ModelInfo{},
			storedInfoErr:     errors.New("not found"),
			currentDimensions: 1536,
			currentModelName:  "text-embedding-3-small",
			expectedCompat:    true,
			expectedReindex:   false,
			expectedReason:    "",
		},
		{
			name: "matching dimensions and empty current model name returns compatible",
			storedInfo: ModelInfo{
				Dimensions: 1536,
				ModelName:  "text-embedding-3-small",
			},
			storedInfoErr:     nil,
			currentDimensions: 1536,
			currentModelName:  "",
			expectedCompat:    true,
			expectedReindex:   false,
			expectedReason:    "",
		},
		{
			name: "dimension mismatch returns incompatible",
			storedInfo: ModelInfo{
				Dimensions: 768,
				ModelName:  "text-embedding-ada-002",
			},
			storedInfoErr:     nil,
			currentDimensions: 1536,
			currentModelName:  "text-embedding-3-small",
			expectedCompat:    false,
			expectedReindex:   true,
			expectedReason:    "dimension mismatch: stored=768, current=1536",
		},
		{
			name: "model name mismatch returns incompatible",
			storedInfo: ModelInfo{
				Dimensions: 1536,
				ModelName:  "text-embedding-ada-002",
			},
			storedInfoErr:     nil,
			currentDimensions: 1536,
			currentModelName:  "text-embedding-3-small",
			expectedCompat:    false,
			expectedReindex:   true,
			expectedReason:    "model changed: stored=text-embedding-ada-002, current=text-embedding-3-small",
		},
		{
			name: "matching config returns compatible",
			storedInfo: ModelInfo{
				Dimensions: 1536,
				ModelName:  "text-embedding-3-small",
			},
			storedInfoErr:     nil,
			currentDimensions: 1536,
			currentModelName:  "text-embedding-3-small",
			expectedCompat:    true,
			expectedReindex:   false,
			expectedReason:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockClient(t)

			// Setup KVGet expectation for GetModelInfo
			mockClient.On("KVGet", IndexerModelKey, mock.AnythingOfType("*indexer.ModelInfo")).
				Run(func(args mock.Arguments) {
					if tt.storedInfoErr == nil {
						info := args.Get(1).(*ModelInfo)
						*info = tt.storedInfo
					}
				}).
				Return(tt.storedInfoErr)

			indexer := New(nil, mockClient, nil, nil, nil)
			result := indexer.CheckModelCompatibility(tt.currentDimensions, tt.currentModelName)

			assert.Equal(t, tt.expectedCompat, result.Compatible)
			assert.Equal(t, tt.expectedReindex, result.NeedsReindex)
			assert.Equal(t, tt.expectedReason, result.Reason)
		})
	}
}

func TestCursorOperations(t *testing.T) {
	t.Run("save and load cursor", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		cursor := Cursor{
			LastCreateAt: 1234567890,
			LastID:       "test-post-id",
		}

		// Expect KVSet to be called with the cursor
		mockClient.On("KVSet", IndexerCursorKey, cursor).Return(nil)

		indexer := New(nil, mockClient, nil, nil, nil)
		indexer.saveCursor(cursor)

		// Now setup for load
		mockClient.On("KVGet", IndexerCursorKey, mock.AnythingOfType("*indexer.Cursor")).
			Run(func(args mock.Arguments) {
				c := args.Get(1).(*Cursor)
				*c = cursor
			}).
			Return(nil)

		loaded := indexer.loadCursor()
		assert.Equal(t, cursor.LastCreateAt, loaded.LastCreateAt)
		assert.Equal(t, cursor.LastID, loaded.LastID)
	})

	t.Run("load with no cursor returns zero values", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		mockClient.On("KVGet", IndexerCursorKey, mock.AnythingOfType("*indexer.Cursor")).
			Return(errors.New("not found"))

		indexer := New(nil, mockClient, nil, nil, nil)
		loaded := indexer.loadCursor()

		assert.Equal(t, int64(0), loaded.LastCreateAt)
		assert.Equal(t, "", loaded.LastID)
	})

	t.Run("save cursor error is logged", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		cursor := Cursor{LastCreateAt: 100, LastID: "post1"}
		mockClient.On("KVSet", IndexerCursorKey, cursor).Return(errors.New("kv error"))
		mockClient.On("LogError", "Failed to save cursor", mock.Anything).Return()

		indexer := New(nil, mockClient, nil, nil, nil)
		indexer.saveCursor(cursor) // Should not panic, just log error
	})
}

func TestLastIndexedTimestamp(t *testing.T) {
	t.Run("save and get timestamp", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		timestamp := int64(1234567890)

		mockClient.On("KVSet", IndexerLastIndexedKey, timestamp).Return(nil)

		indexer := New(nil, mockClient, nil, nil, nil)
		indexer.saveLastIndexedTimestamp(timestamp)

		mockClient.On("KVGet", IndexerLastIndexedKey, mock.AnythingOfType("*int64")).
			Run(func(args mock.Arguments) {
				ts := args.Get(1).(*int64)
				*ts = timestamp
			}).
			Return(nil)

		loaded := indexer.getLastIndexedTimestamp()
		assert.Equal(t, timestamp, loaded)
	})

	t.Run("get with no stored value returns 0", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		mockClient.On("KVGet", IndexerLastIndexedKey, mock.AnythingOfType("*int64")).
			Return(errors.New("not found"))

		indexer := New(nil, mockClient, nil, nil, nil)
		loaded := indexer.getLastIndexedTimestamp()

		assert.Equal(t, int64(0), loaded)
	})

	t.Run("save timestamp error is logged", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		mockClient.On("KVSet", IndexerLastIndexedKey, int64(100)).Return(errors.New("kv error"))
		mockClient.On("LogError", "Failed to save last indexed timestamp", mock.Anything).Return()

		indexer := New(nil, mockClient, nil, nil, nil)
		indexer.saveLastIndexedTimestamp(100) // Should not panic, just log error
	})
}

func TestModelInfoOperations(t *testing.T) {
	t.Run("save and get model info", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		info := ModelInfo{
			ProviderType: "openai",
			ModelName:    "text-embedding-3-small",
			Dimensions:   1536,
		}

		// SaveModelInfo sets IndexedAt before saving
		mockClient.On("KVSet", IndexerModelKey, mock.MatchedBy(func(v interface{}) bool {
			saved := v.(ModelInfo)
			return saved.ProviderType == info.ProviderType &&
				saved.ModelName == info.ModelName &&
				saved.Dimensions == info.Dimensions &&
				saved.IndexedAt > 0
		})).Return(nil)

		indexer := New(nil, mockClient, nil, nil, nil)
		err := indexer.SaveModelInfo(info)
		require.NoError(t, err)

		// Setup for GetModelInfo
		storedInfo := info
		storedInfo.IndexedAt = 1234567890
		mockClient.On("KVGet", IndexerModelKey, mock.AnythingOfType("*indexer.ModelInfo")).
			Run(func(args mock.Arguments) {
				i := args.Get(1).(*ModelInfo)
				*i = storedInfo
			}).
			Return(nil)

		loaded, err := indexer.GetModelInfo()
		require.NoError(t, err)
		assert.Equal(t, storedInfo.ProviderType, loaded.ProviderType)
		assert.Equal(t, storedInfo.ModelName, loaded.ModelName)
		assert.Equal(t, storedInfo.Dimensions, loaded.Dimensions)
		assert.Equal(t, storedInfo.IndexedAt, loaded.IndexedAt)
	})
}

func TestStoreWithRetry(t *testing.T) {
	t.Run("success on first attempt", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		docs := []embeddings.PostDocument{
			{PostID: "post1", Content: "test"},
		}

		mockSearch.On("Store", mock.Anything, docs).Return(nil)

		indexer := New(mockSearch, mockClient, nil, nil, nil)
		err := indexer.storeWithRetry(context.Background(), docs)

		require.NoError(t, err)
	})

	t.Run("success after retries", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		docs := []embeddings.PostDocument{
			{PostID: "post1", Content: "test"},
		}

		// Fail twice, succeed on third attempt
		callCount := 0
		mockSearch.On("Store", mock.Anything, docs).
			Run(func(args mock.Arguments) {
				callCount++
			}).
			Return(func(ctx context.Context, d []embeddings.PostDocument) error {
				if callCount < 3 {
					return errors.New("temporary error")
				}
				return nil
			})

		// Expect LogWarn calls for retries
		mockClient.On("LogWarn", "Embedding store failed, retrying", mock.Anything).Return()

		indexer := New(mockSearch, mockClient, nil, nil, nil)
		err := indexer.storeWithRetry(context.Background(), docs)

		require.NoError(t, err)
		assert.Equal(t, 3, callCount)
	})

	t.Run("failure after max retries", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		docs := []embeddings.PostDocument{
			{PostID: "post1", Content: "test"},
		}

		mockSearch.On("Store", mock.Anything, docs).Return(errors.New("persistent error"))
		mockClient.On("LogWarn", "Embedding store failed, retrying", mock.Anything).Return()

		indexer := New(mockSearch, mockClient, nil, nil, nil)
		err := indexer.storeWithRetry(context.Background(), docs)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "persistent error")
	})
}

func TestStartCatchUpJob(t *testing.T) {
	t.Run("returns error when no previous index exists", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		// No previous timestamp stored
		mockClient.On("KVGet", IndexerLastIndexedKey, mock.AnythingOfType("*int64")).
			Return(errors.New("not found"))

		indexer := New(mockSearch, mockClient, nil, nil, nil)
		_, err := indexer.StartCatchUpJob()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no previous index found")
	})

	t.Run("returns error when search is nil", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		indexer := New(nil, mockClient, nil, nil, nil)
		_, err := indexer.StartCatchUpJob()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "search functionality is not configured")
	})
}

// Integration tests for CheckIndexHealth
// These tests require PostgreSQL with pgvector extension installed.
// Skip if database is not available.

var rootDSN = "postgres://mmuser:mostest@localhost:5432/postgres?sslmode=disable"

func testDB(t *testing.T) *sqlx.DB {
	rootDB, err := sqlx.Connect("postgres", rootDSN)
	if err != nil {
		t.Skipf("Skipping test: PostgreSQL not available: %v", err)
	}
	defer rootDB.Close()

	// Check if pgvector extension is available
	var hasVector bool
	err = rootDB.Get(&hasVector, "SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE name = 'vector')")
	if err != nil || !hasVector {
		t.Skip("Skipping test: pgvector extension not available")
	}

	// Create a unique database name with a timestamp
	dbName := fmt.Sprintf("indexer_test_%d", model.GetMillis())

	// Create the test database
	_, err = rootDB.Exec("CREATE DATABASE " + dbName)
	require.NoError(t, err, "Failed to create test database")
	t.Logf("Created test database: %s", dbName)

	// Connect to the new database
	testDSN := fmt.Sprintf("postgres://mmuser:mostest@localhost:5432/%s?sslmode=disable", dbName)
	db, err := sqlx.Connect("postgres", testDSN)
	if err != nil {
		// Try to clean up the database even if connection fails
		rootDB2, _ := sqlx.Connect("postgres", rootDSN)
		if rootDB2 != nil {
			_, _ = rootDB2.Exec("DROP DATABASE " + dbName)
			rootDB2.Close()
		}
		require.NoError(t, err, "Failed to connect to test database")
	}

	// Store the database name for cleanup
	t.Setenv("INDEXER_TEST_DB", dbName)

	// Enable the pgvector extension
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		db.Close()
		dropTestDB(t)
		require.NoError(t, err, "Failed to create vector extension in test database")
	}

	// Create mock tables for tests
	tables := []string{
		`CREATE TABLE IF NOT EXISTS Posts (
			Id TEXT PRIMARY KEY,
			CreateAt BIGINT NOT NULL,
			DeleteAt BIGINT NOT NULL DEFAULT 0,
			Message TEXT NOT NULL DEFAULT '',
			Type TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS llm_posts_embeddings (
			id TEXT PRIMARY KEY,
			post_id TEXT NOT NULL,
			content TEXT NOT NULL,
			embedding vector(3),
			team_id TEXT,
			channel_id TEXT,
			user_id TEXT,
			created_at BIGINT,
			is_chunk BOOLEAN DEFAULT false,
			chunk_index INTEGER DEFAULT 0,
			total_chunks INTEGER DEFAULT 1
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
	dbName := os.Getenv("INDEXER_TEST_DB")
	if dbName == "" {
		return
	}

	rootDB, err := sqlx.Connect("postgres", rootDSN)
	if err != nil {
		return
	}
	defer rootDB.Close()

	// Drop the test database
	if !t.Failed() {
		_, _ = rootDB.Exec("DROP DATABASE " + dbName)
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

func TestCheckIndexHealth(t *testing.T) {
	t.Run("returns error when search is nil", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		indexer := New(nil, mockClient, nil, nil, nil)
		_, err := indexer.CheckIndexHealth(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "search functionality is not configured")
	})

	t.Run("healthy index when counts match", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		// Add 10 posts to Posts table
		now := model.GetMillis()
		for i := 0; i < 10; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, $3, '')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Add 10 posts to llm_posts_embeddings table
		for i := 0; i < 10; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]')",
				postID, postID, fmt.Sprintf("Content %d", i))
			require.NoError(t, err)
		}

		indexer := New(mockSearch, mockClient, nil, db, nil)
		result, err := indexer.CheckIndexHealth(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(10), result.DBPostCount)
		assert.Equal(t, int64(10), result.IndexedPostCount)
		assert.Equal(t, int64(0), result.MissingPosts)
		assert.Equal(t, "healthy", result.Status)
	})

	t.Run("mismatch status when missing posts within tolerance", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		// Add 100 posts to Posts table
		now := model.GetMillis()
		for i := 0; i < 100; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, $3, '')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Add 99 posts to llm_posts_embeddings (1% missing, within tolerance)
		for i := 0; i < 99; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]')",
				postID, postID, fmt.Sprintf("Content %d", i))
			require.NoError(t, err)
		}

		indexer := New(mockSearch, mockClient, nil, db, nil)
		result, err := indexer.CheckIndexHealth(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(100), result.DBPostCount)
		assert.Equal(t, int64(99), result.IndexedPostCount)
		assert.Equal(t, int64(1), result.MissingPosts)
		assert.Equal(t, "mismatch", result.Status) // 1% is within tolerance but still flagged as mismatch
	})

	t.Run("needs_reindex status when many posts missing", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		// Add 100 posts to Posts table
		now := model.GetMillis()
		for i := 0; i < 100; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, $3, '')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Add only 80 posts to llm_posts_embeddings (20% missing, exceeds tolerance)
		for i := 0; i < 80; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]')",
				postID, postID, fmt.Sprintf("Content %d", i))
			require.NoError(t, err)
		}

		indexer := New(mockSearch, mockClient, nil, db, nil)
		result, err := indexer.CheckIndexHealth(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(100), result.DBPostCount)
		assert.Equal(t, int64(80), result.IndexedPostCount)
		assert.Equal(t, int64(20), result.MissingPosts)
		assert.Equal(t, "needs_reindex", result.Status)
	})

	t.Run("excludes deleted posts from DB count", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		now := model.GetMillis()

		// Add 5 active posts
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, $3, '')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Add 5 deleted posts (should not be counted)
		for i := 5; i < 10; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, $3, $4, '')",
				postID, now+int64(i), now, fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Add 5 posts to llm_posts_embeddings
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]')",
				postID, postID, fmt.Sprintf("Content %d", i))
			require.NoError(t, err)
		}

		indexer := New(mockSearch, mockClient, nil, db, nil)
		result, err := indexer.CheckIndexHealth(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(5), result.DBPostCount) // Only active posts
		assert.Equal(t, int64(5), result.IndexedPostCount)
		assert.Equal(t, "healthy", result.Status)
	})

	t.Run("excludes empty message posts from DB count", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		now := model.GetMillis()

		// Add 5 posts with messages
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, $3, '')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Add 5 posts with empty messages (should not be counted)
		for i := 5; i < 10; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, '', '')",
				postID, now+int64(i))
			require.NoError(t, err)
		}

		// Add 5 posts to llm_posts_embeddings
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]')",
				postID, postID, fmt.Sprintf("Content %d", i))
			require.NoError(t, err)
		}

		indexer := New(mockSearch, mockClient, nil, db, nil)
		result, err := indexer.CheckIndexHealth(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(5), result.DBPostCount) // Only posts with messages
		assert.Equal(t, int64(5), result.IndexedPostCount)
		assert.Equal(t, "healthy", result.Status)
	})
}

func TestCountIndexedPosts(t *testing.T) {
	t.Run("counts unique posts with chunks correctly", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		// Add post1 with 3 chunks
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("post1_chunk_%d", i)
			_, err := db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding, is_chunk, chunk_index, total_chunks) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]', true, $4, 3)",
				id, "post1", fmt.Sprintf("Chunk %d", i), i)
			require.NoError(t, err)
		}

		// Add post2 without chunks
		_, err := db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]')",
			"post2", "post2", "Content 2")
		require.NoError(t, err)

		indexer := New(mockSearch, mockClient, nil, db, nil)
		count, err := indexer.countIndexedPosts(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(2), count) // Should count unique post_ids, not total rows
	})
}
