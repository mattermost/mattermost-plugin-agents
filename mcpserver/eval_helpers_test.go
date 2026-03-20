// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mattermost/mattermost-plugin-ai/chunking"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/testhelpers"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-ai/postgres"
	"github.com/mattermost/mattermost-plugin-ai/search"
	"github.com/mattermost/mattermost/server/public/model"
)

// evalChannelData holds the seeded test data for eval tests.
type evalChannelData struct {
	team    *model.Team
	channel *model.Channel
	// designChannel is a second channel with unrelated content for cross-channel search testing.
	designChannel *model.Channel
	alice         *model.User
	bob           *model.User
	charlie       *model.User
	// bobTimelinePost is Bob's "What's the timeline?" post, used as a thread root.
	bobTimelinePost *model.Post
}

// extractMCPText extracts all text content from an MCP tool result.
func extractMCPText(result *gomcp.CallToolResult) string {
	var text string
	for _, c := range result.Content {
		if tc, ok := c.(*gomcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

// seedChannelConversation creates a realistic multi-user conversation in a channel
// with known content that rubrics can check against.
func seedChannelConversation(t *testing.T, serverURL, adminToken string) *evalChannelData {
	t.Helper()

	ctx := context.Background()
	adminClient := model.NewAPIv4Client(serverURL)
	adminClient.SetToken(adminToken)

	// Create team and channel
	team := testhelpers.CreateTestTeam(t, adminClient, "eval-team", "Eval Team")
	channel := testhelpers.CreateTestChannel(t, adminClient, team.Id, "migration-planning", "Migration Planning")

	// Create users with known passwords so we can log in as them
	password := "EvalTest123!"
	alice := testhelpers.CreateTestUser(t, adminClient, "alice.eval", "alice.eval@example.com", password)
	bob := testhelpers.CreateTestUser(t, adminClient, "bob.eval", "bob.eval@example.com", password)
	charlie := testhelpers.CreateTestUser(t, adminClient, "charlie.eval", "charlie.eval@example.com", password)

	// Add users to team and channel
	for _, u := range []*model.User{alice, bob, charlie} {
		testhelpers.AddUserToTeam(t, adminClient, team.Id, u.Id)
		testhelpers.AddUserToChannel(t, adminClient, channel.Id, u.Id)
	}

	// Create per-user clients
	aliceClient := model.NewAPIv4Client(serverURL)
	_, _, err := aliceClient.Login(ctx, alice.Username, password)
	require.NoError(t, err, "Failed to login as alice")

	bobClient := model.NewAPIv4Client(serverURL)
	_, _, err = bobClient.Login(ctx, bob.Username, password)
	require.NoError(t, err, "Failed to login as bob")

	charlieClient := model.NewAPIv4Client(serverURL)
	_, _, err = charlieClient.Login(ctx, charlie.Username, password)
	require.NoError(t, err, "Failed to login as charlie")

	// Post messages in order to create a realistic conversation.
	// Alice proposes PostgreSQL migration
	_, _, err = aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "I've been evaluating our database options and I think we should migrate from MySQL to PostgreSQL. The JSON support and extension ecosystem are much better for our use case.",
	})
	require.NoError(t, err)

	// Bob asks about timeline
	bobTimelinePost, _, err := bobClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "What's the timeline for this? We have the Q3 feature freeze coming up.",
	})
	require.NoError(t, err)

	// Alice replies in-thread to Bob's question
	_, _, err = aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		RootId:    bobTimelinePost.Id,
		Message:   "I'm targeting next sprint for the schema migration, then two weeks of testing before we cut over.",
	})
	require.NoError(t, err)

	// Charlie raises concern about data volume
	_, _, err = charlieClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "One concern — we have about 500GB of data in the current MySQL instance. Have we estimated the downtime for the migration?",
	})
	require.NoError(t, err)

	// Bob @mentions alice asking about rollback plan
	_, _, err = bobClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "@alice.eval what's the rollback plan if something goes wrong during the migration?",
	})
	require.NoError(t, err)

	// Alice describes rollback approach
	_, _, err = aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "We'll keep the MySQL instance running in read-only mode during the cutover. If anything fails, we can switch back within minutes. I've also set up continuous replication as a safety net.",
	})
	require.NoError(t, err)

	// Charlie shares monitoring docs link
	_, _, err = charlieClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "Good plan. I've put together monitoring dashboards for tracking the migration progress: https://wiki.example.com/pg-migration-monitoring",
	})
	require.NoError(t, err)

	// Bob confirms he'll review
	_, _, err = bobClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "Sounds solid. I'll review the migration script this week and flag any issues before we start.",
	})
	require.NoError(t, err)

	// Create a second channel with unrelated content for cross-channel search testing
	designChannel := testhelpers.CreateTestChannel(t, adminClient, team.Id, "design-review", "Design Review")
	for _, u := range []*model.User{alice, bob} {
		testhelpers.AddUserToChannel(t, adminClient, designChannel.Id, u.Id)
	}

	_, _, err = aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: designChannel.Id,
		Message:   "I've put together the initial Figma mockups for the new dashboard redesign. The layout focuses on key metrics visibility.",
	})
	require.NoError(t, err)

	_, _, err = bobClient.CreatePost(ctx, &model.Post{
		ChannelId: designChannel.Id,
		Message:   "The design system components look great. I think we should use the card-based layout for the analytics section.",
	})
	require.NoError(t, err)

	return &evalChannelData{
		team:            team,
		channel:         channel,
		designChannel:   designChannel,
		alice:           alice,
		bob:             bob,
		charlie:         charlie,
		bobTimelinePost: bobTimelinePost,
	}
}

