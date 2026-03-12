// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	embeddingsmocks "github.com/mattermost/mattermost-plugin-ai/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
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
		{
			name: "should index post with empty message but with attachments",
			post: func() *model.Post {
				p := &model.Post{
					Id:       "post5",
					Message:  "",
					Type:     model.PostTypeDefault,
					UserId:   "user1",
					DeleteAt: 0,
				}
				p.SetProps(model.StringInterface{
					"attachments": []interface{}{
						map[string]interface{}{"text": "attachment content"},
					},
				})
				return p
			}(),
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			expected: true,
		},
		{
			name: "should not index post with empty message and empty attachments",
			post: func() *model.Post {
				p := &model.Post{
					Id:       "post6",
					Message:  "",
					Type:     model.PostTypeDefault,
					UserId:   "user1",
					DeleteAt: 0,
				}
				p.SetProps(model.StringInterface{
					"attachments": []interface{}{},
				})
				return p
			}(),
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			expected: false,
		},
	}

	// Create indexer with empty bots
	mockBots := &bots.MMBots{}
	indexer := New(nil, nil, nil, mockBots, nil, nil)

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
		indexer := New(nil, nil, nil, mockBots, nil, nil)

		// Should not panic and should return no error
		err := indexer.DeletePost(ctx, postID)
		require.NoError(t, err)
	})
}

func TestIndexPost(t *testing.T) {
	mockBots := &bots.MMBots{}
	ctx := context.Background()

	t.Run("does not index deleted post", func(t *testing.T) {
		indexer := New(nil, nil, nil, mockBots, nil, nil)

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
		indexer := New(nil, nil, nil, mockBots, nil, nil)

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
	indexer := New(nil, nil, nil, mockBots, nil, nil)

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

	t.Run("includes posts with attachments but empty message", func(t *testing.T) {
		posts := []PostRecord{
			{
				ID:          "post1",
				Message:     "",
				Props:       `{"attachments":[{"text":"attachment content"}]}`,
				UserID:      "user1",
				CreateAt:    100,
				TeamID:      "team1",
				ChannelID:   "ch1",
				ChannelType: "O",
			},
		}
		docs := indexer.filterAndCreateDocs(posts)
		require.Equal(t, 1, len(docs))
		assert.Contains(t, docs[0].Content, "attachment content")
	})

	t.Run("extracts attachment content via PostBody", func(t *testing.T) {
		posts := []PostRecord{
			{
				ID:          "post1",
				Message:     "Hello",
				Props:       `{"attachments":[{"title":"T","text":"Body"}]}`,
				UserID:      "user1",
				CreateAt:    100,
				TeamID:      "team1",
				ChannelID:   "ch1",
				ChannelType: "O",
			},
		}
		docs := indexer.filterAndCreateDocs(posts)
		require.Equal(t, 1, len(docs))
		assert.Contains(t, docs[0].Content, "Hello")
		assert.Contains(t, docs[0].Content, "T")
		assert.Contains(t, docs[0].Content, "Body")
	})

	t.Run("handles invalid Props JSON gracefully", func(t *testing.T) {
		posts := []PostRecord{
			{
				ID:          "post1",
				Message:     "Hello",
				Props:       "not-json",
				UserID:      "user1",
				CreateAt:    100,
				TeamID:      "team1",
				ChannelID:   "ch1",
				ChannelType: "O",
			},
		}
		docs := indexer.filterAndCreateDocs(posts)
		require.Equal(t, 1, len(docs))
		assert.Equal(t, "Hello", docs[0].Content)
	})

	t.Run("handles empty Props string", func(t *testing.T) {
		posts := []PostRecord{
			{
				ID:          "post1",
				Message:     "Hello",
				Props:       "",
				UserID:      "user1",
				CreateAt:    100,
				TeamID:      "team1",
				ChannelID:   "ch1",
				ChannelType: "O",
			},
		}
		docs := indexer.filterAndCreateDocs(posts)
		require.Equal(t, 1, len(docs))
		assert.Equal(t, "Hello", docs[0].Content)
	})
}

func TestCheckModelCompatibility(t *testing.T) {
	tests := []struct {
		name                string
		storedInfo          ModelInfo
		storedInfoErr       error
		currentProviderType string
		currentDimensions   int
		currentModelName    string
		expectedCompat      bool
		expectedReindex     bool
		expectedReason      string
	}{
		{
			name:                "fresh install with no stored info returns compatible",
			storedInfo:          ModelInfo{},
			storedInfoErr:       errors.New("not found"),
			currentProviderType: "openai",
			currentDimensions:   1536,
			currentModelName:    "text-embedding-3-small",
			expectedCompat:      true,
			expectedReindex:     false,
			expectedReason:      "",
		},
		{
			name: "matching dimensions and empty current model name returns compatible",
			storedInfo: ModelInfo{
				Dimensions: 1536,
				ModelName:  "text-embedding-3-small",
			},
			storedInfoErr:       nil,
			currentProviderType: "",
			currentDimensions:   1536,
			currentModelName:    "",
			expectedCompat:      true,
			expectedReindex:     false,
			expectedReason:      "",
		},
		{
			name: "dimension mismatch returns incompatible",
			storedInfo: ModelInfo{
				Dimensions: 768,
				ModelName:  "text-embedding-ada-002",
			},
			storedInfoErr:       nil,
			currentProviderType: "",
			currentDimensions:   1536,
			currentModelName:    "text-embedding-3-small",
			expectedCompat:      false,
			expectedReindex:     true,
			expectedReason:      "dimension mismatch: stored=768, current=1536",
		},
		{
			name: "model name mismatch returns incompatible",
			storedInfo: ModelInfo{
				Dimensions: 1536,
				ModelName:  "text-embedding-ada-002",
			},
			storedInfoErr:       nil,
			currentProviderType: "",
			currentDimensions:   1536,
			currentModelName:    "text-embedding-3-small",
			expectedCompat:      false,
			expectedReindex:     true,
			expectedReason:      "model changed: stored=text-embedding-ada-002, current=text-embedding-3-small",
		},
		{
			name: "matching config returns compatible",
			storedInfo: ModelInfo{
				Dimensions: 1536,
				ModelName:  "text-embedding-3-small",
			},
			storedInfoErr:       nil,
			currentProviderType: "",
			currentDimensions:   1536,
			currentModelName:    "text-embedding-3-small",
			expectedCompat:      true,
			expectedReindex:     false,
			expectedReason:      "",
		},
		{
			name: "provider type mismatch returns incompatible",
			storedInfo: ModelInfo{
				ProviderType: "openai",
				Dimensions:   1536,
				ModelName:    "text-embedding-3-small",
			},
			storedInfoErr:       nil,
			currentProviderType: "anthropic",
			currentDimensions:   1536,
			currentModelName:    "text-embedding-3-small",
			expectedCompat:      false,
			expectedReindex:     true,
			expectedReason:      "provider changed: stored=openai, current=anthropic",
		},
		{
			name: "matching provider type with same config returns compatible",
			storedInfo: ModelInfo{
				ProviderType: "openai",
				Dimensions:   1536,
				ModelName:    "text-embedding-3-small",
			},
			storedInfoErr:       nil,
			currentProviderType: "openai",
			currentDimensions:   1536,
			currentModelName:    "text-embedding-3-small",
			expectedCompat:      true,
			expectedReindex:     false,
			expectedReason:      "",
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

			indexer := New(nil, nil, mockClient, nil, nil, nil)
			result := indexer.CheckModelCompatibility(tt.currentProviderType, tt.currentDimensions, tt.currentModelName)

			assert.Equal(t, tt.expectedCompat, result.Compatible)
			assert.Equal(t, tt.expectedReindex, result.NeedsReindex)
			assert.Equal(t, tt.expectedReason, result.Reason)
		})
	}
}

func TestCursorOperations(t *testing.T) {
	t.Run("load with no cursor returns zero values", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		mockClient.On("KVGet", IndexerCursorKey, mock.AnythingOfType("*indexer.Cursor")).
			Return(errors.New("not found"))

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		loaded := indexer.loadCursor()

		assert.Equal(t, int64(0), loaded.LastCreateAt)
		assert.Equal(t, "", loaded.LastID)
	})

	t.Run("save cursor error is logged", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		cursor := Cursor{LastCreateAt: 100, LastID: "post1"}
		mockClient.On("KVSet", IndexerCursorKey, cursor).Return(errors.New("kv error"))
		mockClient.On("LogError", "Failed to save cursor", mock.Anything).Return()

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		indexer.saveCursor(cursor) // Should not panic, just log error
	})
}

