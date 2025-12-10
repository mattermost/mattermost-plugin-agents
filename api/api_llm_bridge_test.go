// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Full-stack integration tests using bridge client → real API → fake LLM

func TestBridgeClientAgentCompletion(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name        string
		agent       string
		request     bridgeclient.CompletionRequest
		fakeLLM     *FakeLLM
		expectError bool
		errorMsg    string
		validateRes func(t *testing.T, result string)
	}{
		{
			name:  "successful completion",
			agent: testBotUserID,
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Hello"},
				},
			},
			fakeLLM:     NewFakeLLM("Hello! How can I help you?"),
			expectError: false,
			validateRes: func(t *testing.T, result string) {
				require.Equal(t, "Hello! How can I help you?", result)
			},
		},
		{
			name:  "multiple posts with different roles",
			agent: testBotUserID,
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "system", Message: "You are helpful"},
					{Role: "user", Message: "What's 2+2?"},
				},
			},
			fakeLLM:     NewFakeLLM("The answer is 4"),
			expectError: false,
			validateRes: func(t *testing.T, result string) {
				require.Equal(t, "The answer is 4", result)
			},
		},
		{
			name:  "LLM returns error",
			agent: testBotUserID,
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Hello"},
				},
			},
			fakeLLM:     NewFakeLLMWithError(fmt.Errorf("LLM service unavailable")),
			expectError: true,
			errorMsg:    "failed to complete LLM request",
		},
		{
			name:  "empty posts array",
			agent: testBotUserID,
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{},
			},
			fakeLLM:     NewFakeLLM("test"),
			expectError: true,
			errorMsg:    "posts array cannot be empty",
		},
		{
			name:  "bot not found",
			agent: testNonexistentBot,
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Hello"},
				},
			},
			fakeLLM:     NewFakeLLM("test"),
			expectError: true,
			errorMsg:    "bot not found",
		},
		{
			name:  "bot role alias works",
			agent: testBotUserID,
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "bot", Message: "I'm a bot"},
					{Role: "user", Message: "Hi"},
				},
			},
			fakeLLM:     NewFakeLLM("Hello!"),
			expectError: false,
			validateRes: func(t *testing.T, result string) {
				require.Equal(t, "Hello!", result)
			},
		},
		{
			name:  "invalid role",
			agent: testBotUserID,
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "invalid", Message: "test"},
				},
			},
			fakeLLM:     NewFakeLLM("test"),
			expectError: true,
			errorMsg:    "invalid role",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup bot with fake LLM
			botConfig := llm.BotConfig{
				Name:            "testbot",
				DisplayName:     "Test Bot",
				UserAccessLevel: llm.UserAccessLevelAll,
			}
			e.setupTestBot(botConfig)

			// Inject fake LLM
			if tc.fakeLLM != nil {
				for _, bot := range e.bots.GetAllBots() {
					if bot.GetConfig().Name == "testbot" {
						bot.SetLLMForTest(tc.fakeLLM)
					}
				}
			}

			// Allow error logging
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create bridge client and make request
			client := e.CreateBridgeClient()
			result, err := client.AgentCompletion(tc.agent, tc.request)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorMsg != "" {
					require.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				require.NoError(t, err)
				if tc.validateRes != nil {
					tc.validateRes(t, result)
				}
			}
		})
	}
}

