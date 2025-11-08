// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPost represents a simplified post for testing
type TestPost struct {
	Message string // The message content
}

func TestDirectMessageConversations(t *testing.T) {
	evalConfigs := []struct {
		name         string
		conversation []TestPost // Messages alternate: user, bot, user, bot, etc.
		rubrics      []string
	}{
		{
			name: "knows who it is",
			conversation: []TestPost{
				{Message: "Hi, who are you?"},
			},
			rubrics: []string{
				"explanation that they are Matty or @matty an AI agent",
			},
		},
		{
			name: "knows the model powering it",
			conversation: []TestPost{
				{Message: "What model are you using?"},
			},
			rubrics: []string{
				"explains they are powered by mattermodel-5.4",
			},
		},
		{
			name: "knows it doesn't know what it can do",
			conversation: []TestPost{
				{Message: "Can you access posts in private channels?"},
			},
			rubrics: []string{
				"refers the user to the documentation",
			},
		},
		{
			name: "tool introspection",
			conversation: []TestPost{
				{Message: "What tools do you have?"},
			},
			rubrics: []string{
				"mentions Github and refers to the documentation",
			},
		},
	}

	for _, config := range evalConfigs {
		evals.Run(t, "bot dm "+config.name, func(t *evals.EvalT) {
			// Create thread data from the conversation
			threadData := createDMThreadDataFromConversation(config.conversation)

			// Setup mocks and services
			mockAPI := &plugintest.API{}
			client := pluginapi.NewClient(mockAPI, nil)
			mmClient := mocks.NewMockClient(t)
			licenseChecker := enterprise.NewLicenseChecker(client)
			botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil, nil)
			prompts, err := llm.NewPrompts(prompts.PromptsFolder)
			require.NoError(t, err, "Failed to load prompts")

			// Setup mock expectations
			mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
			mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "professional"}).Maybe()
			mockAPI.On("GetTeam", threadData.Team.Id).Return(threadData.Team, nil)
			mockAPI.On("GetChannel", threadData.Channel.Id).Return(threadData.Channel, nil)
			mmClient.On("GetPostThread", threadData.LatestPost().Id).Return(threadData.PostList, nil).Maybe()
			mmClient.On("GetChannel", threadData.Channel.Id).Return(threadData.Channel, nil).Maybe()

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

			// Create a mock bot for DM
			botConfig := llm.BotConfig{
				ID:                 "testbotid",
				Name:               "matty",
				DisplayName:        "Matty",
				CustomInstructions: "",
				EnableVision:       false,
				DisableTools:       false,
				ServiceID:          "test-service",
			}
			serviceConfig := llm.ServiceConfig{
				ID:           "test-service",
				Type:         llm.ServiceTypeOpenAI,
				DefaultModel: "mattermodel-5.4",
			}
			mmBot := &model.Bot{
				UserId: "testbotid",
			}
			llmInstance := llm.NewLanguageModelTestLogWrapper(t.T, t.LLM)

			bot := bots.NewBot(botConfig, serviceConfig, mmBot, llmInstance)

			// Process the DM request
			textStream, err := conv.ProcessUserRequest(bot, threadData.RequestingUser(), threadData.Channel, threadData.LatestPost())
			require.NoError(t, err, "Failed to process DM request")
			require.NotNil(t, textStream, "Expected a non-nil text stream")

			// Read the response
			response, err := textStream.ReadAll()
			require.NoError(t, err, "Failed to read response from text stream")
			assert.NotEmpty(t, response, "Expected a non-empty DM response")

			// Evaluate the response against rubrics
			for _, rubric := range config.rubrics {
				evals.LLMRubricT(t, rubric, response)
			}
		})
	}
}

// createDMThreadDataFromConversation creates thread data from a conversation
func createDMThreadDataFromConversation(conversation []TestPost) *evals.ThreadExport {
	// Validate conversation ends with user post (odd number of messages)
	if len(conversation) == 0 {
		panic("conversation cannot be empty")
	}
	if len(conversation)%2 == 0 {
		panic("conversation must end with a user message (odd number of messages)")
	}

	// Static IDs
	userID := "testuserid"
	botID := "testbotid"
	channelID := "dm_channel_123"
	teamID := "team123"

	// Static users
	user := &model.User{
		Id:       userID,
		Username: "corey",
		Locale:   "en",
	}

	bot := &model.User{
		Id:        botID,
		Username:  "matty",
		FirstName: "Matty",
		IsBot:     true,
	}

	// Create posts from conversation
	postsMap := make(map[string]*model.Post)
	postList := model.NewPostList()

	baseTime := int64(1234567890000)
	var rootPost *model.Post

	for i, testPost := range conversation {
		// Determine who sent this message (user goes first, then alternates)
		isUserMessage := i%2 == 0
		senderID := userID
		if !isUserMessage {
			senderID = botID
		}

		postID := fmt.Sprintf("post%d", i+1)
		post := &model.Post{
			Id:        postID,
			UserId:    senderID,
			ChannelId: channelID,
			Message:   testPost.Message,
			CreateAt:  baseTime + int64(i*1000), // 1 second apart
		}

		// First post is root, others are replies
		if i == 0 {
			rootPost = post
		} else {
			post.RootId = rootPost.Id
		}

		postsMap[postID] = post
		postList.AddPost(post)
		postList.AddOrder(postID)
	}

	return &evals.ThreadExport{
		Team: &model.Team{
			Id:          teamID,
			Name:        "fastfutures",
			DisplayName: "Fast Futures",
		},
		Channel: &model.Channel{
			Id:     channelID,
			TeamId: teamID,
			Type:   model.ChannelTypeDirect,
			Name:   model.GetDMNameFromIds(userID, botID),
		},
		Users: map[string]*model.User{
			userID: user,
			botID:  bot,
		},
		Posts:     postsMap,
		RootPost:  rootPost,
		PostList:  postList,
		FileInfos: make(map[string]*model.FileInfo),
		Files:     make(map[string][]byte),
	}
}