func TestLastIndexedTimestamp(t *testing.T) {
	t.Run("save and get timestamp", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		timestamp := int64(1234567890)

		mockClient.On("KVSet", IndexerLastIndexedKey, timestamp).Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
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

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		loaded := indexer.getLastIndexedTimestamp()

		assert.Equal(t, int64(0), loaded)
	})

	t.Run("save timestamp error is logged", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		mockClient.On("KVSet", IndexerLastIndexedKey, int64(100)).Return(errors.New("kv error"))
		mockClient.On("LogError", "Failed to save last indexed timestamp", mock.Anything).Return()

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		indexer.saveLastIndexedTimestamp(100) // Should not panic, just log error
	})
}

func TestModelInfoOperations(t *testing.T) {
	t.Run("SaveModelInfo sets IndexedAt before saving", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		info := ModelInfo{
			ProviderType: "openai",
			ModelName:    "text-embedding-3-small",
			Dimensions:   1536,
		}

		// SaveModelInfo should set IndexedAt to a non-zero timestamp before saving
		mockClient.On("KVSet", IndexerModelKey, mock.MatchedBy(func(v interface{}) bool {
			saved := v.(ModelInfo)
			return saved.ProviderType == info.ProviderType &&
				saved.ModelName == info.ModelName &&
				saved.Dimensions == info.Dimensions &&
				saved.IndexedAt > 0
		})).Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		err := indexer.SaveModelInfo(info)
		require.NoError(t, err)
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

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, nil)
		err := indexer.storeWithRetry(context.Background(), docs, mockSearch)

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

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, nil)
		err := indexer.storeWithRetry(context.Background(), docs, mockSearch)

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

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, nil)
		err := indexer.storeWithRetry(context.Background(), docs, mockSearch)

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

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, nil)
		_, err := indexer.StartCatchUpJob()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no previous index found")
	})

	t.Run("returns error when search is nil", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
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
		`CREATE TABLE IF NOT EXISTS Channels (
			Id TEXT PRIMARY KEY,
			Type TEXT NOT NULL DEFAULT '',
			Name TEXT NOT NULL DEFAULT '',
			TeamId TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS Posts (
			Id TEXT PRIMARY KEY,
			CreateAt BIGINT NOT NULL,
			DeleteAt BIGINT NOT NULL DEFAULT 0,
			Message TEXT NOT NULL DEFAULT '',
			Props TEXT NOT NULL DEFAULT '{}',
			Type TEXT NOT NULL DEFAULT '',
			ChannelId TEXT DEFAULT '',
			UserId TEXT DEFAULT ''
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

		indexer := New(nil, nil, mockClient, nil, nil, nil)
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

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, nil)
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

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, nil)
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

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, nil)
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

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, nil)
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

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, nil)
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

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, nil)
		count, err := indexer.countIndexedPosts(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(2), count) // Should count unique post_ids, not total rows
	})
}

func TestConfigGetter(t *testing.T) {
	t.Run("getModelInfoFromConfig returns nil when no getter set", func(t *testing.T) {
		indexer := New(nil, nil, nil, nil, nil, nil)

		result := indexer.getModelInfoFromConfig()
		assert.Nil(t, result)
	})

	t.Run("getModelInfoFromConfig returns correct ModelInfo from config", func(t *testing.T) {
		// Pass config getter to constructor
		configGetter := func() embeddings.EmbeddingSearchConfig {
			return embeddings.EmbeddingSearchConfig{
				Dimensions: 1536,
				EmbeddingProvider: embeddings.UpstreamConfig{
					Type:       embeddings.ProviderTypeOpenAI,
					Parameters: []byte(`{"embeddingModel": "text-embedding-3-small"}`),
				},
			}
		}

		indexer := New(nil, configGetter, nil, nil, nil, nil)

		result := indexer.getModelInfoFromConfig()

		require.NotNil(t, result)
		assert.Equal(t, embeddings.ProviderTypeOpenAI, result.ProviderType)
		assert.Equal(t, "text-embedding-3-small", result.ModelName)
		assert.Equal(t, 1536, result.Dimensions)
	})
}

func TestStoreWithRetryIncremental(t *testing.T) {
	t.Run("success on first attempt", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		docs := []embeddings.PostDocument{
			{PostID: "post1", Content: "test"},
		}

		mockSearch.On("Store", mock.Anything, docs).Return(nil)

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, nil)
		err := indexer.storeWithRetryIncremental(context.Background(), docs, mockSearch)

		require.NoError(t, err)
	})

	t.Run("success after retry", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		docs := []embeddings.PostDocument{
			{PostID: "post1", Content: "test"},
		}

		// Fail on first attempt, succeed on second
		callCount := 0
		mockSearch.On("Store", mock.Anything, docs).
			Run(func(args mock.Arguments) {
				callCount++
			}).
			Return(func(ctx context.Context, d []embeddings.PostDocument) error {
				if callCount == 1 {
					return errors.New("temporary error")
				}
				return nil
			})

		mockClient.On("LogWarn", "Incremental embedding store failed, retrying", mock.Anything).Return()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, nil)
		err := indexer.storeWithRetryIncremental(context.Background(), docs, mockSearch)

		require.NoError(t, err)
		assert.Equal(t, 2, callCount)
	})

	t.Run("failure after max retries", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		docs := []embeddings.PostDocument{
			{PostID: "post1", Content: "test"},
		}

		// Always fail
		mockSearch.On("Store", mock.Anything, docs).Return(errors.New("persistent error"))
		mockClient.On("LogWarn", "Incremental embedding store failed, retrying", mock.Anything).Return()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, nil)
		err := indexer.storeWithRetryIncremental(context.Background(), docs, mockSearch)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "persistent error")
	})
}

func TestGetJobStatusIncludesStale(t *testing.T) {
	t.Run("not found returns error", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found"))

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		_, err := indexer.GetJobStatus()

		require.Error(t, err)
	})

	t.Run("completed job is not stale", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusCompleted
			}).
			Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		jobStatus, err := indexer.GetJobStatus()

		require.NoError(t, err)
		assert.False(t, jobStatus.IsStale)
		assert.Equal(t, JobStatusCompleted, jobStatus.Status)
	})

	t.Run("running job within threshold is not stale", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		recentTime := time.Now().Add(-5 * time.Minute)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
				status.LastUpdatedAt = recentTime
			}).
			Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		jobStatus, err := indexer.GetJobStatus()

		require.NoError(t, err)
		assert.False(t, jobStatus.IsStale)
		assert.Equal(t, JobStatusRunning, jobStatus.Status)
	})

	t.Run("running job beyond threshold is stale", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		oldTime := time.Now().Add(-45 * time.Minute)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
				status.LastUpdatedAt = oldTime
			}).
			Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		jobStatus, err := indexer.GetJobStatus()

		require.NoError(t, err)
		assert.True(t, jobStatus.IsStale)
		assert.Equal(t, JobStatusRunning, jobStatus.Status)
	})

	t.Run("uses StartedAt as fallback when LastUpdatedAt is zero", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		oldStartTime := time.Now().Add(-45 * time.Minute)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
				status.StartedAt = oldStartTime
			}).
			Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		jobStatus, err := indexer.GetJobStatus()

		require.NoError(t, err)
		assert.True(t, jobStatus.IsStale)
	})
}

// TestShouldIndexPost_AdditionalCases tests additional shouldIndexPost scenarios
func TestShouldIndexPost_AdditionalCases(t *testing.T) {
	tests := []struct {
		name     string
		post     *model.Post
		channel  *model.Channel
		botSetup func(*bots.MMBots)
		expected bool
	}{
		{
			name: "should skip posts in DM channels with bots",
			post: &model.Post{
				Id:       "post1",
				Message:  "Hello",
				Type:     model.PostTypeDefault,
				UserId:   "regular-user-id",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "dm-channel-id",
				Type: model.ChannelTypeDirect,
				Name: "bot-user-id__regular-user-id", // DM channel name contains bot ID
			},
			botSetup: func(mockBots *bots.MMBots) {
				testBot := bots.NewBot(
					llm.BotConfig{Name: "testbot"},
					llm.ServiceConfig{},
					&model.Bot{UserId: "bot-user-id"},
					nil,
				)
				mockBots.SetBotsForTesting([]*bots.Bot{testBot})
			},
			expected: false,
		},
		{
			name: "should skip bot posts via IsAnyBot check",
			post: &model.Post{
				Id:       "bot-post1",
				Message:  "Bot message",
				Type:     model.PostTypeDefault,
				UserId:   "bot-user-id",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
				Name: "town-square",
			},
			botSetup: func(mockBots *bots.MMBots) {
				testBot := bots.NewBot(
					llm.BotConfig{Name: "testbot"},
					llm.ServiceConfig{},
					&model.Bot{UserId: "bot-user-id"},
					nil,
				)
				mockBots.SetBotsForTesting([]*bots.Bot{testBot})
			},
			expected: false,
		},
		{
			name: "should skip PostTypeJoinChannel",
			post: &model.Post{
				Id:       "post1",
				Message:  "user joined the channel",
				Type:     model.PostTypeJoinChannel,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			botSetup: func(mockBots *bots.MMBots) {},
			expected: false,
		},
		{
			name: "should skip PostTypeLeaveChannel",
			post: &model.Post{
				Id:       "post1",
				Message:  "user left the channel",
				Type:     model.PostTypeLeaveChannel,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			botSetup: func(mockBots *bots.MMBots) {},
			expected: false,
		},
		{
			name: "should skip PostTypeAddToChannel",
			post: &model.Post{
				Id:       "post1",
				Message:  "user added to channel",
				Type:     model.PostTypeAddToChannel,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			botSetup: func(mockBots *bots.MMBots) {},
			expected: false,
		},
		{
			name: "should skip PostTypeRemoveFromChannel",
			post: &model.Post{
				Id:       "post1",
				Message:  "user removed from channel",
				Type:     model.PostTypeRemoveFromChannel,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			botSetup: func(mockBots *bots.MMBots) {},
			expected: false,
		},
		{
			name: "should skip PostTypeHeaderChange",
			post: &model.Post{
				Id:       "post1",
				Message:  "channel header changed",
				Type:     model.PostTypeHeaderChange,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "channel1",
				Type: model.ChannelTypeOpen,
			},
			botSetup: func(mockBots *bots.MMBots) {},
			expected: false,
		},
		{
			name: "should index regular post in DM with non-bot user",
			post: &model.Post{
				Id:       "post1",
				Message:  "Hello",
				Type:     model.PostTypeDefault,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel: &model.Channel{
				Id:   "dm-channel",
				Type: model.ChannelTypeDirect,
				Name: "user1__user2",
			},
			botSetup: func(mockBots *bots.MMBots) {
				// Bot has a different user ID not in channel name
				testBot := bots.NewBot(
					llm.BotConfig{Name: "testbot"},
					llm.ServiceConfig{},
					&model.Bot{UserId: "bot-user-id"},
					nil,
				)
				mockBots.SetBotsForTesting([]*bots.Bot{testBot})
			},
			expected: true,
		},
		{
			name: "should handle nil channel gracefully",
			post: &model.Post{
				Id:       "post1",
				Message:  "Hello",
				Type:     model.PostTypeDefault,
				UserId:   "user1",
				DeleteAt: 0,
			},
			channel:  nil,
			botSetup: func(mockBots *bots.MMBots) {},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockBots := &bots.MMBots{}
			tt.botSetup(mockBots)
			indexer := New(nil, nil, nil, mockBots, nil, nil)
			result := indexer.shouldIndexPost(tt.post, tt.channel)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIndexPost_WithSearchError tests IndexPost when search.Store() returns an error
func TestIndexPost_WithSearchError(t *testing.T) {
	t.Run("returns error when search.Store fails", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		ctx := context.Background()
		post := &model.Post{
			Id:        "post1",
			Message:   "Test message",
			Type:      model.PostTypeDefault,
			UserId:    "user1",
			ChannelId: "channel1",
			CreateAt:  1234567890,
			DeleteAt:  0,
		}
		channel := &model.Channel{
			Id:     "channel1",
			TeamId: "team1",
			Type:   model.ChannelTypeOpen,
		}

		// Store should fail twice (maxIncrementalRetries = 2)
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(errors.New("storage error"))
		mockClient.On("LogWarn", "Incremental embedding store failed, retrying", mock.Anything).Return()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, nil, nil)
		err := indexer.IndexPost(ctx, post, channel)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "storage error")
	})
}

// TestDeletePost_WithSearchError tests DeletePost when search.Delete() returns an error
func TestDeletePost_WithSearchError(t *testing.T) {
	t.Run("returns error when search.Delete fails", func(t *testing.T) {
		mockBots := &bots.MMBots{}
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		ctx := context.Background()
		postID := "test-post-id"

		mockSearch.On("Delete", mock.Anything, []string{postID}).Return(errors.New("delete error"))

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, nil, mockBots, nil, nil)
		err := indexer.DeletePost(ctx, postID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete error")
	})
}

// TestStartReindexJob tests the StartReindexJob function
func TestStartReindexJob(t *testing.T) {
	t.Run("returns error when search is nil", func(t *testing.T) {
		indexer := New(nil, nil, nil, nil, nil, nil)
		_, err := indexer.StartReindexJob(true)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "search functionality is not configured")
	})

	t.Run("returns error when getSearch returns nil", func(t *testing.T) {
		indexer := New(func() embeddings.EmbeddingSearch { return nil }, nil, nil, nil, nil, nil)
		_, err := indexer.StartReindexJob(true)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "search functionality is not configured")
	})

	t.Run("returns error when job already running", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		// Return a running job status
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
				status.StartedAt = time.Now()
			}).
			Return(nil)

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, nil)
		_, err := indexer.StartReindexJob(true)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "job already running")
	})

	t.Run("returns error on KVGet failure", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("kv get error"))

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, nil)
		_, err := indexer.StartReindexJob(true)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to check job status")
	})

	t.Run("happy path starts job and persists status", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockMutexAPI := &plugintest.API{}

		// Setup mutex API
		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		// First KVGet (optimistic check) - no job running
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found")).Once()

		// Second KVGet (after mutex acquired) - still no job running
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found")).Once()

		// KVSet for job status
		var savedStatus *JobStatus
		mockClient.On("KVSet", ReindexJobKey, mock.MatchedBy(func(v interface{}) bool {
			status := v.(JobStatus)
			savedStatus = &status
			return status.Status == JobStatusRunning &&
				!status.StartedAt.IsZero() &&
				status.CutoffAt > 0 &&
				status.NodeID != ""
		})).Return(nil).Once()

		// KVDelete for cursor (clearIndex=true)
		mockClient.On("KVDelete", IndexerCursorKey).Return(nil).Maybe()

		// For the background job - we need to handle various operations
		// The job will fail because we don't have full DB setup, but the start should succeed
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()
		mockSearch.On("Clear", mock.Anything).Return(nil).Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, mockMutexAPI)
		status, err := indexer.StartReindexJob(true)

		require.NoError(t, err)
		assert.Equal(t, JobStatusRunning, status.Status)
		assert.NotZero(t, status.CutoffAt)
		assert.NotEmpty(t, status.NodeID)
		assert.NotNil(t, savedStatus)

		// Give the background goroutine a moment to start
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("returns error on KVSet failure", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockMutexAPI := &plugintest.API{}

		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		// First KVGet (optimistic check)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found")).Once()

		// Second KVGet (after mutex)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found")).Once()

		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()

		// KVSet fails
		mockClient.On("KVSet", ReindexJobKey, mock.Anything).Return(errors.New("kv set error")).Once()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, mockMutexAPI)
		_, err := indexer.StartReindexJob(true)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to save job status")
	})
}

// TestCancelJob tests the CancelJob function
func TestCancelJob(t *testing.T) {
	t.Run("returns error when no job is running", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockMutexAPI := &plugintest.API{}

		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusCompleted
			}).
			Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, mockMutexAPI)
		_, err := indexer.CancelJob()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "not running")
	})

	t.Run("successfully cancels running job", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockMutexAPI := &plugintest.API{}

		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
				status.StartedAt = time.Now()
			}).
			Return(nil)

		mockClient.On("KVSet", ReindexJobKey, mock.MatchedBy(func(v interface{}) bool {
			status := v.(JobStatus)
			return status.Status == JobStatusCanceled && !status.CompletedAt.IsZero()
		})).Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, mockMutexAPI)
		status, err := indexer.CancelJob()

		require.NoError(t, err)
		assert.Equal(t, JobStatusCanceled, status.Status)
		assert.False(t, status.CompletedAt.IsZero())
	})

	t.Run("returns error on KVGet failure", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockMutexAPI := &plugintest.API{}

		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("kv error"))

		indexer := New(nil, nil, mockClient, nil, nil, mockMutexAPI)
		_, err := indexer.CancelJob()

		require.Error(t, err)
	})

	t.Run("returns error on KVSet failure", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockMutexAPI := &plugintest.API{}

		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
			}).
			Return(nil)

		mockClient.On("KVSet", ReindexJobKey, mock.Anything).Return(errors.New("save error"))

		indexer := New(nil, nil, mockClient, nil, nil, mockMutexAPI)
		_, err := indexer.CancelJob()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to save job status")
	})
}

// Additional TestStartCatchUpJob tests - extending the existing tests
func TestStartCatchUpJob_AdditionalCases(t *testing.T) {
	t.Run("happy path starts catch up job", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockMutexAPI := &plugintest.API{}

		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		lastIndexed := int64(1234567890)

		// Get last indexed timestamp
		mockClient.On("KVGet", IndexerLastIndexedKey, mock.AnythingOfType("*int64")).
			Run(func(args mock.Arguments) {
				ts := args.Get(1).(*int64)
				*ts = lastIndexed
			}).
			Return(nil).Once()

		// Check if job is running
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found"))

		// Save job status
		mockClient.On("KVSet", ReindexJobKey, mock.MatchedBy(func(v interface{}) bool {
			status := v.(JobStatus)
			return status.Status == JobStatusRunning && status.Resumable == true
		})).Return(nil).Once()

		// Save cursor
		mockClient.On("KVSet", IndexerCursorKey, mock.MatchedBy(func(v interface{}) bool {
			cursor := v.(Cursor)
			return cursor.LastCreateAt == lastIndexed && cursor.LastID == ""
		})).Return(nil).Once()

		// Background job operations
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, mockMutexAPI)
		status, err := indexer.StartCatchUpJob()

		require.NoError(t, err)
		assert.Equal(t, JobStatusRunning, status.Status)
		assert.True(t, status.Resumable)

		// Give the background goroutine a moment
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("returns error when job already running", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockMutexAPI := &plugintest.API{}

		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		// Get last indexed timestamp
		mockClient.On("KVGet", IndexerLastIndexedKey, mock.AnythingOfType("*int64")).
			Run(func(args mock.Arguments) {
				ts := args.Get(1).(*int64)
				*ts = int64(1234567890)
			}).
			Return(nil).Once()

		// Return running job with recent heartbeat (non-stale)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
				status.StartedAt = time.Now()
				status.LastUpdatedAt = time.Now()
			}).
			Return(nil)

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, mockMutexAPI)
		_, err := indexer.StartCatchUpJob()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "job already running")
	})

	t.Run("sets CutoffAt to current timestamp", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockMutexAPI := &plugintest.API{}

		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		lastIndexed := int64(1234567890)
		beforeCall := time.Now().UnixMilli()

		// Get last indexed timestamp
		mockClient.On("KVGet", IndexerLastIndexedKey, mock.AnythingOfType("*int64")).
			Run(func(args mock.Arguments) {
				ts := args.Get(1).(*int64)
				*ts = lastIndexed
			}).
			Return(nil).Once()

		// Check if job is running
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found"))

		// Capture the saved job status to verify CutoffAt
		var savedStatus JobStatus
		mockClient.On("KVSet", ReindexJobKey, mock.MatchedBy(func(v interface{}) bool {
			status := v.(JobStatus)
			savedStatus = status
			return status.Status == JobStatusRunning
		})).Return(nil).Once()

		// Save cursor
		mockClient.On("KVSet", IndexerCursorKey, mock.Anything).Return(nil).Once()

		// Background job operations
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, mockMutexAPI)
		status, err := indexer.StartCatchUpJob()

		afterCall := time.Now().UnixMilli()

		require.NoError(t, err)
		assert.Equal(t, JobStatusRunning, status.Status)

		// CutoffAt must be set (non-zero) and within the time window of the call
		assert.NotZero(t, savedStatus.CutoffAt, "CutoffAt should be set")
		assert.GreaterOrEqual(t, savedStatus.CutoffAt, beforeCall, "CutoffAt should be >= time before call")
		assert.LessOrEqual(t, savedStatus.CutoffAt, afterCall, "CutoffAt should be <= time after call")

		// Also verify the returned status
		assert.NotZero(t, status.CutoffAt, "returned status.CutoffAt should be set")

		// Give the background goroutine a moment
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("catches up only posts created after last indexed timestamp", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockMutexAPI := &plugintest.API{}
		mockBots := &bots.MMBots{}

		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		// Create test channel
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name, TeamId) VALUES ('channel1', 'O', 'town-square', 'team1')")
		require.NoError(t, err)

		// Timeline setup: lastIndexedTime is the cutoff point
		now := model.GetMillis()
		lastIndexedTime := now - 10000 // 10 seconds ago

		// Posts BEFORE lastIndexedTime (should NOT be caught up - simulate already indexed)
		oldPostIDs := []string{"old-post-1", "old-post-2", "old-post-3"}
		for i, postID := range oldPostIDs {
			_, err = db.Exec(
				"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ($1, $2, 0, $3, '', 'channel1', 'user1')",
				postID, lastIndexedTime-1000-int64(i)*100, fmt.Sprintf("Old message %d", i))
			require.NoError(t, err)
		}

		// Posts AFTER lastIndexedTime (SHOULD be caught up)
		newPostIDs := []string{"new-post-1", "new-post-2", "new-post-3"}
		for i, postID := range newPostIDs {
			_, err = db.Exec(
				"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ($1, $2, 0, $3, '', 'channel1', 'user1')",
				postID, lastIndexedTime+int64(i+1)*100, fmt.Sprintf("New message %d", i))
			require.NoError(t, err)
		}

		// Mock: return lastIndexedTime when asked
		mockClient.On("KVGet", IndexerLastIndexedKey, mock.AnythingOfType("*int64")).
			Run(func(args mock.Arguments) {
				ts := args.Get(1).(*int64)
				*ts = lastIndexedTime
			}).
			Return(nil)

		// Check if job is running - return not found
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found"))

		// Cursor operations - the catch-up job sets cursor to start from lastIndexedTime
		// When the background job loads it, return the cursor that was set
		mockClient.On("KVGet", IndexerCursorKey, mock.AnythingOfType("*indexer.Cursor")).
			Run(func(args mock.Arguments) {
				cursor := args.Get(1).(*Cursor)
				cursor.LastCreateAt = lastIndexedTime
				cursor.LastID = ""
			}).
			Return(nil).Maybe()

		// Other KVGet calls (like model info)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()

		// Track which documents are stored
		var storedPostIDs []string
		var storedMu sync.Mutex
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				docs := args.Get(1).([]embeddings.PostDocument)
				storedMu.Lock()
				for _, doc := range docs {
					storedPostIDs = append(storedPostIDs, doc.PostID)
				}
				storedMu.Unlock()
			}).
			Return(nil).Maybe()

		// Other KV operations
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, mockMutexAPI)
		status, err := indexer.StartCatchUpJob()

		require.NoError(t, err)
		assert.Equal(t, JobStatusRunning, status.Status)

		// Wait for background job to complete
		time.Sleep(300 * time.Millisecond)

		// VERIFY: Only posts after lastIndexedTime were stored
		storedMu.Lock()
		defer storedMu.Unlock()

		assert.ElementsMatch(t, newPostIDs, storedPostIDs,
			"Only posts after lastIndexedTime should be indexed; got: %v, expected: %v", storedPostIDs, newPostIDs)

		// VERIFY: Posts before lastIndexedTime were NOT stored
		for _, oldPostID := range oldPostIDs {
			assert.NotContains(t, storedPostIDs, oldPostID,
				"Posts before lastIndexedTime should not be re-indexed: %s", oldPostID)
		}
	})
}

// TestRunReindexJob tests the background reindex job processing
func TestRunReindexJob(t *testing.T) {
	t.Run("job completes successfully with batch processing", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		// Add test posts to the database
		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Setup mocks for job execution
		mockSearch.On("Clear", mock.Anything).Return(nil)
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil).Maybe()

		// KV operations - use Maybe() for flexible matching
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
			CutoffAt:  now + 100,
		}

		// Run the job directly
		indexer.runReindexJob(jobStatus, true)

		// If job failed, print the error for debugging
		if jobStatus.Status == JobStatusFailed {
			t.Logf("Job failed with error: %s", jobStatus.Error)
		}

		assert.Equal(t, JobStatusCompleted, jobStatus.Status)
		assert.False(t, jobStatus.CompletedAt.IsZero())
		assert.GreaterOrEqual(t, jobStatus.ProcessedRows, int64(5))
	})

	t.Run("job handles search not configured", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
		}

		indexer.runReindexJob(jobStatus, true)

		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Search not configured")
	})

	t.Run("job handles getSearch returning nil", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil)

		indexer := New(func() embeddings.EmbeddingSearch { return nil }, nil, mockClient, nil, nil, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
		}

		indexer.runReindexJob(jobStatus, true)

		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Search not configured")
	})

	t.Run("job detects cancellation and stops", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		// Add posts
		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		for i := 0; i < 10; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		mockSearch.On("Clear", mock.Anything).Return(nil)

		// Return canceled status on first check
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusCanceled
			}).
			Return(nil)
		mockClient.On("KVGet", IndexerCursorKey, mock.AnythingOfType("*indexer.Cursor")).
			Return(errors.New("not found"))
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
			CutoffAt:  now + 100,
		}

		indexer.runReindexJob(jobStatus, true)

		// Job should exit without completing
		assert.NotEqual(t, JobStatusCompleted, jobStatus.Status)
	})

	t.Run("job handles clear index failure", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		mockSearch.On("Clear", mock.Anything).Return(errors.New("clear failed"))
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil)

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, nil, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
		}

		indexer.runReindexJob(jobStatus, true)

		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Failed to clear search index")
	})
}

// TestJobProgressAndHeartbeat tests job progress updates and heartbeat mechanism
func TestJobProgressAndHeartbeat(t *testing.T) {
	t.Run("job updates progress during batch processing", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		// Add many posts to trigger progress saves
		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		for i := 0; i < 600; i++ { // More than 500 to trigger progress save
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		mockSearch.On("Clear", mock.Anything).Return(nil)
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil).Maybe()

		// Use Maybe() for all mocks to make them flexible
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()

		// Track job status saves
		saveCount := 0
		mockClient.On("KVSet", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				if args.Get(0).(string) == ReindexJobKey {
					saveCount++
				}
			}).
			Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
			CutoffAt:  now + 700,
		}

		indexer.runReindexJob(jobStatus, true)

		if jobStatus.Status == JobStatusFailed {
			t.Logf("Job failed with error: %s", jobStatus.Error)
		}

		assert.Equal(t, JobStatusCompleted, jobStatus.Status)
		assert.Greater(t, saveCount, 1) // Should have saved progress at least once
		assert.False(t, jobStatus.LastUpdatedAt.IsZero())
	})
}

// TestBatchProcessing tests batch processing and pagination
func TestBatchProcessing(t *testing.T) {
	t.Run("processes posts in batches with correct pagination", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		// Add posts that will require multiple batches (defaultBatchSize = 100)
		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		for i := 0; i < 250; i++ {
			postID := fmt.Sprintf("post%03d", i)
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		mockSearch.On("Clear", mock.Anything).Return(nil)

		// Track store calls to verify batch processing
		storeCallCount := 0
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				storeCallCount++
			}).
			Return(nil).Maybe()

		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
			CutoffAt:  now + 500,
		}

		indexer.runReindexJob(jobStatus, true)

		if jobStatus.Status == JobStatusFailed {
			t.Logf("Job failed with error: %s", jobStatus.Error)
		}

		assert.Equal(t, JobStatusCompleted, jobStatus.Status)
		assert.GreaterOrEqual(t, storeCallCount, 3) // Should have at least 3 batches (250/100)
		assert.Equal(t, int64(250), jobStatus.ProcessedRows)
	})
}

// TestCutoffTimestampHandling tests that posts created during reindex are handled
func TestCutoffTimestampHandling(t *testing.T) {
	t.Run("job uses cutoff timestamp to exclude new posts", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		cutoffTime := model.GetMillis()

		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)

		// Add posts before cutoff
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("old-post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, cutoffTime-100+int64(i), fmt.Sprintf("Old message %d", i))
			require.NoError(t, err)
		}

		// Add posts after cutoff (should not be in main pass, but caught in catch-up)
		for i := 0; i < 3; i++ {
			postID := fmt.Sprintf("new-post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, cutoffTime+1+int64(i), fmt.Sprintf("New message %d", i))
			require.NoError(t, err)
		}

		mockSearch.On("Clear", mock.Anything).Return(nil)
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil).Maybe()

		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
			CutoffAt:  cutoffTime,
		}

		indexer.runReindexJob(jobStatus, true)

		if jobStatus.Status == JobStatusFailed {
			t.Logf("Job failed with error: %s", jobStatus.Error)
		}

		assert.Equal(t, JobStatusCompleted, jobStatus.Status)
		// Should process 5 old posts + 3 new posts (via catch-up)
		assert.Equal(t, int64(8), jobStatus.ProcessedRows)
	})
}

// TestStaleJobDetectionEdgeCases tests edge cases in stale job detection via GetJobStatus
func TestStaleJobDetectionEdgeCases(t *testing.T) {
	t.Run("job exactly at threshold boundary is stale", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		// Exactly at the threshold
		exactThresholdTime := time.Now().Add(-StaleJobThreshold)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
				status.LastUpdatedAt = exactThresholdTime
			}).
			Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		jobStatus, err := indexer.GetJobStatus()

		require.NoError(t, err)
		// At exact boundary, time.Since will return >= threshold, so IsStale should be true
		assert.True(t, jobStatus.IsStale)
	})

	t.Run("job just under threshold is not stale", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		justUnderThreshold := time.Now().Add(-StaleJobThreshold + time.Minute)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
				status.LastUpdatedAt = justUnderThreshold
			}).
			Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		jobStatus, err := indexer.GetJobStatus()

		require.NoError(t, err)
		assert.False(t, jobStatus.IsStale)
	})
}

// TestCheckIndexHealth_ExcludesBotDMChannels tests bot DM channel exclusion in health checks
func TestCheckIndexHealth_ExcludesBotDMChannels(t *testing.T) {
	t.Run("excludes posts in bot DM channels", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		// Setup bot
		testBot := bots.NewBot(
			llm.BotConfig{Name: "testbot"},
			llm.ServiceConfig{},
			&model.Bot{UserId: "bot-user-id"},
			nil,
		)
		mockBots.SetBotsForTesting([]*bots.Bot{testBot})

		now := model.GetMillis()

		// Create a regular channel
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('regular-channel', 'O', 'town-square')")
		require.NoError(t, err)

		// Create a DM channel with the bot (name contains bot user ID)
		_, err = db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('dm-with-bot', 'D', 'bot-user-id__regular-user-id')")
		require.NoError(t, err)

		// Add 5 posts in regular channel (should be counted)
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("regular-post%d", i)
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ($1, $2, 0, $3, '', 'regular-channel', 'regular-user-id')",
				postID, now+int64(i), fmt.Sprintf("Regular message %d", i))
			require.NoError(t, err)
		}

		// Add 3 posts in DM with bot (should be excluded)
		for i := 0; i < 3; i++ {
			postID := fmt.Sprintf("dm-post%d", i)
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ($1, $2, 0, $3, '', 'dm-with-bot', 'regular-user-id')",
				postID, now+int64(100+i), fmt.Sprintf("DM message %d", i))
			require.NoError(t, err)
		}

		// Add 5 indexed posts (only regular posts)
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("regular-post%d", i)
			_, err = db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]')",
				postID, postID, fmt.Sprintf("Content %d", i))
			require.NoError(t, err)
		}

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)
		result, err := indexer.CheckIndexHealth(context.Background())

		require.NoError(t, err)
		// Should only count 5 posts (excluding DM with bot)
		assert.Equal(t, int64(5), result.DBPostCount)
		assert.Equal(t, int64(5), result.IndexedPostCount)
		assert.Equal(t, "healthy", result.Status)
	})
}

func TestCheckIndexHealth_ExcludesBotPosts(t *testing.T) {
	t.Run("excludes posts from bot users when bots configured", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		// Setup bots with user IDs
		testBot := bots.NewBot(
			llm.BotConfig{Name: "testbot"},
			llm.ServiceConfig{},
			&model.Bot{UserId: "bot-user-id"},
			nil,
		)
		mockBots.SetBotsForTesting([]*bots.Bot{testBot})

		now := model.GetMillis()

		// Create a regular channel (not a DM with bot)
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('regular-channel', 'O', 'town-square')")
		require.NoError(t, err)

		// Add 5 posts from regular users in regular channel
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("user-post%d", i)
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ($1, $2, 0, $3, '', 'regular-channel', 'regular-user-id')",
				postID, now+int64(i), fmt.Sprintf("User message %d", i))
			require.NoError(t, err)
		}

		// Add 3 posts from the bot user (should be excluded)
		for i := 0; i < 3; i++ {
			postID := fmt.Sprintf("bot-post%d", i)
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ($1, $2, 0, $3, '', 'regular-channel', 'bot-user-id')",
				postID, now+int64(100+i), fmt.Sprintf("Bot message %d", i))
			require.NoError(t, err)
		}

		// Add 5 indexed posts (matching the user posts)
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("user-post%d", i)
			_, err = db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]')",
				postID, postID, fmt.Sprintf("Content %d", i))
			require.NoError(t, err)
		}

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)
		result, err := indexer.CheckIndexHealth(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(5), result.DBPostCount) // Should exclude bot posts
		assert.Equal(t, int64(5), result.IndexedPostCount)
		assert.Equal(t, "healthy", result.Status)
	})

	t.Run("works correctly when no bots configured", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		now := model.GetMillis()

		// Add 5 posts
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, $3, '')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Add 5 indexed posts
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]')",
				postID, postID, fmt.Sprintf("Content %d", i))
			require.NoError(t, err)
		}

		// nil bots
		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, nil, db, nil)
		result, err := indexer.CheckIndexHealth(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(5), result.DBPostCount)
		assert.Equal(t, int64(5), result.IndexedPostCount)
		assert.Equal(t, "healthy", result.Status)
	})

	t.Run("works correctly with empty bot list", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}
		mockBots.SetBotsForTesting([]*bots.Bot{}) // Empty list

		now := model.GetMillis()

		// Add 5 posts
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, $3, '')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Add 5 indexed posts
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err := db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ($1, $2, $3, '[0.1, 0.2, 0.3]')",
				postID, postID, fmt.Sprintf("Content %d", i))
			require.NoError(t, err)
		}

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)
		result, err := indexer.CheckIndexHealth(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(5), result.DBPostCount)
		assert.Equal(t, int64(5), result.IndexedPostCount)
		assert.Equal(t, "healthy", result.Status)
	})
}

// TestResumeFromCheckpoint tests that reindexing can resume from a saved checkpoint
func TestResumeFromCheckpoint(t *testing.T) {
	t.Run("resumes from saved cursor when clearIndex is false", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		// Add a channel and 10 posts with sequential timestamps
		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)

		for i := 0; i < 10; i++ {
			postID := fmt.Sprintf("post%02d", i)
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, now+int64(i*1000), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Simulate a saved cursor from a previous failed job
		// The cursor points to post05, so resuming should start from post06
		savedCursor := Cursor{
			LastCreateAt: now + 5000, // Timestamp of post05
			LastID:       "post05",
		}

		// Track which posts are indexed
		var indexedPosts []string
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				docs := args.Get(1).([]embeddings.PostDocument)
				for _, doc := range docs {
					indexedPosts = append(indexedPosts, doc.PostID)
				}
			}).
			Return(nil).Maybe()

		// No Clear call should happen when clearIndex=false
		// (If Clear is called, the test will fail with "unexpected method call")

		// KV operations
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found")).Maybe()
		mockClient.On("KVGet", IndexerCursorKey, mock.AnythingOfType("*indexer.Cursor")).
			Run(func(args mock.Arguments) {
				cursor := args.Get(1).(*Cursor)
				*cursor = savedCursor
			}).
			Return(nil).Once()
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)

		jobStatus := &JobStatus{
			Status:        JobStatusRunning,
			StartedAt:     time.Now(),
			CutoffAt:      now + 20000, // Include all posts
			ProcessedRows: 6,           // Already processed 6 posts (post00-post05)
		}

		// Run with clearIndex=false to resume from checkpoint
		indexer.runReindexJob(jobStatus, false)

		if jobStatus.Status == JobStatusFailed {
			t.Logf("Job failed with error: %s", jobStatus.Error)
		}

		assert.Equal(t, JobStatusCompleted, jobStatus.Status)

		// Should only have indexed posts after the cursor (post06-post09)
		// Not post00-post05 which were already processed
		assert.Equal(t, 4, len(indexedPosts), "Should only index posts after cursor position")
		for _, postID := range indexedPosts {
			assert.True(t, postID >= "post06", "Should not re-index posts before cursor: %s", postID)
		}

		// Verify total processed count includes both previous and new
		assert.Equal(t, int64(10), jobStatus.ProcessedRows)
	})

	t.Run("starts from beginning when no cursor exists", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		// Add a channel and 5 posts
		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)

		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("post%02d", i)
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, now+int64(i*1000), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// Track indexed posts
		var indexedPosts []string
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				docs := args.Get(1).([]embeddings.PostDocument)
				for _, doc := range docs {
					indexedPosts = append(indexedPosts, doc.PostID)
				}
			}).
			Return(nil).Maybe()

		// No cursor exists
		mockClient.On("KVGet", IndexerCursorKey, mock.AnythingOfType("*indexer.Cursor")).
			Return(errors.New("not found"))
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
			CutoffAt:  now + 10000,
		}

		// Run with clearIndex=false but no cursor - should start from beginning
		indexer.runReindexJob(jobStatus, false)

		assert.Equal(t, JobStatusCompleted, jobStatus.Status)
		assert.Equal(t, 5, len(indexedPosts), "Should index all posts when no cursor exists")
	})

	t.Run("saves cursor on failure for later resume", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		// Add a channel and enough posts to require multiple batches (batch size is 100)
		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)

		for i := 0; i < 150; i++ {
			postID := fmt.Sprintf("post%03d", i)
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, now+int64(i*1000), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		mockSearch.On("Clear", mock.Anything).Return(nil)

		// First batch succeeds, second batch fails (after all retries)
		batchCount := 0
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				batchCount++
			}).
			Return(func(ctx context.Context, docs []embeddings.PostDocument) error {
				// First batch succeeds, all subsequent calls fail
				if batchCount > 1 {
					return errors.New("simulated storage failure")
				}
				return nil
			})

		// Track cursor saves
		var savedCursor *Cursor
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", IndexerCursorKey, mock.AnythingOfType("indexer.Cursor")).
			Run(func(args mock.Arguments) {
				c := args.Get(1).(Cursor)
				savedCursor = &c
			}).
			Return(nil).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
			CutoffAt:  now + 200000,
		}

		indexer.runReindexJob(jobStatus, true)

		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Failed to store documents")

		// Cursor should have been saved for resume - pointing to where first batch ended
		assert.NotNil(t, savedCursor, "Cursor should be saved on failure")
		if savedCursor != nil {
			// Cursor should point to approximately where first batch ended (around post099)
			assert.True(t, savedCursor.LastCreateAt > now, "Cursor should have a valid timestamp")
		}
	})
}

// TestMarkOrphanedJobAsFailed tests the automatic marking of orphaned jobs on startup
func TestMarkOrphanedJobAsFailed(t *testing.T) {
	t.Run("marks running job on same node as failed", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		// Get current hostname to match the job's NodeID
		hostname, _ := os.Hostname()

		// Return a running job on this node
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
				status.NodeID = hostname
				status.ProcessedRows = 1000
				status.StartedAt = time.Now().Add(-1 * time.Hour)
			}).
			Return(nil)

		// Expect KVSet to mark as failed
		var savedStatus *JobStatus
		mockClient.On("KVSet", ReindexJobKey, mock.MatchedBy(func(v interface{}) bool {
			status := v.(JobStatus)
			savedStatus = &status
			return status.Status == JobStatusFailed
		})).Return(nil)

		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		err := indexer.MarkOrphanedJobAsFailed()

		require.NoError(t, err)
		require.NotNil(t, savedStatus)
		assert.Equal(t, JobStatusFailed, savedStatus.Status)
		assert.Contains(t, savedStatus.Error, "Job orphaned")
		assert.Contains(t, savedStatus.Error, hostname)
		assert.False(t, savedStatus.CompletedAt.IsZero())
	})

	t.Run("does not mark job running on different node", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		// Return a running job on a DIFFERENT node
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusRunning
				status.NodeID = "different-node-hostname"
				status.ProcessedRows = 1000
			}).
			Return(nil)

		// KVSet should NOT be called
		indexer := New(nil, nil, mockClient, nil, nil, nil)
		err := indexer.MarkOrphanedJobAsFailed()

		require.NoError(t, err)
		mockClient.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
	})

	t.Run("does nothing when no job exists", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found"))

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		err := indexer.MarkOrphanedJobAsFailed()

		require.NoError(t, err)
		mockClient.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
	})

	t.Run("does nothing when job is not running", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		hostname, _ := os.Hostname()

		// Return a completed job on this node
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusCompleted
				status.NodeID = hostname
			}).
			Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		err := indexer.MarkOrphanedJobAsFailed()

		require.NoError(t, err)
		mockClient.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
	})

	t.Run("does nothing when job is failed", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)

		hostname, _ := os.Hostname()

		// Return a failed job on this node
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.Status = JobStatusFailed
				status.NodeID = hostname
			}).
			Return(nil)

		indexer := New(nil, nil, mockClient, nil, nil, nil)
		err := indexer.MarkOrphanedJobAsFailed()

		require.NoError(t, err)
		mockClient.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
	})
}

// TestCatchUpPassHeartbeat tests that catch-up pass updates heartbeat and saves progress
func TestCatchUpPassHeartbeat(t *testing.T) {
	t.Run("catch-up pass updates LastUpdatedAt", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		// Set up cutoff time before posts are created
		cutoffTime := model.GetMillis()

		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)

		// Add posts AFTER the cutoff (these will be processed by catch-up pass)
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("catchup-post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, cutoffTime+1+int64(i), fmt.Sprintf("Catch-up message %d", i))
			require.NoError(t, err)
		}

		mockSearch.On("Clear", mock.Anything).Return(nil)
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil).Maybe()

		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)

		initialTime := time.Now().Add(-time.Hour) // Set initial time in the past
		jobStatus := &JobStatus{
			Status:        JobStatusRunning,
			StartedAt:     time.Now(),
			CutoffAt:      cutoffTime,
			LastUpdatedAt: initialTime,
		}

		indexer.runReindexJob(jobStatus, true)

		assert.Equal(t, JobStatusCompleted, jobStatus.Status)
		// LastUpdatedAt should have been updated during catch-up
		assert.True(t, jobStatus.LastUpdatedAt.After(initialTime), "LastUpdatedAt should be updated during catch-up")
	})

	t.Run("catch-up saves progress every 500 posts", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		// Set cutoffTime well in the past so all catch-up posts have CreateAt values
		// safely below catchUpCutoff (which captures time.Now() during the catch-up pass)
		cutoffTime := model.GetMillis() - 2000

		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)

		// Add 600+ posts after cutoff to trigger progress save during catch-up
		for i := 0; i < 650; i++ {
			postID := fmt.Sprintf("catchup-post%03d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, cutoffTime+1+int64(i), fmt.Sprintf("Catch-up message %d", i))
			require.NoError(t, err)
		}

		mockSearch.On("Clear", mock.Anything).Return(nil)
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil).Maybe()

		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()

		// Track job status saves during catch-up
		catchUpSaveCount := 0
		mockClient.On("KVSet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				// Count saves that occur during catch-up (ProcessedRows > 0 but status still running)
				if status.Status == JobStatusRunning && status.ProcessedRows > 0 {
					catchUpSaveCount++
				}
			}).
			Return(nil).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
			CutoffAt:  cutoffTime,
		}

		indexer.runReindexJob(jobStatus, true)

		assert.Equal(t, JobStatusCompleted, jobStatus.Status)
		// Should have saved progress at least once during catch-up (after 500 posts)
		assert.Greater(t, catchUpSaveCount, 0, "Should save progress during catch-up pass")
	})
}

// TestCatchUpFailureHandling tests that catch-up failure marks job as failed
func TestCatchUpFailureHandling(t *testing.T) {
	t.Run("catch-up failure marks job as failed", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}

		cutoffTime := model.GetMillis()

		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)

		// Add posts AFTER the cutoff (catch-up posts)
		for i := 0; i < 5; i++ {
			postID := fmt.Sprintf("catchup-post%d", i)
			_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, cutoffTime+1+int64(i), fmt.Sprintf("Catch-up message %d", i))
			require.NoError(t, err)
		}

		mockSearch.On("Clear", mock.Anything).Return(nil)

		// Track whether we're in catch-up phase (no posts before cutoff means first Store is catch-up)
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(errors.New("simulated catch-up failure"))

		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, nil)

		jobStatus := &JobStatus{
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
			CutoffAt:  cutoffTime,
		}

		indexer.runReindexJob(jobStatus, true)

		// Job should be failed, not completed
		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Catch-up pass failed")
	})
}

// TestResumePreservation tests that resume preserves CutoffAt and TotalRows
func TestResumePreservation(t *testing.T) {
	t.Run("resume preserves CutoffAt and TotalRows from failed job", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}
		mockMutexAPI := &plugintest.API{}

		// Create a failed job with specific CutoffAt and TotalRows
		originalCutoffAt := int64(1700000000000)
		originalTotalRows := int64(5000)
		originalProcessedRows := int64(2500)

		failedJobStatus := JobStatus{
			Status:        JobStatusFailed,
			CutoffAt:      originalCutoffAt,
			TotalRows:     originalTotalRows,
			ProcessedRows: originalProcessedRows,
		}

		// Setup mocks
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				*status = failedJobStatus
			}).
			Return(nil)

		// Capture the new job status that gets saved
		var savedJobStatus *JobStatus
		mockClient.On("KVSet", ReindexJobKey, mock.AnythingOfType("indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(JobStatus)
				savedJobStatus = &status
			}).
			Return(nil)
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()
		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, mockMutexAPI)

		// Start resume (clearIndex=false)
		_, err := indexer.StartReindexJob(false)
		require.NoError(t, err)

		// Verify the new job preserved the original values
		require.NotNil(t, savedJobStatus)
		assert.Equal(t, originalCutoffAt, savedJobStatus.CutoffAt, "CutoffAt should be preserved from failed job")
		assert.Equal(t, originalTotalRows, savedJobStatus.TotalRows, "TotalRows should be preserved from failed job")
		assert.Equal(t, originalProcessedRows, savedJobStatus.ProcessedRows, "ProcessedRows should be preserved from failed job")
	})

	t.Run("fresh reindex calculates new CutoffAt and TotalRows", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		mockClient := mocks.NewMockClient(t)
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockBots := &bots.MMBots{}
		mockMutexAPI := &plugintest.API{}

		// Add posts to the database (use past timestamps to avoid race with cutoff capture)
		now := model.GetMillis() - 1000
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		for i := 0; i < 10; i++ {
			postID := fmt.Sprintf("post%d", i)
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				postID, now+int64(i), fmt.Sprintf("Message %d", i))
			require.NoError(t, err)
		}

		// No previous job exists
		mockSearch.On("Clear", mock.Anything).Return(nil).Maybe()
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(errors.New("not found"))

		// Capture the new job status
		var savedJobStatus *JobStatus
		mockClient.On("KVSet", ReindexJobKey, mock.AnythingOfType("indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(JobStatus)
				savedJobStatus = &status
			}).
			Return(nil)
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(errors.New("not found")).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()
		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		indexer := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, mockBots, db, mockMutexAPI)

		// Start fresh reindex (clearIndex=true)
		_, err = indexer.StartReindexJob(true)
		require.NoError(t, err)

		// Verify new values were calculated
		require.NotNil(t, savedJobStatus)
		assert.Greater(t, savedJobStatus.CutoffAt, int64(0), "CutoffAt should be set for fresh reindex")
		assert.Equal(t, int64(10), savedJobStatus.TotalRows, "TotalRows should be calculated for fresh reindex")
		assert.Equal(t, int64(0), savedJobStatus.ProcessedRows, "ProcessedRows should be 0 for fresh reindex")
	})
}

func TestRunDataRetention(t *testing.T) {
	tests := []struct {
		name          string
		getSearch     func() embeddings.EmbeddingSearch
		nowTime       int64
		batchSize     int64
		setupMock     func(*embeddingsmocks.MockEmbeddingSearch)
		expectedCount int64
		expectError   bool
	}{
		{
			name:          "nil getSearch returns zero",
			getSearch:     nil,
			nowTime:       1000,
			batchSize:     100,
			expectedCount: 0,
			expectError:   false,
		},
		{
			name: "getSearch returns nil returns zero",
			getSearch: func() embeddings.EmbeddingSearch {
				return nil
			},
			nowTime:       1000,
			batchSize:     100,
			expectedCount: 0,
			expectError:   false,
		},
		{
			name:      "successful deletion returns count",
			nowTime:   1000,
			batchSize: 100,
			setupMock: func(m *embeddingsmocks.MockEmbeddingSearch) {
				m.On("DeleteOrphaned", mock.Anything, int64(1000), int64(100)).Return(int64(42), nil)
			},
			expectedCount: 42,
			expectError:   false,
		},
		{
			name:      "deletion error is propagated",
			nowTime:   1000,
			batchSize: 100,
			setupMock: func(m *embeddingsmocks.MockEmbeddingSearch) {
				m.On("DeleteOrphaned", mock.Anything, int64(1000), int64(100)).Return(int64(0), errors.New("db error"))
			},
			expectedCount: 0,
			expectError:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			getSearch := tc.getSearch
			if getSearch == nil && tc.setupMock != nil {
				mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
				tc.setupMock(mockSearch)
				getSearch = func() embeddings.EmbeddingSearch { return mockSearch }
			}

			indexer := New(getSearch, nil, nil, nil, nil, nil)
			count, err := indexer.RunDataRetention(context.Background(), tc.nowTime, tc.batchSize)

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.expectedCount, count)
		})
	}
}

func TestGetModelInfoFromConfig(t *testing.T) {
	tests := []struct {
		name         string
		configGetter func() embeddings.EmbeddingSearchConfig
		expected     *ModelInfo
	}{
		{
			name:         "nil configGetter returns nil",
			configGetter: nil,
			expected:     nil,
		},
		{
			name: "valid config returns correct model info",
			configGetter: func() embeddings.EmbeddingSearchConfig {
				return embeddings.EmbeddingSearchConfig{
					EmbeddingProvider: embeddings.UpstreamConfig{
						Type: "openai",
						Parameters: func() []byte {
							return []byte(`{"embeddingModel": "text-embedding-3-small"}`)
						}(),
					},
					Dimensions: 1536,
				}
			},
			expected: &ModelInfo{
				ProviderType: "openai",
				ModelName:    "text-embedding-3-small",
				Dimensions:   1536,
			},
		},
		{
			name: "config with no parameters returns empty model name",
			configGetter: func() embeddings.EmbeddingSearchConfig {
				return embeddings.EmbeddingSearchConfig{
					EmbeddingProvider: embeddings.UpstreamConfig{
						Type: "bedrock",
					},
					Dimensions: 768,
				}
			},
			expected: &ModelInfo{
				ProviderType: "bedrock",
				ModelName:    "",
				Dimensions:   768,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			indexer := New(nil, tc.configGetter, nil, nil, nil, nil)
			result := indexer.getModelInfoFromConfig()

			if tc.expected == nil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tc.expected.ProviderType, result.ProviderType)
				assert.Equal(t, tc.expected.ModelName, result.ModelName)
				assert.Equal(t, tc.expected.Dimensions, result.Dimensions)
			}
		})
	}
}