func TestBridgeClientAgentCompletionStream(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name        string
		agent       string
		request     bridgeclient.CompletionRequest
		fakeLLM     *FakeLLM
		expectError bool
		errorMsg    string
		validateRes func(t *testing.T, result *llm.TextStreamResult)
	}{
		{
			name:  "successful streaming",
			agent: testBotUserID,
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Count to 3"},
				},
			},
			fakeLLM: NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "1"},
				{Type: llm.EventTypeText, Value: " "},
				{Type: llm.EventTypeText, Value: "2"},
				{Type: llm.EventTypeText, Value: " "},
				{Type: llm.EventTypeText, Value: "3"},
				{Type: llm.EventTypeEnd, Value: nil},
			}),
			expectError: false,
			validateRes: func(t *testing.T, result *llm.TextStreamResult) {
				require.NotNil(t, result)
				require.NotNil(t, result.Stream)

				var text strings.Builder
				for event := range result.Stream {
					if event.Type == llm.EventTypeText {
						if textValue, ok := event.Value.(string); ok {
							text.WriteString(textValue)
						}
					} else if event.Type == llm.EventTypeEnd {
						break
					}
				}

				require.Equal(t, "1 2 3", text.String())
			},
		},
		{
			name:  "streaming with error event",
			agent: testBotUserID,
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Hello"},
				},
			},
			fakeLLM:     StreamingLLMError("simulated error"),
			expectError: false, // Request succeeds, error is in stream
			validateRes: func(t *testing.T, result *llm.TextStreamResult) {
				require.NotNil(t, result)

				gotError := false
				for event := range result.Stream {
					if event.Type == llm.EventTypeError {
						gotError = true
						break
					}
				}
				require.True(t, gotError, "should receive error event in stream")
			},
		},
		{
			name:  "bot not found",
			agent: testNonexistentBot,
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Hello"},
				},
			},
			fakeLLM:     NewFakeLLM("test"),
			expectError: true,
			errorMsg:    "bot not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup bot with fake LLM
			botConfig := llm.BotConfig{
				Name:            "testbot",
				DisplayName:     "Test Bot",
				UserAccessLevel: llm.UserAccessLevelAll,
			}
			e.setupTestBot(botConfig)

			// Inject fake LLM
			for _, bot := range e.bots.GetAllBots() {
				if bot.GetConfig().Name == "testbot" {
					bot.SetLLMForTest(tc.fakeLLM)
				}
			}

			// Allow error logging
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create bridge client and make streaming request
			client := e.CreateBridgeClient()
			result, err := client.AgentCompletionStream(tc.agent, tc.request)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorMsg != "" {
					require.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				require.NoError(t, err)
				if tc.validateRes != nil {
					tc.validateRes(t, result)
				}
			}
		})
	}
}

func TestBridgeClientServiceCompletion(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name          string
		service       string
		request       bridgeclient.CompletionRequest
		serviceConfig llm.ServiceConfig
		fakeLLM       *FakeLLM
		expectError   bool
		errorMsg      string
		validateRes   func(t *testing.T, result string)
	}{
		{
			name:    "successful service completion by ID",
			service: "test-service-id",
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Hello"},
				},
			},
			serviceConfig: llm.ServiceConfig{
				ID:   "test-service-id",
				Name: "Test Service",
			},
			fakeLLM:     NewFakeLLM("Service response"),
			expectError: false,
			validateRes: func(t *testing.T, result string) {
				require.Equal(t, "Service response", result)
			},
		},
		{
			name:    "successful service completion by name",
			service: "TestService",
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Hello"},
				},
			},
			serviceConfig: llm.ServiceConfig{
				ID:   "test-service-id",
				Name: "TestService",
			},
			fakeLLM:     NewFakeLLM("Service response by name"),
			expectError: false,
			validateRes: func(t *testing.T, result string) {
				require.Equal(t, "Service response by name", result)
			},
		},
		{
			name:    "service not found",
			service: "nonexistent-service",
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Hello"},
				},
			},
			serviceConfig: llm.ServiceConfig{ID: "other-service", Name: "Other"},
			fakeLLM:       NewFakeLLM("test"),
			expectError:   true,
			errorMsg:      "no bot found for service",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup bot with service
			botConfig := llm.BotConfig{
				Name:            "testbot",
				DisplayName:     "Test Bot",
				UserAccessLevel: llm.UserAccessLevelAll,
			}
			e.setupTestBot(botConfig)

			// Set service and LLM
			for _, bot := range e.bots.GetAllBots() {
				bot.SetServiceForTest(tc.serviceConfig)
				if tc.fakeLLM != nil {
					bot.SetLLMForTest(tc.fakeLLM)
				}
			}

			// Allow error logging
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create bridge client and make request
			client := e.CreateBridgeClient()
			result, err := client.ServiceCompletion(tc.service, tc.request)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorMsg != "" {
					require.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				require.NoError(t, err)
				if tc.validateRes != nil {
					tc.validateRes(t, result)
				}
			}
		})
	}
}