// embeddingSearchService wraps a real embeddings.EmbeddingSearch to implement tools.SemanticSearchService.
// This bridges the real PGVector+CompositeSearch pipeline to the MCP server's search interface.
// It enriches results with channel/user metadata from the Mattermost API, mirroring what
// search.Search.enrichResults() does in production.
type embeddingSearchService struct {
	search    embeddings.EmbeddingSearch
	serverURL string
	token     string
}

func (s *embeddingSearchService) Enabled() bool { return s.search != nil }

func (s *embeddingSearchService) Search(ctx context.Context, query string, opts search.Options) ([]search.RAGResult, error) {
	results, err := s.search.Search(ctx, query, embeddings.SearchOptions{
		Limit:     opts.Limit,
		Offset:    opts.Offset,
		TeamID:    opts.TeamID,
		ChannelID: opts.ChannelID,
		UserID:    opts.UserID,
	})
	if err != nil {
		return nil, err
	}

	// Enrich results with channel/user metadata (mirrors search.Search.enrichResults)
	client := model.NewAPIv4Client(s.serverURL)
	client.SetToken(s.token)

	ragResults := make([]search.RAGResult, len(results))
	for i, r := range results {
		var channelName, username string
		if ch, _, chErr := client.GetChannel(ctx, r.Document.ChannelID, ""); chErr == nil {
			channelName = ch.DisplayName
		}
		if u, _, uErr := client.GetUser(ctx, r.Document.UserID, ""); uErr == nil {
			username = u.Username
		}

		ragResults[i] = search.RAGResult{
			Index:       i + 1,
			PostID:      r.Document.PostID,
			ChannelID:   r.Document.ChannelID,
			ChannelName: channelName,
			UserID:      r.Document.UserID,
			Username:    username,
			Content:     r.Document.Content,
			Score:       r.Score,
		}
	}
	return ragResults, nil
}

// setupPGVectorContainer starts a pgvector PostgreSQL container and returns a connection.
// The container is automatically cleaned up when the test finishes.
func setupPGVectorContainer(t *testing.T) *sqlx.DB {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pgContainer, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg15",
		tcpostgres.WithDatabase("embeddings_test"),
		tcpostgres.WithUsername("user"),
		tcpostgres.WithPassword("pass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second)),
	)
	require.NoError(t, err, "Failed to start pgvector container")

	t.Cleanup(func() {
		if termErr := pgContainer.Terminate(context.Background()); termErr != nil {
			t.Logf("Failed to terminate pgvector container: %v", termErr)
		}
	})

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get pgvector DSN")

	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err, "Failed to connect to pgvector")

	// Enable pgvector extension
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS vector")
	require.NoError(t, err, "Failed to enable pgvector extension")

	// Create stub Mattermost tables required by PGVector's foreign keys and access control
	// (same schema as embeddings/integration_test.go)
	for _, tableSQL := range []string{
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
	} {
		_, err = db.Exec(tableSQL)
		require.NoError(t, err, "Failed to create stub table")
	}

	return db
}

