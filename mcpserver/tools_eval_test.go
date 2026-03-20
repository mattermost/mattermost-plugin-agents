// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost/server/public/model"
)

// TestReadChannelOutputQualityEval tests that read_channel output is useful for LLM consumption.
// It seeds a realistic conversation and grades the tool output against rubrics.
func TestReadChannelOutputQualityEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	// Setup Mattermost container + MCP server (expensive — done once)
	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	// Seed test data
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
	}

	evals.Run(t, "read_channel output quality", func(e *evals.EvalT) {
		args := map[string]interface{}{
			"channel_id": data.channel.Id,
			"limit":      20,
		}

		result, err := executeToolWithMCP(e.T, suite, "read_channel", args)
		require.NoError(e.T, err, "read_channel should succeed")
		require.NotEmpty(e.T, result.Content, "read_channel should return content")

		text := ""
		for _, c := range result.Content {
			if tc, ok := c.(*gomcp.TextContent); ok {
				text += tc.Text
			}
		}
		require.NotEmpty(e.T, text, "read_channel should return text content")

		e.Logf("read_channel output:\n%s", text)

		for _, rubric := range rubrics {
			evals.LLMRubricT(e, rubric, text)
		}
	})
}

// TestChannelSummarizationFlowEval tests the full agentic loop:
// LLM receives MCP tools → decides to call get_channel_info + read_channel → produces summary.
// Uses the real AutoRunToolsWrapper and ToolStore from production code.
func TestChannelSummarizationFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	// Setup Mattermost container + MCP server
	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	// Seed test data
	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	rubrics := []string{
		"Mentions the discussion about migrating from MySQL to PostgreSQL",
		"Mentions the timeline question and the response about next sprint",
		"Mentions the concern about 500GB of data or downtime",
		"Mentions the rollback plan involving keeping MySQL in read-only mode",
		"Is a coherent summary, not a raw dump of messages or tool output",
		"Does not mention the tool-calling process itself",
	}

	evals.Run(t, "channel summarization flow", func(e *evals.EvalT) {
		// Convert MCP tools to llm.Tool (mirrors production mcp/user_clients.go:201)
		mcpTools := mcpToolsToLLMTools(e.T, suite.mcpServer.GetMCPServer())
		require.NotEmpty(e.T, mcpTools, "Should have MCP tools")

		// Collect all tool names for auto-run
		allToolNames := make([]string, len(mcpTools))
		for i, tool := range mcpTools {
			allToolNames[i] = tool.Name
		}

		// Build ToolStore (production: llm/tools.go:364)
		toolStore := llm.NewToolStore(nil, false)
		toolStore.AddTools(mcpTools)

		// Build context with tools and required template fields
		llmContext := llm.NewContext()
		llmContext.Tools = toolStore
		llmContext.RequestingUser = data.alice
		llmContext.Channel = &model.Channel{Type: model.ChannelTypeDirect} // Simulate DM context
		llmContext.Team = data.team
		llmContext.ServerName = "Eval Server"
		llmContext.CompanyName = "Eval Corp"
		llmContext.BotName = "AI Assistant"
		llmContext.BotUsername = "ai-bot"
		llmContext.BotModel = "eval-model"

		// Load real system prompt and build posts (production: conversations/completion.go)
		promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
		require.NoError(e.T, err, "Failed to load prompts")

		posts, err := conversations.BuildNewConversationPosts(promptsObj, llmContext, llm.Post{
			Role:    llm.PostRoleUser,
			Message: "Summarize what's been discussed in the " + data.channel.DisplayName + " channel. The channel ID is " + data.channel.Id,
		})
		require.NoError(e.T, err, "Failed to build conversation posts")

		// Wrap eval LLM with real AutoRunToolsWrapper (production: llm/auto_run_tools.go:18)
		wrappedLLM := llm.NewAutoRunToolsWrapper(e.LLM)

		// Execute with auto-run tools (same option as production channels.go:113)
		result, err := conversations.ExecuteCompletion(wrappedLLM, posts, llmContext, llm.OperationConversation, "",
			llm.WithAutoRunTools(allToolNames))
		require.NoError(e.T, err, "ChatCompletion should succeed")

		summary, err := result.ReadAll()
		require.NoError(e.T, err, "ReadAll should succeed")
		require.NotEmpty(e.T, summary, "Summary should not be empty")

		e.Logf("Channel summary:\n%s", summary)

		for _, rubric := range rubrics {
			evals.LLMRubricT(e, rubric, summary)
		}
	})
}
