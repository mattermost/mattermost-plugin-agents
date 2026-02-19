// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-ai/bifrost"
	"github.com/mattermost/mattermost-plugin-ai/chunking"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/postgres"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These eval tests require:
// 1. GOEVALS=1 environment variable
// 2. OPENAI_API_KEY environment variable
// 3. PostgreSQL with pgvector extension

var rootDSN = "postgres://mmuser:mostest@localhost:5432/postgres?sslmode=disable"

func skipIfNotEval(t *testing.T) {
	t.Helper()
	if os.Getenv("GOEVALS") == "" {
		t.Skip("Skipping eval test. Set GOEVALS=1 to run.")
	}
}

func getOpenAIKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("Skipping: OPENAI_API_KEY not set")
	}
	return key
}

func evalTestDB(t *testing.T) *sqlx.DB {
	t.Helper()

	rootDB, err := sqlx.Connect("postgres", rootDSN)
	if err != nil {
		t.Skipf("Skipping: PostgreSQL not available: %v", err)
	}
	defer rootDB.Close()

	// Check if pgvector extension is available
	var hasVector bool
	err = rootDB.Get(&hasVector, "SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE name = 'vector')")
	if err != nil || !hasVector {
		t.Skip("Skipping: pgvector extension not available")
	}

	// Create a unique database name
	dbName := fmt.Sprintf("eval_search_%d", model.GetMillis())

	_, err = rootDB.Exec("CREATE DATABASE " + dbName)
	require.NoError(t, err, "Failed to create test database")

	testDSN := fmt.Sprintf("postgres://mmuser:mostest@localhost:5432/%s?sslmode=disable", dbName)
	db, err := sqlx.Connect("postgres", testDSN)
	if err != nil {
		_, _ = rootDB.Exec("DROP DATABASE " + dbName)
		require.NoError(t, err)
	}

	t.Setenv("EVAL_TEST_DB", dbName)

	// Enable pgvector extension
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS vector")
	require.NoError(t, err)

	// Create mock Mattermost tables
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
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		db.Close()
		rootDB2, _ := sqlx.Connect("postgres", rootDSN)
		if rootDB2 != nil {
			_, _ = rootDB2.Exec("DROP DATABASE " + dbName)
			rootDB2.Close()
		}
	})

	return db
}

func addEvalTestChannel(t *testing.T, db *sqlx.DB, channelID, teamID, channelType string, memberUserIDs []string) {
	_, err := db.Exec(
		"INSERT INTO Channels (Id, Name, DisplayName, Type, TeamId, DeleteAt) VALUES ($1, $2, $3, $4, $5, 0)",
		channelID, "channel-"+channelID, "Channel "+channelID, channelType, teamID)
	require.NoError(t, err)

	for _, userID := range memberUserIDs {
		_, err := db.Exec(
			"INSERT INTO ChannelMembers (ChannelId, UserId) VALUES ($1, $2)",
			channelID, userID)
		require.NoError(t, err)
	}
}

func addEvalTestPost(t *testing.T, db *sqlx.DB, postID, userID, channelID, message string, createAt int64) {
	_, err := db.Exec(
		"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, UserId, ChannelId, Type) VALUES ($1, $2, 0, $3, $4, $5, '')",
		postID, createAt, message, userID, channelID)
	require.NoError(t, err)
}

