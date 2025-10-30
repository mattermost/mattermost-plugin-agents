// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"bytes"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-ai/mmtools"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock implementations
type mockToolProvider struct{}

func (m *mockToolProvider) GetTools(isDM bool, bot *bots.Bot) []llm.Tool {
	tools := []llm.Tool{}

	tools = append(tools, llm.Tool{
		Name:        "GetGithubIssue",
		Description: "Retrieve a single GitHub issue by owner, repo, and issue number.",
		Schema:      llm.NewJSONSchemaFromStruct[mmtools.GetGithubIssueArgs](),
		Resolver: func(context *llm.Context, args llm.ToolArgumentGetter) (string, error) {
			return "Unable to retrieve GitHub issue", nil
		},
	})

	return tools
}

type mockMCPClientManager struct{}

func (m *mockMCPClientManager) GetToolsForUser(userID, sessionID string) ([]llm.Tool, *mcp.Errors) {
	return []llm.Tool{}, nil
}

type mockConfigProvider struct{}

func (m *mockConfigProvider) GetEnableLLMTrace() bool {
	return false
}

func (m *mockConfigProvider) GetServiceByID(id string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

func TestConversationMentionHandling(t *testing.T) {
	// Define the evaluation rubrics for each conversation
	evalConfigs := []struct {
		filename string
		rubrics  []string
	}{
		{
			filename: "attribution_long_thread.json",
			rubrics: []string{
				"is a list of bugs",
				"includes a description of each bug",
				"attributes each bug to a user",
				"attributes the bug about trying to save without a color and the save button not doing anything to @maria.nunez",
				"the bug about the end user being able to change channel banner is attributed to @maria.nunez",
				"has no unnecessary statements",
				"should NOT include any statements inviting the user to ask more questions",
			},
		},
	}

	for _, config := range evalConfigs {
		testName := "conversation from " + config.filename
		evals.Run(t, testName, func(t *evals.EvalT) {
			// Load thread data from the JSON file
			path := filepath.Join(".", config.filename)
			threadData := evals.LoadThreadFromJSON(t, path)

			mockAPI := &plugintest.API{}
			client := pluginapi.NewClient(mockAPI, nil)
			mmClient := mocks.NewMockClient(t)
			licenseChecker := enterprise.NewLicenseChecker(client)
			botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
			prompts, err := llm.NewPrompts(prompts.PromptsFolder)
			require.NoError(t, err, "Failed to load prompts")

			mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
			mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
			mockAPI.On("GetTeam", threadData.Team.Id).Return(threadData.Team, nil)
			mmClient.On("GetPostThread", threadData.LatestPost().Id).Return(threadData.PostList, nil)
			for _, user := range threadData.Users {
				mmClient.On("GetUser", user.Id).Return(user, nil).Maybe()
			}
			for _, fileInfo := range threadData.FileInfos {
				mmClient.On("GetFileInfo", fileInfo.Id).Return(fileInfo, nil).Maybe()
			}
			for id, file := range threadData.Files {
				mmClient.On("GetFile", id).Return(io.NopCloser(bytes.NewReader(file)), nil).Maybe()
			}

			// Create mock implementations
			toolProvider := &mockToolProvider{}
			mcpClientManager := &mockMCPClientManager{}
			configProvider := &mockConfigProvider{}

			contextBuilder := llmcontext.NewLLMContextBuilder(
				client,
				toolProvider,
				mcpClientManager,
				configProvider,
			)

			conv := conversations.New(
				prompts,
				mmClient,
				nil,
				contextBuilder,
				botService,
				nil,
				licenseChecker,
				i18n.Init(),
				nil,
			)

			// Create a mock bot
			botConfig := llm.BotConfig{
				ID:                 "botid",
				Name:               "matty",
				DisplayName:        "Matty",
				CustomInstructions: "",
				EnableVision:       true,
				DisableTools:       false,
				ServiceID:          "test-service",
			}
			serviceConfig := llm.ServiceConfig{
				ID:           "test-service",
				Type:         llm.ServiceTypeOpenAI,
				DefaultModel: "gpt-4",
			}
			mmBot := &model.Bot{
				UserId: "botid",
			}
			llmInstance := llm.NewLanguageModelTestLogWrapper(t.T, t.LLM)

			bot := bots.NewBot(botConfig, serviceConfig, mmBot, llmInstance)

			textStream, err := conv.ProcessUserRequest(bot, threadData.RequestingUser(), threadData.Channel, threadData.LatestPost(), "")
			require.NoError(t, err, "Failed to process user request")
			require.NotNil(t, textStream, "Expected a non-nil text stream")

			// Read the response from the text stream
			response, err := textStream.ReadAll()
			require.NoError(t, err, "Failed to read response from text stream")
			assert.NotEmpty(t, response, "Expected a non-empty conversation response")

			// Evaluate the response against the rubric
			for _, rubric := range config.rubrics {
				evals.LLMRubricT(t, rubric, response)
			}
		})
	}
}
