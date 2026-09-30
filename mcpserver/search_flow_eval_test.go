// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

var citationPermalinkPattern = regexp.MustCompile(`\[permalink\]\(([^)\s]+)\)`)
var citedPostIDPattern = regexp.MustCompile(`/pl/([a-z0-9]{26})\?view=citation`)

type searchFlowResult struct {
	response  string
	toolCalls []llm.ToolCall
	citedIDs  []string
}

// runSearchFlowEval runs a search bar request through the same system prompt
// and tool loop the plugin uses, with every tool call auto-approved.
func runSearchFlowEval(e *evals.EvalT, suite *TestSuite, data *evalChannelData, query string, scope *model.Channel) searchFlowResult {
	e.Helper()

	setup := setupAgenticEval(e.T, e, suite, data.alice, data.team)
	setup.llmContext.SiteURL = suite.serverURL

	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(e.T, err)
	systemPrompt, err := conversations.FormatSearchSystemPrompt(promptsObj, setup.llmContext, scope)
	require.NoError(e.T, err)

	runResult, err := toolrunner.New(setup.llm).Run(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleSystem, Message: systemPrompt},
			{Role: llm.PostRoleUser, Message: query},
		},
		Context:   setup.llmContext,
		Operation: llm.OperationSearch,
	}, func(llm.ToolCall) bool { return true }, nil)
	require.NoError(e.T, err)

	response, err := runResult.Stream.ReadAll()
	require.NoError(e.T, err)
	require.NotEmpty(e.T, response)
	e.Logf("LLM response:\n%s", response)

	var citedIDs []string
	for _, link := range citationPermalinkPattern.FindAllStringSubmatch(response, -1) {
		match := citedPostIDPattern.FindStringSubmatch(link[1])
		require.NotNil(e.T, match, "citation %q does not use the post permalink format", link[1])
		citedIDs = append(citedIDs, match[1])
	}

	return searchFlowResult{
		response:  response,
		toolCalls: setup.logger.ToolCalls(),
		citedIDs:  citedIDs,
	}
}

// seededPostIDs returns the IDs of every post in the eval team's channels.
func seededPostIDs(t *testing.T, suite *TestSuite, data *evalChannelData) map[string]bool {
	t.Helper()
	adminClient := model.NewAPIv4Client(suite.serverURL)
	adminClient.SetToken(suite.adminToken)

	ids := make(map[string]bool)
	for _, channel := range []*model.Channel{data.channel, data.designChannel} {
		posts, _, err := adminClient.GetPostsForChannel(context.Background(), channel.Id, 0, 100, "", false, false)
		require.NoError(t, err)
		for id := range posts.Posts {
			ids[id] = true
		}
	}
	return ids
}

func searchPostsCalls(calls []llm.ToolCall) []llm.ToolCall {
	var out []llm.ToolCall
	for _, call := range calls {
		if llm.BareMCPToolName(call.Name) == "search_posts" {
			out = append(out, call)
		}
	}
	return out
}

// TestSearchBarFlowEval covers the search bar conversation: the agent must
// search Mattermost itself, answer from what it finds, and cite real posts.
func TestSearchBarFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)
	knownPostIDs := seededPostIDs(t, suite, data)

	tests := []struct {
		name            string
		query           string
		rubrics         []string
		mustCite        []string
		requireCitation bool
	}{
		{
			name:  "answers a question and cites the source post",
			query: "What's the rollback plan for the database migration?",
			rubrics: []string{
				"Says the MySQL instance stays running in read-only mode during the cutover",
				"Says they can switch back within minutes if something fails",
			},
			mustCite:        []string{data.aliceRollbackPost.Id},
			requireCitation: true,
		},
		{
			name:  "turns bare search terms into an answer from the thread",
			query: "Q3 feature freeze migration timeline",
			rubrics: []string{
				"Says the schema migration is targeted for next sprint",
				"Mentions two weeks of testing before the cutover",
			},
			requireCitation: true,
		},
		{
			name:  "says so when nothing relevant exists",
			query: "What did we decide about the office holiday party venue?",
			rubrics: []string{
				"States that no information about the office holiday party venue was found",
				"Does not name or describe a holiday party venue",
			},
		},
	}

	for _, tt := range tests {
		evals.Run(t, "search bar "+tt.name, func(e *evals.EvalT) {
			result := runSearchFlowEval(e, suite, data, tt.query, nil)

			assert.NotEmpty(e.T, searchPostsCalls(result.toolCalls), "the agent must search with search_posts")

			for _, id := range result.citedIDs {
				assert.True(e.T, knownPostIDs[id], "citation references post %s, which does not exist", id)
			}
			if tt.requireCitation {
				assert.NotEmpty(e.T, result.citedIDs, "the answer must cite at least one post")
			}
			for _, id := range tt.mustCite {
				assert.True(e.T, slices.Contains(result.citedIDs, id), "the answer must cite post %s (cited: %v)", id, result.citedIDs)
			}

			for _, rubric := range tt.rubrics {
				evals.LLMRubricT(e, rubric, result.response)
			}
		})
	}
}

// TestAskChannelSearchFlowEval covers /ask-channel: the agent must only look
// inside the requested channel, and the answer must not pull in other channels.
func TestAskChannelSearchFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	evals.Run(t, "ask-channel stays in the channel", func(e *evals.EvalT) {
		result := runSearchFlowEval(e, suite, data, "What has been proposed here?", data.designChannel)

		var channelLookups int
		for _, call := range result.toolCalls {
			switch llm.BareMCPToolName(call.Name) {
			case "search_posts", "read_channel":
			default:
				continue
			}
			channelLookups++
			var args struct {
				ChannelID string `json:"channel_id"`
			}
			require.NoError(e.T, json.Unmarshal(call.Arguments, &args))
			assert.Equal(e.T, data.designChannel.Id, args.ChannelID, "%s call %s left the requested channel", call.Name, string(call.Arguments))
		}
		require.Positive(e.T, channelLookups, "the agent must search or read the requested channel")

		for _, rubric := range []string{
			"Mentions the Figma mockups for the dashboard redesign or the card-based layout for the analytics section",
			"Does not mention a database migration, MySQL, or PostgreSQL",
		} {
			evals.LLMRubricT(e, rubric, result.response)
		}
	})
}
