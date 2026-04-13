// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channels_test

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/channels"
	"github.com/mattermost/mattermost-plugin-agents/evals"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fixedStart = int64(23974)
)

func TestChannelSummarization(t *testing.T) {
	evalConfigs := []struct {
		name            string
		filename        string
		expectedRubrics []string
	}{
		{
			name:     "developers webapp channel",
			filename: "developers_webapp.json",
			expectedRubrics: []string{
				"is a summary",
				"includes a mention that @daniel.espino-garcia mentioned react scan",
				"mentions positive feedback to react scan",
				"mentions @claudio.costa is working on adding code coverage tracking to the monorepo",
				"mentions claudio and harrison discussing exactly what should be tracked for code coverage",
				"mentions harrison queueing a item for a June 2nd webguild meeting about showing off PRs around accessibility",
				"does not mention the summarization process",
				"does not mention people joining or leaving the channel",
			},
		},
	}

	for _, config := range evalConfigs {
		testName := "channel summarization " + config.name
		evals.Run(t, testName, func(t *evals.EvalT) {
			// Load thread data from the JSON file
			path := filepath.Join(".", config.filename)
			threadData := evals.LoadThreadFromJSON(t, path)

			// Setup mocks
			mmClient := mocks.NewMockClient(t)
			promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
			require.NoError(t, err, "Failed to load prompts")

			// Setup mock expectations
			setupChannelMocksFromThreadData(mmClient, threadData)

			// Create channel service
			channelService := channels.New(
				t.LLM,
				promptsObj,
				mmClient,
				nil, // dbClient not needed for this test
			)

			// Create context
			ctx := llm.NewContext()
			ctx.RequestingUser = threadData.RequestingUser()
			ctx.Channel = threadData.Channel
			ctx.Team = threadData.Team

			// Perform summarization based on type
			textStream, err := channelService.Interval(ctx, threadData.Channel.Id, fixedStart, 0, prompts.PromptSummarizeChannelRangeSystem)
			require.NoError(t, err, "Failed to summarize channel")
			require.NotNil(t, textStream, "Expected a non-nil text stream")

			// Read the response
			summary, err := textStream.ReadAll()
			require.NoError(t, err, "Failed to read summary from text stream")
			assert.NotEmpty(t, summary, "Expected a non-empty channel summary")

			// Evaluate the summary against rubrics
			for _, rubric := range config.expectedRubrics {
				evals.LLMRubricT(t, rubric, summary)
			}
		})
	}
}

func setupChannelMocksFromThreadData(mmClient *mocks.MockClient, threadData *evals.ThreadExport) {
	// Mock posts retrieval - return the thread data as channel posts
	mmClient.On("GetPostsSince", threadData.Channel.Id, fixedStart).Return(threadData.PostList, nil)

	// Mock users
	for userID, user := range threadData.Users {
		mmClient.On("GetUser", userID).Return(user, nil)
	}

	// Mock file info if needed
	for _, fileInfo := range threadData.FileInfos {
		mmClient.On("GetFileInfo", fileInfo.Id).Return(fileInfo, nil).Maybe()
	}

	// Mock file content if needed
	for id, file := range threadData.Files {
		mmClient.On("GetFile", id).Return(io.NopCloser(bytes.NewReader(file)), nil).Maybe()
	}
}
