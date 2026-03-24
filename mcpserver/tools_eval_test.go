// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost/server/public/model"
)

// runAgenticFlowEval is a helper that runs an agentic flow eval with the given prompt, rubrics,
// and a list of tools the LLM is required to have called.
func runAgenticFlowEval(e *evals.EvalT, suite *TestSuite, requestingUser *model.User, team *model.Team, userMessage string, rubrics []string, requiredTools []string) string {
	e.Helper()

	setup := setupAgenticEval(e.T, e, suite, requestingUser, team)

	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(e.T, err, "Failed to load prompts")

	posts, err := conversations.BuildNewConversationPosts(promptsObj, setup.llmContext, llm.Post{
		Role:    llm.PostRoleUser,
		Message: userMessage,
	})
	require.NoError(e.T, err, "Failed to build conversation posts")

	result, err := setup.wrappedLLM.ChatCompletion(llm.CompletionRequest{
		Posts:     posts,
		Context:   setup.llmContext,
		Operation: llm.OperationConversation,
	}, llm.WithAutoRunTools(setup.allToolNames))
	require.NoError(e.T, err, "ChatCompletion should succeed")

	response, err := result.ReadAll()
	require.NoError(e.T, err, "ReadAll should succeed")
	require.NotEmpty(e.T, response, "Response should not be empty")

	e.Logf("LLM response:\n%s", response)

	// Assert that each required tool was actually called (not just that the LLM claims it was)
	calledTools := setup.logger.CalledTools()
	for _, requiredTool := range requiredTools {
		found := false
		for _, called := range calledTools {
			if called == requiredTool {
				found = true
				break
			}
		}
		assert.True(e.T, found, "Required tool %q was not called by the LLM (called tools: %v)", requiredTool, calledTools)
	}

	for _, rubric := range rubrics {
		evals.LLMRubricT(e, rubric, response)
	}

	return response
}

// ---------------------------------------------------------------------------
// Individual Tool Evals
// ---------------------------------------------------------------------------

// TestReadChannelOutputQualityEval tests that read_channel output is useful for LLM consumption.
func TestReadChannelOutputQualityEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	rubrics := []string{
		"The output contains the username or author for each message",
		"The output contains timestamps or time references for messages",
		"An LLM could determine the chronological order of messages from this output",
		"The output identifies which messages are threaded replies vs top-level messages",
		"The output contains a message proposing a migration from MySQL to PostgreSQL",
		"The output contains a question about timeline and the Q3 feature freeze",
		"The output contains a reply mentioning next sprint for the schema migration",
		"The output contains a concern about 500GB of data and downtime estimation",
		// Negative rubrics
		"The output does not contain raw JSON objects or unformatted API response structures",
		"The output does not include posts from any channel other than the Migration Planning channel",
	}

	evals.Run(t, "read_channel output quality", func(e *evals.EvalT) {
		result, err := executeToolWithMCP(e.T, suite, "read_channel", map[string]interface{}{
			"channel_id": data.channel.Id,
			"limit":      20,
		})
		require.NoError(e.T, err, "read_channel should succeed")

		text := extractMCPText(result)
		require.NotEmpty(e.T, text, "read_channel should return text content")
		e.Logf("read_channel output:\n%s", text)

		for _, rubric := range rubrics {
			evals.LLMRubricT(e, rubric, text)
		}
	})
}

// TestSearchPostsOutputQualityEval tests that search_posts output is parseable and relevant.
// Runs two sub-tests: keyword search only, and keyword + semantic search.
func TestSearchPostsOutputQualityEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	rubrics := []string{
		"Each search result includes the author username",
		"Each search result includes the post message content",
		"Each search result includes a Post ID that could be used in follow-up tool calls",
		"The results contain posts about database migration or PostgreSQL",
		// Negative rubric
		"The output does not contain raw JSON objects or unformatted API response structures",
	}

	t.Run("keyword", func(t *testing.T) {
		suite.CreateMCPServer(false)

		evals.Run(t, "search_posts keyword quality", func(e *evals.EvalT) {
			// Use a single term to avoid AND logic requiring all terms in one post
			result, err := executeToolWithMCP(e.T, suite, "search_posts", map[string]interface{}{
				"query":   "migration",
				"team_id": data.team.Id,
				"limit":   10,
			})
			require.NoError(e.T, err, "search_posts should succeed")

			text := extractMCPText(result)
			require.NotEmpty(e.T, text, "search_posts should return text content")
			e.Logf("search_posts keyword output:\n%s", text)

			for _, rubric := range rubrics {
				evals.LLMRubricT(e, rubric, text)
			}
		})
	})

	t.Run("with_semantic", func(t *testing.T) {
		// Set up real PGVector + mock embedding provider and index test posts
		searchService := setupEmbeddingSearch(t, data, suite.serverURL, suite.adminToken)
		suite.CreateMCPServerWithSearch(false, searchService)

		// This sub-test validates the semantic search pipeline integration (PGVector + mock embeddings).
		// Mock embeddings don't produce semantically meaningful similarity scores, so this tests
		// that the pipeline wiring works end-to-end (index, query, format), not search relevance.
		evals.Run(t, "search_posts semantic pipeline", func(e *evals.EvalT) {
			result, err := executeToolWithMCP(e.T, suite, "search_posts", map[string]interface{}{
				"query":   "database migration plan",
				"team_id": data.team.Id,
				"limit":   10,
			})
			require.NoError(e.T, err, "search_posts should succeed")

			text := extractMCPText(result)
			require.NotEmpty(e.T, text, "search_posts should return text content")
			e.Logf("search_posts semantic output:\n%s", text)

			for _, rubric := range rubrics {
				evals.LLMRubricT(e, rubric, text)
			}
		})
	})
}