func TestBridgeClientServiceCompletionStream(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name          string
		service       string
		request       bridgeclient.CompletionRequest
		serviceConfig llm.ServiceConfig
		fakeLLM       *FakeLLM
		expectError   bool
		errorMsg      string
		validateRes   func(t *testing.T, result *llm.TextStreamResult)
	}{
		{
			name:    "successful service streaming",
			service: "openai-service",
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Stream test"},
				},
			},
			serviceConfig: llm.ServiceConfig{
				ID:   "openai-service",
				Name: "OpenAI",
			},
			fakeLLM: NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "OpenAI "},
				{Type: llm.EventTypeText, Value: "stream"},
				{Type: llm.EventTypeEnd, Value: nil},
			}),
			expectError: false,
			validateRes: func(t *testing.T, result *llm.TextStreamResult) {
				require.NotNil(t, result)

				var text strings.Builder
				for event := range result.Stream {
					if event.Type == llm.EventTypeText {
						if textValue, ok := event.Value.(string); ok {
							text.WriteString(textValue)
						}
					} else if event.Type == llm.EventTypeEnd {
						break
					}
				}

				require.Equal(t, "OpenAI stream", text.String())
			},
		},
		{
			name:    "service not found",
			service: "nonexistent",
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Hello"},
				},
			},
			serviceConfig: llm.ServiceConfig{ID: "other", Name: "Other"},
			fakeLLM:       NewFakeLLM("test"),
			expectError:   true,
			errorMsg:      "no bot found for service",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup bot with service
			botConfig := llm.BotConfig{
				Name:            "testbot",
				DisplayName:     "Test Bot",
				UserAccessLevel: llm.UserAccessLevelAll,
			}
			e.setupTestBot(botConfig)

			// Set service and LLM
			for _, bot := range e.bots.GetAllBots() {
				bot.SetServiceForTest(tc.serviceConfig)
				if tc.fakeLLM != nil {
					bot.SetLLMForTest(tc.fakeLLM)
				}
			}

			// Allow error logging
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create bridge client and make streaming request
			client := e.CreateBridgeClient()
			result, err := client.ServiceCompletionStream(tc.service, tc.request)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorMsg != "" {
					require.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				require.NoError(t, err)
				if tc.validateRes != nil {
					tc.validateRes(t, result)
				}
			}
		})
	}
}