// createRealEmbeddingSearch creates a CompositeSearch with real OpenAI embeddings and PGVector
func createRealEmbeddingSearch(t *testing.T, db *sqlx.DB, apiKey string) embeddings.EmbeddingSearch {
	t.Helper()

	// Use text-embedding-3-small for cost efficiency in tests
	const dimensions = 1536
	const embeddingModel = "text-embedding-3-small"

	provider, err := bifrost.NewEmbeddingProvider(bifrost.EmbeddingConfig{
		Provider:   schemas.OpenAI,
		APIKey:     apiKey,
		Model:      embeddingModel,
		Dimensions: dimensions,
	})
	require.NoError(t, err)

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

// TestSemanticSearchRelevance tests that semantically similar content ranks higher
// This test uses real OpenAI embeddings to verify semantic search quality.
func TestSemanticSearchRelevance(t *testing.T) {
	skipIfNotEval(t)
	apiKey := getOpenAIKey(t)
	db := evalTestDB(t)

	search := createRealEmbeddingSearch(t, db, apiKey)
	ctx := context.Background()

	addEvalTestChannel(t, db, "channel1", "team1", "O", []string{"user1"})

	now := model.GetMillis()

	// Create posts with varying semantic relevance to programming languages
	testPosts := []struct {
		id       string
		message  string
		category string // for verification
	}{
		// Highly relevant to "programming language"
		{"prog1", "Python is a versatile programming language used for data science and web development", "programming"},
		{"prog2", "JavaScript is the most popular programming language for building web applications", "programming"},
		{"prog3", "Rust provides memory safety guarantees without garbage collection in systems programming", "programming"},

		// Moderately relevant (software/tech but not languages)
		{"tech1", "Docker containers help developers deploy applications consistently across environments", "tech"},
		{"tech2", "Git version control is essential for collaborative software development teams", "tech"},

		// Unrelated topics
		{"food1", "The best chocolate chip cookies require brown butter and sea salt", "food"},
		{"weather1", "Today's forecast shows sunny skies with temperatures reaching 75 degrees", "weather"},
		{"sports1", "The championship game went into overtime with a thrilling finish", "sports"},
	}

	var docs []embeddings.PostDocument
	for i, p := range testPosts {
		addEvalTestPost(t, db, p.id, "user1", "channel1", p.message, now+int64(i*1000))
		docs = append(docs, embeddings.PostDocument{
			PostID:    p.id,
			CreateAt:  now + int64(i*1000),
			TeamID:    "team1",
			ChannelID: "channel1",
			UserID:    "user1",
			Content:   p.message,
		})
	}

	// Store all documents (this calls the real OpenAI API)
	t.Log("Indexing documents with real OpenAI embeddings...")
	err := search.Store(ctx, docs)
	require.NoError(t, err)

	// Search for programming language content
	t.Log("Searching for 'programming language for software development'...")
	results, err := search.Search(ctx, "programming language for software development", embeddings.SearchOptions{
		Limit:  len(testPosts),
		UserID: "user1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, results)

	// Log results
	t.Log("Search results ordered by relevance:")
	for i, result := range results {
		t.Logf("%d. [%.4f] %s: %s", i+1, result.Score, result.Document.PostID, result.Document.Content[:min(80, len(result.Document.Content))]+"...")
	}

	// Verify that programming-related posts appear in top 3
	top3IDs := make(map[string]bool)
	for i := 0; i < min(3, len(results)); i++ {
		top3IDs[results[i].Document.PostID] = true
	}

	programmingInTop3 := 0
	for _, p := range testPosts {
		if p.category == "programming" && top3IDs[p.id] {
			programmingInTop3++
		}
	}

	assert.GreaterOrEqual(t, programmingInTop3, 2, "At least 2 of the 3 programming posts should be in top 3 results")

	// Verify unrelated posts are ranked lower
	for i, result := range results {
		for _, p := range testPosts {
			if result.Document.PostID == p.id && (p.category == "food" || p.category == "weather" || p.category == "sports") {
				assert.Greater(t, i, 2, "Unrelated post '%s' should not be in top 3", p.id)
			}
		}
	}
}

// TestSemanticSearchDifferentQueries tests that different queries return appropriate results
func TestSemanticSearchDifferentQueries(t *testing.T) {
	skipIfNotEval(t)
	apiKey := getOpenAIKey(t)
	db := evalTestDB(t)

	search := createRealEmbeddingSearch(t, db, apiKey)
	ctx := context.Background()

	addEvalTestChannel(t, db, "channel1", "team1", "O", []string{"user1"})

	now := model.GetMillis()

	// Create diverse posts
	testPosts := []struct {
		id      string
		message string
		topic   string
	}{
		{"db1", "PostgreSQL database performance tuning and query optimization strategies", "database"},
		{"db2", "MongoDB is a NoSQL database that stores data in flexible JSON-like documents", "database"},
		{"ml1", "Neural networks learn patterns from training data through backpropagation", "ml"},
		{"ml2", "Random forests combine multiple decision trees for better predictions", "ml"},
		{"devops1", "Kubernetes orchestrates containerized applications across clusters", "devops"},
		{"devops2", "CI/CD pipelines automate testing and deployment of software", "devops"},
	}

	var docs []embeddings.PostDocument
	for i, p := range testPosts {
		addEvalTestPost(t, db, p.id, "user1", "channel1", p.message, now+int64(i*1000))
		docs = append(docs, embeddings.PostDocument{
			PostID:    p.id,
			CreateAt:  now + int64(i*1000),
			TeamID:    "team1",
			ChannelID: "channel1",
			UserID:    "user1",
			Content:   p.message,
		})
	}

	t.Log("Indexing documents...")
	err := search.Store(ctx, docs)
	require.NoError(t, err)

	// Test different queries
	queries := []struct {
		query         string
		expectedTopic string
	}{
		{"database query performance SQL", "database"},
		{"machine learning neural network AI", "ml"},
		{"container deployment kubernetes docker", "devops"},
	}

	for _, q := range queries {
		t.Run(q.query, func(t *testing.T) {
			results, err := search.Search(ctx, q.query, embeddings.SearchOptions{
				Limit:  3,
				UserID: "user1",
			})
			require.NoError(t, err)
			require.NotEmpty(t, results)

			t.Logf("Query: %s", q.query)
			for i, result := range results {
				t.Logf("  %d. [%.4f] %s", i+1, result.Score, result.Document.PostID)
			}

			// Check if the top result matches expected topic
			topResultID := results[0].Document.PostID
			var topResultTopic string
			for _, p := range testPosts {
				if p.id == topResultID {
					topResultTopic = p.topic
					break
				}
			}

			assert.Equal(t, q.expectedTopic, topResultTopic,
				"Top result for '%s' should be about %s, got %s",
				q.query, q.expectedTopic, topResultTopic)
		})
	}
}

// TestSemanticSearchWithFilters tests that filters work correctly with semantic search
func TestSemanticSearchWithFilters(t *testing.T) {
	skipIfNotEval(t)
	apiKey := getOpenAIKey(t)
	db := evalTestDB(t)

	search := createRealEmbeddingSearch(t, db, apiKey)
	ctx := context.Background()

	// Create two channels
	addEvalTestChannel(t, db, "channel1", "team1", "O", []string{"user1"})
	addEvalTestChannel(t, db, "channel2", "team1", "O", []string{"user1"})

	now := model.GetMillis()

	// Create similar posts in different channels and at different times
	testPosts := []struct {
		id        string
		channelID string
		message   string
		offset    int64
	}{
		{"ch1_old", "channel1", "Python programming language basics for beginners", 0},
		{"ch1_new", "channel1", "Advanced Python techniques for experienced developers", 10000},
		{"ch2_old", "channel2", "Python data science and machine learning applications", 0},
		{"ch2_new", "channel2", "Python web development with Django and Flask", 10000},
	}

	var docs []embeddings.PostDocument
	for _, p := range testPosts {
		addEvalTestPost(t, db, p.id, "user1", p.channelID, p.message, now+p.offset)
		docs = append(docs, embeddings.PostDocument{
			PostID:    p.id,
			CreateAt:  now + p.offset,
			TeamID:    "team1",
			ChannelID: p.channelID,
			UserID:    "user1",
			Content:   p.message,
		})
	}

	t.Log("Indexing documents...")
	err := search.Store(ctx, docs)
	require.NoError(t, err)

	// Search with channel filter
	t.Run("channel filter", func(t *testing.T) {
		results, err := search.Search(ctx, "Python programming", embeddings.SearchOptions{
			Limit:     10,
			UserID:    "user1",
			ChannelID: "channel1",
		})
		require.NoError(t, err)

		for _, r := range results {
			assert.Equal(t, "channel1", r.Document.ChannelID, "All results should be from channel1")
		}
		assert.Len(t, results, 2, "Should find 2 posts in channel1")
	})

	// Search with time filter
	t.Run("time filter", func(t *testing.T) {
		results, err := search.Search(ctx, "Python programming", embeddings.SearchOptions{
			Limit:        10,
			UserID:       "user1",
			CreatedAfter: now + 5000,
		})
		require.NoError(t, err)

		for _, r := range results {
			assert.Greater(t, r.Document.CreateAt, now+5000, "All results should be after filter time")
		}
		assert.Len(t, results, 2, "Should find 2 newer posts")
	})
}
