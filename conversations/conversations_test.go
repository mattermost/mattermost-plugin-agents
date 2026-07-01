// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock implementations
type mockToolProvider struct{}

func (m *mockToolProvider) GetTools(bot *bots.Bot, _ *llm.Context) []llm.Tool {
	return []llm.Tool{
		{
			Name:        "WebSearch",
			Description: "Search the web for information.",
			Schema:      llm.NewJSONSchemaFromStruct[struct{ Term string }](),
			Resolver: func(_ context.Context, _ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
				return "No results found.", nil
			},
		},
	}
}

type mockMCPClientManager struct{}

func (m *mockMCPClientManager) GetToolsForUser(context.Context, string) ([]llm.Tool, *mcp.Errors) {
	return []llm.Tool{}, nil
}

type mockConfigProvider struct{}

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
			botService := bots.New(mockAPI, client, licenseChecker, nil, nil, &http.Client{}, nil)
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

			_ = conversations.New(
				prompts,
				mmClient,
				nil,
				contextBuilder,
				botService,
				nil,
				licenseChecker,
				i18n.Init(),
				nil,
				nil, // configProvider - nil means channel tool calling is disabled (default)
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

			_ = bots.NewBot(botConfig, serviceConfig, mmBot, llmInstance)

			// Build completion request directly (ProcessUserRequest was removed in Step L)
			llmContext := contextBuilder.BuildLLMContextUserRequest(
				bots.NewBot(botConfig, serviceConfig, mmBot, llmInstance),
				threadData.RequestingUser(),
				threadData.Channel,
			)
			systemPrompt, err := prompts.Format("direct_message_question_system", llmContext)
			require.NoError(t, err, "Failed to format system prompt")

			posts := []llm.Post{
				{Role: llm.PostRoleSystem, Message: systemPrompt},
				{Role: llm.PostRoleUser, Message: threadData.LatestPost().Message},
			}
			textStream, err := llmInstance.ChatCompletion(context.Background(), llm.CompletionRequest{
				Posts:     posts,
				Context:   llmContext,
				Operation: llm.OperationConversation,
			})
			require.NoError(t, err, "Failed to get chat completion")
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
