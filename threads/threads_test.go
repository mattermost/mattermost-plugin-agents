// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package threads_test

import (
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/evals"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/mattermost/mattermost-plugin-agents/threads"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runThreadAnalysisEval is a helper function for running thread analysis eval tests
func runThreadAnalysisEval(t *evals.EvalT, threadData *evals.ThreadExport, promptName string) string {
	// Create the mock client with the thread data
	mockClient := mockThread(t, threadData)

	// Create context with requesting user and add channel and team info
	llmContext := llm.NewContext()
	llmContext.RequestingUser = &model.User{
		Id:       model.NewId(),
		Username: "bill",
		Locale:   "en",
	}
	llmContext.Channel = threadData.Channel
	llmContext.Team = threadData.Team

	// Set up conversation service for the eval
	ts := setupTest(t.T)

	// Do the thread analysis
	threadService := threads.New(t.LLM, ts.prompts, mockClient, ts.convService)
	result, err := threadService.Analyze(threadData.RootPost.Id, llmContext, promptName, model.NewId(), model.NewId())
	require.NoError(t, err)
	require.NotNil(t, result)
	output, err := result.Stream.ReadAll()
	require.NoError(t, err)
	assert.NotEmpty(t, output, "Expected a non-empty output")

	return output
}

func TestThreadsSummarizeFromExportedData(t *testing.T) {
	// Define the evaluation rubrics for each thread
	evalConfigs := []struct {
		filename string
		rubrics  []string
	}{
		{
			filename: "eval_timed_dnd.json",
			rubrics: []string{
				"mentions that the issue being discussed is a consistency issue on time units of seconds vs milliseconds",
				"contains the usernames involved as @mentions if referenced",
			},
		},
		{
			filename: "eval_announcement.json",
			rubrics: []string{
				"mentions the successful release of v2.5.0",
				"contains the usernames involved as @mentions if referenced",
			},
		},
	}

	for _, config := range evalConfigs {
		testName := "thread summarization from " + config.filename

		evals.Run(t, testName, func(t *evals.EvalT) {
			// Load thread data from the JSON file
			path := filepath.Join(".", config.filename)
			threadData := evals.LoadThreadFromJSON(t, path)

			// Run the analysis
			summary := runThreadAnalysisEval(t, threadData, prompts.PromptSummarizeThreadSystem)

			// Evaluate the summary against the rubric
			for _, rubric := range config.rubrics {
				evals.LLMRubricT(t, rubric, summary)
			}
		})
	}
}

func TestThreadsActionItemsFromExportedData(t *testing.T) {
	evalConfigs := []struct {
		filename string
		rubrics  []string
	}{
		{
			filename: "eval_timed_dnd.json",
			rubrics: []string{
				"does not list any committed action items with specific owners and deadlines",
			},
		},
		{
			filename: "eval_announcement.json",
			rubrics: []string{
				"does not list any committed action items with specific owners and deadlines",
			},
		},
	}

	for _, config := range evalConfigs {
		testName := "action items from " + config.filename

		evals.Run(t, testName, func(t *evals.EvalT) {
			// Load thread data from the JSON file
			path := filepath.Join(".", config.filename)
			threadData := evals.LoadThreadFromJSON(t, path)

			// Run the analysis
			actionItems := runThreadAnalysisEval(t, threadData, prompts.PromptFindActionItemsSystem)

			// Evaluate the action items against the rubric
			for _, rubric := range config.rubrics {
				evals.LLMRubricT(t, rubric, actionItems)
			}
		})
	}
}

func TestThreadsOpenQuestionsFromExportedData(t *testing.T) {
	evalConfigs := []struct {
		filename string
		rubrics  []string
	}{
		{
			filename: "eval_timed_dnd.json",
			rubrics: []string{
				"does not list any questions that went completely unanswered",
			},
		},
		{
			filename: "eval_announcement.json",
			rubrics: []string{
				"does not list any questions that went completely unanswered",
			},
		},
	}

	for _, config := range evalConfigs {
		testName := "open questions from " + config.filename

		evals.Run(t, testName, func(t *evals.EvalT) {
			// Load thread data from the JSON file
			path := filepath.Join(".", config.filename)
			threadData := evals.LoadThreadFromJSON(t, path)

			// Run the analysis
			openQuestions := runThreadAnalysisEval(t, threadData, prompts.PromptFindOpenQuestionsSystem)

			// Evaluate the open questions against the rubric
			for _, rubric := range config.rubrics {
				evals.LLMRubricT(t, rubric, openQuestions)
			}
		})
	}
}

func mockThread(t *evals.EvalT, threadData *evals.ThreadExport) *mmapimocks.MockClient {
	// Mock pluginapi returning thread
	mockClient := mmapimocks.NewMockClient(t.T)
	mockClient.EXPECT().GetPostThread(threadData.RootPost.Id).Return(threadData.PostList, nil)

	// Mock users
	for userID, user := range threadData.Users {
		mockClient.EXPECT().GetUser(userID).Return(user, nil)
	}

	return mockClient
}