// setupEmbeddingSearch creates a real CompositeSearch with PGVector + mock embedding provider,
// indexes the test posts, and returns a SemanticSearchService ready for the MCP server.
func setupEmbeddingSearch(t *testing.T, data *evalChannelData, serverURL, adminToken string) tools.SemanticSearchService {
	t.Helper()

	db := setupPGVectorContainer(t)
	ctx := context.Background()

	const dimensions = 64 // Small for test speed

	// Create real CompositeSearch: PGVector vector store + mock embedding provider
	provider := embeddings.NewMockEmbeddingProvider(dimensions)
	vectorStore, err := postgres.NewPGVector(db, postgres.PGVectorConfig{Dimensions: dimensions})
	require.NoError(t, err, "Failed to create PGVector store")

	compositeSearch := embeddings.NewCompositeSearch(vectorStore, provider, chunking.Options{
		ChunkSize:        500,
		ChunkOverlap:     50,
		ChunkingStrategy: "sentences",
	})

	// Fetch all posts from Mattermost and index them into PGVector
	client := model.NewAPIv4Client(serverURL)
	client.SetToken(adminToken)

	for _, ch := range []*model.Channel{data.channel, data.designChannel} {
		// Register channel in pgvector DB stub (required for access control)
		_, err = db.Exec(
			"INSERT INTO Channels (Id, Name, DisplayName, Type, TeamId, DeleteAt) VALUES ($1, $2, $3, $4, $5, 0) ON CONFLICT (Id) DO NOTHING",
			ch.Id, ch.Name, ch.DisplayName, string(ch.Type), ch.TeamId)
		require.NoError(t, err)

		var posts *model.PostList
		posts, _, err = client.GetPostsForChannel(ctx, ch.Id, 0, 100, "", false, false)
		require.NoError(t, err)

		var docs []embeddings.PostDocument
		for _, post := range posts.Posts {
			if post.Message == "" {
				continue
			}

			// Insert post into stub Posts table (foreign key for llm_posts_embeddings)
			_, err = db.Exec(
				"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, UserId, ChannelId, Type) VALUES ($1, $2, 0, $3, $4, $5, '') ON CONFLICT (Id) DO NOTHING",
				post.Id, post.CreateAt, post.Message, post.UserId, post.ChannelId)
			require.NoError(t, err)

			// Register user as channel member for search access control
			_, _ = db.Exec(
				"INSERT INTO ChannelMembers (ChannelId, UserId) VALUES ($1, $2) ON CONFLICT (ChannelId, UserId) DO NOTHING",
				ch.Id, post.UserId)

			docs = append(docs, embeddings.PostDocument{
				PostID:    post.Id,
				CreateAt:  post.CreateAt,
				TeamID:    ch.TeamId,
				ChannelID: ch.Id,
				UserID:    post.UserId,
				Content:   post.Message,
			})
		}

		if len(docs) > 0 {
			err = compositeSearch.Store(ctx, docs)
			require.NoError(t, err, "Failed to index posts for channel %s", ch.DisplayName)
		}
	}

	// Verify indexing
	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM llm_posts_embeddings")
	require.NoError(t, err)
	t.Logf("Indexed %d documents into PGVector", count)

	return &embeddingSearchService{
		search:    compositeSearch,
		serverURL: serverURL,
		token:     adminToken,
	}
}

// agenticEvalSetup holds the components needed for agentic flow evals.
type agenticEvalSetup struct {
	wrappedLLM   llm.LanguageModel
	llmContext   *llm.Context
	allToolNames []string
}

// setupAgenticEval builds the common infrastructure for agentic flow evals:
// MCP tools as llm.Tool, ToolStore, AutoRunToolsWrapper, and populated llm.Context.
func setupAgenticEval(t *testing.T, e *evals.EvalT, suite *TestSuite, data *evalChannelData) *agenticEvalSetup {
	t.Helper()

	mcpTools := mcpToolsToLLMTools(t, suite.mcpServer.GetMCPServer())
	require.NotEmpty(t, mcpTools, "Should have MCP tools")

	allToolNames := make([]string, len(mcpTools))
	for i, tool := range mcpTools {
		allToolNames[i] = tool.Name
	}

	toolStore := llm.NewToolStore(nil, false)
	toolStore.AddTools(mcpTools)

	llmContext := llm.NewContext()
	llmContext.Tools = toolStore
	llmContext.RequestingUser = data.alice
	llmContext.Channel = &model.Channel{Type: model.ChannelTypeDirect}
	llmContext.Team = data.team
	llmContext.ServerName = "Eval Server"
	llmContext.CompanyName = "Eval Corp"
	llmContext.BotName = "AI Assistant"
	llmContext.BotUsername = "ai-bot"
	llmContext.BotModel = "eval-model"

	wrappedLLM := llm.NewAutoRunToolsWrapper(e.LLM)

	return &agenticEvalSetup{
		wrappedLLM:   wrappedLLM,
		llmContext:   llmContext,
		allToolNames: allToolNames,
	}
}

// mcpToolsToLLMTools converts MCP server tools into llm.Tool instances with resolvers
// that call through the MCP protocol. This mirrors the production pattern in
// mcp/user_clients.go:201 (createToolResolver).
func mcpToolsToLLMTools(t *testing.T, mcpServer *gomcp.Server) []llm.Tool {
	t.Helper()

	session := testhelpers.CreateTestMCPSession(t, mcpServer)

	ctx := context.Background()
	toolsList, err := session.ListTools(ctx, nil)
	require.NoError(t, err, "Failed to list MCP tools")

	var tools []llm.Tool
	for _, tool := range toolsList.Tools {
		toolName := tool.Name // capture for closure
		tools = append(tools, llm.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      tool.InputSchema,
			Resolver: func(_ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				// Same pattern as production createToolResolver (mcp/user_clients.go:201)
				var args map[string]any
				if err := argsGetter(&args); err != nil {
					return "", err
				}
				result, err := session.CallTool(ctx, &gomcp.CallToolParams{
					Name:      toolName,
					Arguments: args,
				})
				if err != nil {
					return "", err
				}
				// Extract text content
				var text string
				for _, c := range result.Content {
					if tc, ok := c.(*gomcp.TextContent); ok {
						text += tc.Text
					}
				}
				return text, nil
			},
		})
	}
	return tools
}