func TestBridgeClientPermissions(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name        string
		userID      string
		channelID   string
		botConfig   llm.BotConfig
		envSetup    func(e *TestEnvironment)
		expectError bool
		errorMsg    string
	}{
		{
			name:      "no UserID or ChannelID - succeeds (backward compatibility)",
			userID:    "",
			channelID: "",
			botConfig: llm.BotConfig{
				UserAccessLevel: llm.UserAccessLevelAll,
			},
			envSetup:    func(e *TestEnvironment) {},
			expectError: false,
		},
		{
			name:      "UserID only with allowed user - succeeds",
			userID:    testUserID,
			channelID: "",
			botConfig: llm.BotConfig{
				UserAccessLevel: llm.UserAccessLevelAll,
			},
			envSetup:    func(e *TestEnvironment) {},
			expectError: false,
		},
		{
			name:      "UserID only with blocked user - returns error",
			userID:    testUserID,
			channelID: "",
			botConfig: llm.BotConfig{
				UserAccessLevel: llm.UserAccessLevelBlock,
				UserIDs:         []string{testUserID},
			},
			envSetup:    func(e *TestEnvironment) {},
			expectError: true,
			errorMsg:    "permission denied",
		},
		{
			name:      "UserID + ChannelID with allowed user and channel - succeeds",
			userID:    testUserID,
			channelID: testChannelID,
			botConfig: llm.BotConfig{
				UserAccessLevel:    llm.UserAccessLevelAll,
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
			},
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("GetChannel", testChannelID).Return(&model.Channel{
					Id:     testChannelID,
					Type:   model.ChannelTypeOpen,
					TeamId: "team-123",
				}, nil).Once()
			},
			expectError: false,
		},
		{
			name:      "UserID + ChannelID with blocked channel - returns error",
			userID:    testUserID,
			channelID: testChannelID,
			botConfig: llm.BotConfig{
				UserAccessLevel:    llm.UserAccessLevelAll,
				ChannelAccessLevel: llm.ChannelAccessLevelBlock,
				ChannelIDs:         []string{testChannelID},
			},
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("GetChannel", testChannelID).Return(&model.Channel{
					Id:     testChannelID,
					Type:   model.ChannelTypeOpen,
					TeamId: "team-123",
				}, nil).Once()
			},
			expectError: true,
			errorMsg:    "permission denied",
		},
		{
			name:      "UserID + ChannelID with blocked user - returns error",
			userID:    testUserID,
			channelID: testChannelID,
			botConfig: llm.BotConfig{
				UserAccessLevel: llm.UserAccessLevelBlock,
				UserIDs:         []string{testUserID},
			},
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("GetChannel", testChannelID).Return(&model.Channel{
					Id:     testChannelID,
					Type:   model.ChannelTypeOpen,
					TeamId: "team-123",
				}, nil).Once()
			},
			expectError: true,
			errorMsg:    "permission denied",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup bot
			tc.botConfig.Name = "testbot"
			tc.botConfig.DisplayName = "Test Bot"
			e.setupTestBot(tc.botConfig)

			// Inject fake LLM
			fakeLLM := NewFakeLLM("Test response")
			for _, bot := range e.bots.GetAllBots() {
				bot.SetLLMForTest(fakeLLM)
			}

			// Setup environment
			tc.envSetup(e)

			// Allow error logging
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create request with permissions fields
			request := bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "test message"},
				},
				UserID:    tc.userID,
				ChannelID: tc.channelID,
			}

			// Create bridge client and make request
			client := e.CreateBridgeClient()
			_, err := client.AgentCompletion(testBotUserID, request)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorMsg != "" {
					require.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBridgeGetBots(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name        string
		userID      string
		botConfigs  []llm.BotConfig
		expectBots  int
		validateRes func(t *testing.T, agents []bridgeclient.BridgeAgentInfo)
	}{
		{
			name:   "get all bots without user_id",
			userID: "",
			botConfigs: []llm.BotConfig{
				{
					Name:            "bot1",
					DisplayName:     "Bot One",
					ServiceID:       "service1",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
				{
					Name:            "bot2",
					DisplayName:     "Bot Two",
					ServiceID:       "service2",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
			},
			expectBots: 2,
			validateRes: func(t *testing.T, agents []bridgeclient.BridgeAgentInfo) {
				require.Len(t, agents, 2)
				// Verify agent fields are populated
				for _, agent := range agents {
					require.NotEmpty(t, agent.ID)
					require.NotEmpty(t, agent.DisplayName)
					require.NotEmpty(t, agent.Username)
					require.NotEmpty(t, agent.ServiceID)
					require.NotEmpty(t, agent.ServiceType)
				}
			},
		},
		{
			name:   "get filtered bots with user_id",
			userID: testUserID,
			botConfigs: []llm.BotConfig{
				{
					Name:            "bot1",
					DisplayName:     "Bot One",
					ServiceID:       "service1",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
				{
					Name:            "bot2",
					DisplayName:     "Bot Two",
					ServiceID:       "service2",
					UserAccessLevel: llm.UserAccessLevelAllow,
					UserIDs:         []string{testOtherUserID},
				},
			},
			expectBots: 1,
			validateRes: func(t *testing.T, agents []bridgeclient.BridgeAgentInfo) {
				require.Len(t, agents, 1)
				require.Equal(t, "bot1", agents[0].Username)
			},
		},
		{
			name:       "no bots configured",
			userID:     "",
			botConfigs: []llm.BotConfig{},
			expectBots: 0,
			validateRes: func(t *testing.T, agents []bridgeclient.BridgeAgentInfo) {
				require.Empty(t, agents)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup bots - create all at once
			allBots := make([]*bots.Bot, 0, len(tc.botConfigs))
			for i, config := range tc.botConfigs {
				mmBot := &model.Bot{
					UserId:      fmt.Sprintf("%s%02d", testBotUserID[:24], i),
					Username:    config.Name,
					DisplayName: config.DisplayName,
				}
				bot := bots.NewBot(config, llm.ServiceConfig{
					ID:   config.ServiceID,
					Name: config.ServiceID,
					Type: "test",
				}, mmBot, nil)
				allBots = append(allBots, bot)
			}
			e.bots.SetBotsForTesting(allBots)

			// Create bridge client and make request
			client := e.CreateBridgeClient()
			agents, err := client.GetAgents(tc.userID)
			require.NoError(t, err)

			require.Len(t, agents, tc.expectBots)
			if tc.validateRes != nil {
				tc.validateRes(t, agents)
			}
		})
	}
}

func TestBridgeGetServices(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		userID         string
		botConfigs     []llm.BotConfig
		expectServices int
		validateRes    func(t *testing.T, services []bridgeclient.BridgeServiceInfo)
	}{
		{
			name:   "get all services without user_id",
			userID: "",
			botConfigs: []llm.BotConfig{
				{
					Name:            "bot1",
					DisplayName:     "Bot One",
					ServiceID:       "service1",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
				{
					Name:            "bot2",
					DisplayName:     "Bot Two",
					ServiceID:       "service2",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
			},
			expectServices: 2,
			validateRes: func(t *testing.T, services []bridgeclient.BridgeServiceInfo) {
				require.Len(t, services, 2)
				// Verify service fields are populated
				for _, svc := range services {
					require.NotEmpty(t, svc.ID)
					require.NotEmpty(t, svc.Name)
					require.NotEmpty(t, svc.Type)
				}
			},
		},
		{
			name:   "deduplicate services from multiple bots",
			userID: "",
			botConfigs: []llm.BotConfig{
				{
					Name:            "bot1",
					DisplayName:     "Bot One",
					ServiceID:       "service1",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
				{
					Name:            "bot2",
					DisplayName:     "Bot Two",
					ServiceID:       "service1",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
			},
			expectServices: 1,
			validateRes: func(t *testing.T, services []bridgeclient.BridgeServiceInfo) {
				require.Len(t, services, 1)
			},
		},
		{
			name:   "filter services by user permissions",
			userID: testUserID,
			botConfigs: []llm.BotConfig{
				{
					Name:            "bot1",
					DisplayName:     "Bot One",
					ServiceID:       "service1",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
				{
					Name:            "bot2",
					DisplayName:     "Bot Two",
					ServiceID:       "service2",
					UserAccessLevel: llm.UserAccessLevelAllow,
					UserIDs:         []string{testOtherUserID},
				},
			},
			expectServices: 1,
			validateRes: func(t *testing.T, services []bridgeclient.BridgeServiceInfo) {
				require.Len(t, services, 1)
				require.Equal(t, "service1", services[0].ID)
			},
		},
		{
			name:           "no services configured",
			userID:         "",
			botConfigs:     []llm.BotConfig{},
			expectServices: 0,
			validateRes: func(t *testing.T, services []bridgeclient.BridgeServiceInfo) {
				require.Empty(t, services)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup bots - create all at once
			allBots := make([]*bots.Bot, 0, len(tc.botConfigs))
			for i, config := range tc.botConfigs {
				mmBot := &model.Bot{
					UserId:      fmt.Sprintf("%s%02d", testBotUserID[:24], i),
					Username:    config.Name,
					DisplayName: config.DisplayName,
				}
				bot := bots.NewBot(config, llm.ServiceConfig{
					ID:   config.ServiceID,
					Name: config.ServiceID,
					Type: "test",
				}, mmBot, nil)
				allBots = append(allBots, bot)
			}
			e.bots.SetBotsForTesting(allBots)

			// Create bridge client and make request
			client := e.CreateBridgeClient()
			services, err := client.GetServices(tc.userID)
			require.NoError(t, err)

			require.Len(t, services, tc.expectServices)
			if tc.validateRes != nil {
				tc.validateRes(t, services)
			}
		})
	}
}
