// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package threads_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/threads"
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
	result, err := threadService.Analyze(context.Background(), threadData.RootPost.Id, llmContext, promptName, model.NewId(), model.NewId())
	require.NoError(t, err)
	require.NotNil(t, result)
	output, err := result.Stream.ReadAll()
	require.NoError(t, err)
	assert.NotEmpty(t, output, "Expected a non-empty output")

	return output
}

func TestThreadsSummarizeFromExportedDataEval(t *testing.T) {
	// Define the evaluation rubrics for each thread
	evalConfigs := []struct {
		filename string
		rubrics  []string
	}{
		{
			filename: "eval_timed_dnd.json",
			rubrics: []string{
				"mentions that the issue being discussed is a consistency issue on time units of seconds vs milliseconds",
				"refers to thread participants by @username; people mentioned only in passing, whose username is not known, may be named without an @mention",
			},
		},
		{
			filename: "eval_announcement.json",
			rubrics: []string{
				"mentions the successful release of v2.5.0",
				"refers to thread participants by @username; people mentioned only in passing, whose username is not known, may be named without an @mention",
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

func TestThreadsActionItemsFromExportedDataEval(t *testing.T) {
	evalConfigs := []struct {
		filename string
		rubrics  []string
	}{
		{
			filename: "eval_timed_dnd.json",
			rubrics: []string{
				// @harrison offers to "change my code" for DndEndTime, which is a
				// borderline commitment, so listing only that is accepted.
				"Either states that there are no action items, or lists only @harrison updating his timed DND code or documenting that DndEndTime is in seconds",
			},
		},
		{
			filename: "eval_announcement.json",
			rubrics: []string{
				"States that there are no action items in the thread",
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

func TestThreadsOpenQuestionsFromExportedDataEval(t *testing.T) {
	evalConfigs := []struct {
		filename string
		rubrics  []string
	}{
		{
			filename: "eval_timed_dnd.json",
			rubrics: []string{
				"States that there are no open questions in the thread",
			},
		},
		{
			filename: "eval_announcement.json",
			rubrics: []string{
				"States that there are no open questions in the thread",
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