// TestGetChannelMembersOutputQualityEval tests that get_channel_members output is parseable.
func TestGetChannelMembersOutputQualityEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	rubrics := []string{
		"The output lists members with their usernames",
		"Each member entry includes an ID that could be used in follow-up tool calls",
		"The member list includes alice.eval, bob.eval, and charlie.eval",
		// Negative rubric
		"The output does not include any bot accounts",
	}

	evals.Run(t, "get_channel_members output quality", func(e *evals.EvalT) {
		result, err := executeToolWithMCP(e.T, suite, "get_channel_members", map[string]interface{}{
			"channel_id": data.channel.Id,
			"limit":      50,
		})
		require.NoError(e.T, err, "get_channel_members should succeed")

		text := extractMCPText(result)
		require.NotEmpty(e.T, text, "get_channel_members should return text content")
		e.Logf("get_channel_members output:\n%s", text)

		for _, rubric := range rubrics {
			evals.LLMRubricT(e, rubric, text)
		}
	})
}

// ---------------------------------------------------------------------------
// Agentic Flow Evals
// ---------------------------------------------------------------------------

// TestChannelSummarizationFlowEval tests the full agentic loop:
// LLM discovers channel by display name → calls read_channel → produces summary.
func TestChannelSummarizationFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	evals.Run(t, "channel summarization flow", func(e *evals.EvalT) {
		runAgenticFlowEval(e, suite, data.alice, data.team,
			// Use all-lowercase to test case-insensitive lookup and substring matching ("eval" → "Eval Team")
			"Summarize what's been discussed in the migration planning channel on the eval team.",
			[]string{
				"Mentions a database migration (e.g., MySQL to PostgreSQL)",
				"Mentions at least two of: timeline/sprint, data volume/downtime, rollback plan",
				"Is a coherent summary, not a raw dump of messages or tool output",
				// Rubric referencing a user that only exists in the seeded data (anti-hallucination)
				"Mentions at least one of alice.eval, bob.eval, or charlie.eval by username",
			},
			[]string{"get_channel_info", "read_channel"},
		)
	})
}

// TestFindSpecificInfoFlowEval tests search → synthesize:
// LLM must discover the right channel on its own, read it, and find what a specific user said.
func TestFindSpecificInfoFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	evals.Run(t, "find specific info flow", func(e *evals.EvalT) {
		runAgenticFlowEval(e, suite, data.alice, data.team,
			"What did alice.eval say about the rollback plan for the database migration?",
			[]string{
				"Mentions keeping MySQL in read-only mode during cutover",
				"Mentions the ability to switch back within minutes",
				"Mentions continuous replication as a safety net",
				"Does not attribute the rollback plan to bob.eval or charlie.eval",
				"Focuses specifically on the rollback plan rather than summarizing unrelated channel topics like timelines, data volume, or monitoring",
			},
			[]string{"read_channel"},
		)
	})
}

// TestDMSummaryFlowEval tests the read → write loop:
// LLM discovers channel by display name → reads it → composes a summary → sends it as a DM.
func TestDMSummaryFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	evals.Run(t, "dm summary flow", func(e *evals.EvalT) {
		runAgenticFlowEval(e, suite, data.alice, data.team,
			// Use wrong capitalization on "Planning" to test case-insensitive lookup
			"Send bob.eval a DM summarizing the key decisions made in the migration Planning channel on the Eval team.",
			[]string{
				"Confirms that a DM was sent to bob.eval",
			},
			[]string{"read_channel", "dm"},
		)

		// Verify the DM was actually sent by checking via the Mattermost API
		ctx := context.Background()
		adminClient := model.NewAPIv4Client(suite.serverURL)
		adminClient.SetToken(suite.adminToken)

		// Get the admin user (MCP server identity)
		adminUser, _, err := adminClient.GetMe(ctx, "")
		require.NoError(e.T, err, "Should get admin user")

		// Get DM channel between admin and bob
		dmChannel, _, err := adminClient.CreateDirectChannel(ctx, adminUser.Id, data.bob.Id)
		require.NoError(e.T, err, "Should get DM channel")

		// Check that a post exists in the DM channel
		dmPosts, _, err := adminClient.GetPostsForChannel(ctx, dmChannel.Id, 0, 10, "", false, false)
		require.NoError(e.T, err, "Should get DM posts")

		// Find a post that mentions migration (the summary the LLM sent)
		found := false
		for _, post := range dmPosts.Posts {
			if post.UserId == adminUser.Id && containsMigrationContent(post.Message) {
				found = true
				e.Logf("DM post content:\n%s", post.Message)
				break
			}
		}
		assert.True(e.T, found, "A DM post mentioning migration should exist in the DM channel with bob")
	})
}

// containsMigrationContent checks if a message contains migration-related keywords (case-insensitive).
func containsMigrationContent(message string) bool {
	lower := strings.ToLower(message)
	keywords := []string{"migration", "postgresql", "mysql", "database", "migrate"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// TestBroadcastDMToTeamFlowEval tests the team lookup → list members → filter bots → send DMs flow:
// LLM looks up a team by display name, gets members, excludes bot accounts, and DMs each real user.
func TestBroadcastDMToTeamFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedTeamBroadcastScenario(t, suite.serverURL, suite.adminToken)

	evals.Run(t, "broadcast DM to team flow", func(e *evals.EvalT) {
		runAgenticFlowEval(e, suite, data.dana, data.team,
			// Use lowercase "staff" to test case-insensitive team lookup
			"Send the following message to everyone on the staff team: Hey! Just a reminder that we have a company all-hands meeting tomorrow at 3pm in the main conference room. Please make sure to attend.",
			[]string{
				"Confirms that direct messages were sent to multiple individual users",
				"Does not mention sending a message to a bot account (autobot.eval or Automation Bot)",
			},
			[]string{"get_team_members", "dm"},
		)

		// Verify DMs were actually sent by checking the Mattermost API
		ctx := context.Background()
		adminClient := model.NewAPIv4Client(suite.serverURL)
		adminClient.SetToken(suite.adminToken)

		// Get the admin user (MCP server identity — also a team member)
		adminUser, _, err := adminClient.GetMe(ctx, "")
		require.NoError(e.T, err, "Should get admin user")

		// Verify DMs were sent to all non-requesting team members including admin.
		// (dana is the requesting user — LLMs may reasonably skip DM'ing "yourself")
		for _, targetUser := range []*model.User{data.emma, data.frank, adminUser} {
			dmChannel, _, dmErr := adminClient.CreateDirectChannel(ctx, adminUser.Id, targetUser.Id)
			require.NoError(e.T, dmErr, "Should get DM channel with %s", targetUser.Username)

			dmPosts, _, dmErr := adminClient.GetPostsForChannel(ctx, dmChannel.Id, 0, 10, "", false, false)
			require.NoError(e.T, dmErr, "Should get DM posts for %s", targetUser.Username)

			found := false
			for _, post := range dmPosts.Posts {
				if post.UserId == adminUser.Id && strings.Contains(strings.ToLower(post.Message), "all-hands") {
					found = true
					e.Logf("DM to %s: %s", targetUser.Username, post.Message)
					break
				}
			}
			assert.True(e.T, found, "A DM about the all-hands meeting should have been sent to %s", targetUser.Username)
		}

		// Verify NO DM was sent to the bot
		botDMChannel, _, err := adminClient.CreateDirectChannel(ctx, adminUser.Id, data.botUser.Id)
		require.NoError(e.T, err, "Should get DM channel with bot")

		botDMPosts, _, err := adminClient.GetPostsForChannel(ctx, botDMChannel.Id, 0, 10, "", false, false)
		require.NoError(e.T, err, "Should get DM posts for bot")

		for _, post := range botDMPosts.Posts {
			if post.UserId == adminUser.Id && strings.Contains(strings.ToLower(post.Message), "all-hands") {
				assert.Fail(e.T, "A DM about the all-hands meeting should NOT have been sent to bot %s", data.botUser.Username)
				break
			}
		}
	})
}
