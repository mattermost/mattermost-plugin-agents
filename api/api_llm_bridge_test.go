// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/model"
	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
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
		{
			name:    "allowed tools not supported on service stream endpoint",
			service: "openai-service",
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{
					{Role: "user", Message: "Hello"},
				},
				AllowedTools: []string{"eligible_tool"},
			},
			serviceConfig: llm.ServiceConfig{
				ID:   "openai-service",
				Name: "OpenAI",
			},
			fakeLLM:     NewFakeLLM("test"),
			expectError: true,
			errorMsg:    "allowed_tools is only supported for agent completion endpoints",
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
			name:      "whitespace UserID and ChannelID are treated as unset",
			userID:    " \t ",
			channelID: " \n ",
			botConfig: llm.BotConfig{
				UserAccessLevel: llm.UserAccessLevelAllow,
				UserIDs:         []string{testUserID},
			},
			envSetup:    func(e *TestEnvironment) {},
			expectError: false,
		},
		{
			name:      "ChannelID only with valid channel ID - succeeds (user checks skipped)",
			userID:    "",
			channelID: testChannelID,
			botConfig: llm.BotConfig{
				UserAccessLevel: llm.UserAccessLevelBlock,
				UserIDs:         []string{testUserID},
			},
			envSetup:    func(e *TestEnvironment) {},
			expectError: false,
		},
		{
			name:      "ChannelID only with invalid channel ID - returns validation error",
			userID:    "",
			channelID: "bad",
			botConfig: llm.BotConfig{
				UserAccessLevel: llm.UserAccessLevelAll,
			},
			envSetup:    func(e *TestEnvironment) {},
			expectError: true,
			errorMsg:    "invalid channel_id",
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

func TestBridgeClientAgentCompletionRejectsInvalidPrincipalIDs(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLM("unused")
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()

	_, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts:  []bridgeclient.Post{{Role: "user", Message: "hello"}},
		UserID: "bad",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user_id")

	_, err = client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts:     []bridgeclient.Post{{Role: "user", Message: "hello"}},
		ChannelID: "bad",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid channel_id")
}

func TestBridgeClientServiceCompletionRejectsInvalidPrincipalIDs(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLM("unused"))
	}

	client := e.CreateBridgeClient()

	_, err := client.ServiceCompletion("service-id", bridgeclient.CompletionRequest{
		Posts:  []bridgeclient.Post{{Role: "user", Message: "hello"}},
		UserID: "bad",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user_id")

	_, err = client.ServiceCompletion("service-id", bridgeclient.CompletionRequest{
		Posts:     []bridgeclient.Post{{Role: "user", Message: "hello"}},
		ChannelID: "bad",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid channel_id")
}

func TestBridgeClientServiceCompletionTreatsWhitespacePrincipalIDsAsUnset(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAllow,
		UserIDs:         []string{testUserID},
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLM("service-ok"))
	}

	client := e.CreateBridgeClient()

	result, err := client.ServiceCompletion("service-id", bridgeclient.CompletionRequest{
		Posts:     []bridgeclient.Post{{Role: "user", Message: "hello"}},
		UserID:    " \t ",
		ChannelID: " \n ",
	})
	require.NoError(t, err)
	require.Equal(t, "service-ok", result)
}

func TestBridgeClientAgentCompletionTrimsNewlineWrappedPrincipalIDs(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(NewFakeLLM("agent-principal-trim-ok"))
	}

	client := e.CreateBridgeClient()

	result, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts:  []bridgeclient.Post{{Role: "user", Message: "hello"}},
		UserID: "\n\t" + testUserID + "\t\n",
	})
	require.NoError(t, err)
	require.Equal(t, "agent-principal-trim-ok", result)
}

func TestBridgeClientAgentCompletionStreamRejectsInvalidPrincipalIDs(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLM("unused")
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()

	_, err := client.AgentCompletionStream(testBotUserID, bridgeclient.CompletionRequest{
		Posts:  []bridgeclient.Post{{Role: "user", Message: "hello"}},
		UserID: "bad",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user_id")

	_, err = client.AgentCompletionStream(testBotUserID, bridgeclient.CompletionRequest{
		Posts:     []bridgeclient.Post{{Role: "user", Message: "hello"}},
		ChannelID: "bad",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid channel_id")
}

func TestBridgeClientServiceCompletionStreamRejectsInvalidPrincipalIDs(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLM("unused"))
	}

	client := e.CreateBridgeClient()

	_, err := client.ServiceCompletionStream("service-id", bridgeclient.CompletionRequest{
		Posts:  []bridgeclient.Post{{Role: "user", Message: "hello"}},
		UserID: "bad",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user_id")

	_, err = client.ServiceCompletionStream("service-id", bridgeclient.CompletionRequest{
		Posts:     []bridgeclient.Post{{Role: "user", Message: "hello"}},
		ChannelID: "bad",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid channel_id")
}

func TestBridgeClientServiceCompletionStreamTreatsWhitespacePrincipalIDsAsUnset(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAllow,
		UserIDs:         []string{testUserID},
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "stream-ok"},
			{Type: llm.EventTypeEnd, Value: nil},
		}))
	}

	client := e.CreateBridgeClient()

	result, err := client.ServiceCompletionStream("service-id", bridgeclient.CompletionRequest{
		Posts:     []bridgeclient.Post{{Role: "user", Message: "hello"}},
		UserID:    " \t ",
		ChannelID: " \n ",
	})
	require.NoError(t, err)

	text, readErr := result.ReadAll()
	require.NoError(t, readErr)
	require.Equal(t, "stream-ok", text)
}

func TestBridgeClientAgentCompletionStreamTrimsNewlineWrappedPrincipalIDs(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "agent-stream-principal-trim-ok"},
			{Type: llm.EventTypeEnd, Value: nil},
		}))
	}

	client := e.CreateBridgeClient()

	result, err := client.AgentCompletionStream(testBotUserID, bridgeclient.CompletionRequest{
		Posts:  []bridgeclient.Post{{Role: "user", Message: "hello"}},
		UserID: "\n\t" + testUserID + "\t\n",
	})
	require.NoError(t, err)

	text, readErr := result.ReadAll()
	require.NoError(t, readErr)
	require.Equal(t, "agent-stream-principal-trim-ok", text)
}

func TestBridgeClientServiceCompletionTrimsNewlineWrappedPrincipalIDs(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLM("service-principal-trim-ok"))
	}

	client := e.CreateBridgeClient()

	result, err := client.ServiceCompletion("service-id", bridgeclient.CompletionRequest{
		Posts:  []bridgeclient.Post{{Role: "user", Message: "hello"}},
		UserID: "\n\t" + testUserID + "\t\n",
	})
	require.NoError(t, err)
	require.Equal(t, "service-principal-trim-ok", result)
}

func TestBridgeClientServiceCompletionStreamTrimsNewlineWrappedPrincipalIDs(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "service-stream-principal-trim-ok"},
			{Type: llm.EventTypeEnd, Value: nil},
		}))
	}

	client := e.CreateBridgeClient()

	result, err := client.ServiceCompletionStream("service-id", bridgeclient.CompletionRequest{
		Posts:  []bridgeclient.Post{{Role: "user", Message: "hello"}},
		UserID: "\n\t" + testUserID + "\t\n",
	})
	require.NoError(t, err)

	text, readErr := result.ReadAll()
	require.NoError(t, readErr)
	require.Equal(t, "service-stream-principal-trim-ok", text)
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
		{
			name:   "agents are sorted by display name",
			userID: "",
			botConfigs: []llm.BotConfig{
				{
					Name:            "bot-zulu",
					DisplayName:     "Zulu Bot",
					ServiceID:       "service-z",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
				{
					Name:            "bot-alpha",
					DisplayName:     "Alpha Bot",
					ServiceID:       "service-a",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
			},
			expectBots: 2,
			validateRes: func(t *testing.T, agents []bridgeclient.BridgeAgentInfo) {
				require.Len(t, agents, 2)
				require.Equal(t, "Alpha Bot", agents[0].DisplayName)
				require.Equal(t, "Zulu Bot", agents[1].DisplayName)
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
		{
			name:   "services are sorted by name",
			userID: "",
			botConfigs: []llm.BotConfig{
				{
					Name:            "bot-zulu",
					DisplayName:     "Zulu Bot",
					ServiceID:       "service-zulu",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
				{
					Name:            "bot-alpha",
					DisplayName:     "Alpha Bot",
					ServiceID:       "service-alpha",
					UserAccessLevel: llm.UserAccessLevelAll,
				},
			},
			expectServices: 2,
			validateRes: func(t *testing.T, services []bridgeclient.BridgeServiceInfo) {
				require.Len(t, services, 2)
				require.Equal(t, "service-alpha", services[0].Name)
				require.Equal(t, "service-zulu", services[1].Name)
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

func TestBridgeGetBotsSortsByIDWhenDisplayNameMatches(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botA := bots.NewBot(
		llm.BotConfig{
			Name:            "bot-a",
			DisplayName:     "Shared Name",
			ServiceID:       "service-a",
			UserAccessLevel: llm.UserAccessLevelAll,
		},
		llm.ServiceConfig{ID: "service-a", Name: "service-a", Type: "test"},
		&model.Bot{
			UserId:      fmt.Sprintf("%s01", testBotUserID[:24]),
			Username:    "bot-a",
			DisplayName: "Shared Name",
		},
		nil,
	)
	botB := bots.NewBot(
		llm.BotConfig{
			Name:            "bot-b",
			DisplayName:     "Shared Name",
			ServiceID:       "service-b",
			UserAccessLevel: llm.UserAccessLevelAll,
		},
		llm.ServiceConfig{ID: "service-b", Name: "service-b", Type: "test"},
		&model.Bot{
			UserId:      fmt.Sprintf("%s00", testBotUserID[:24]),
			Username:    "bot-b",
			DisplayName: "Shared Name",
		},
		nil,
	)
	e.bots.SetBotsForTesting([]*bots.Bot{botA, botB})

	client := e.CreateBridgeClient()
	agents, err := client.GetAgents("")
	require.NoError(t, err)
	require.Len(t, agents, 2)
	require.Equal(t, fmt.Sprintf("%s00", testBotUserID[:24]), agents[0].ID)
	require.Equal(t, fmt.Sprintf("%s01", testBotUserID[:24]), agents[1].ID)
}

func TestBridgeGetServicesSortsByIDWhenNameMatches(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botA := bots.NewBot(
		llm.BotConfig{
			Name:            "bot-a",
			DisplayName:     "Bot A",
			ServiceID:       "service-b",
			UserAccessLevel: llm.UserAccessLevelAll,
		},
		llm.ServiceConfig{ID: "service-b", Name: "Shared Service", Type: "test"},
		&model.Bot{
			UserId:      fmt.Sprintf("%s01", testBotUserID[:24]),
			Username:    "bot-a",
			DisplayName: "Bot A",
		},
		nil,
	)
	botB := bots.NewBot(
		llm.BotConfig{
			Name:            "bot-b",
			DisplayName:     "Bot B",
			ServiceID:       "service-a",
			UserAccessLevel: llm.UserAccessLevelAll,
		},
		llm.ServiceConfig{ID: "service-a", Name: "Shared Service", Type: "test"},
		&model.Bot{
			UserId:      fmt.Sprintf("%s00", testBotUserID[:24]),
			Username:    "bot-b",
			DisplayName: "Bot B",
		},
		nil,
	)
	e.bots.SetBotsForTesting([]*bots.Bot{botA, botB})

	client := e.CreateBridgeClient()
	services, err := client.GetServices("")
	require.NoError(t, err)
	require.Len(t, services, 2)
	require.Equal(t, "service-a", services[0].ID)
	require.Equal(t, "service-b", services[1].ID)
}

func setupBridgeEligibleMCPServer(t *testing.T, toolNames []string) *httptest.Server {
	t.Helper()

	server := gosdkmcp.NewServer(
		&gosdkmcp.Implementation{
			Name:    "bridge-test-mcp-server",
			Version: "1.0.0",
		},
		nil,
	)

	for _, toolName := range toolNames {
		name := toolName
		server.AddTool(
			&gosdkmcp.Tool{
				Name:        name,
				Description: "discovered " + name,
				InputSchema: llm.NewJSONSchemaFromStruct[struct{}](),
			},
			func(_ context.Context, _ *gosdkmcp.CallToolRequest) (*gosdkmcp.CallToolResult, error) {
				return &gosdkmcp.CallToolResult{
					Content: []gosdkmcp.Content{
						&gosdkmcp.TextContent{Text: "ok"},
					},
					IsError: false,
				}, nil
			},
		)
	}

	handler := gosdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *gosdkmcp.Server {
		return server
	}, nil)

	return httptest.NewServer(handler)
}

func TestBridgeClientAgentCompletionUsesAgentContextAndPrompt(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:               "testbot",
		DisplayName:        "Test Bot",
		CustomInstructions: "Always answer with a single short sentence.",
		UserAccessLevel:    llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLM("Hello! How can I help?")
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()
	result, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Hi there"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "Hello! How can I help?", result)

	require.NotNil(t, fakeLLM.LastConversation.Context)
	require.Equal(t, "Test Bot", fakeLLM.LastConversation.Context.BotName)
	require.Equal(t, "testbot", fakeLLM.LastConversation.Context.BotUsername)
	require.NotNil(t, fakeLLM.LastConversation.Context.RequestingUser)
	require.Equal(t, bridgeSyntheticUsername, fakeLLM.LastConversation.Context.RequestingUser.Username)

	require.GreaterOrEqual(t, len(fakeLLM.LastConversation.Posts), 2)
	require.Equal(t, llm.PostRoleSystem, fakeLLM.LastConversation.Posts[0].Role)
	require.Contains(t, fakeLLM.LastConversation.Posts[0].Message, "You are called Test Bot")
	require.Contains(t, fakeLLM.LastConversation.Posts[0].Message, "Always answer with a single short sentence.")
	require.Equal(t, llm.PostRoleUser, fakeLLM.LastConversation.Posts[1].Role)
	require.Equal(t, "Hi there", fakeLLM.LastConversation.Posts[1].Message)
	require.True(t, fakeLLM.LastConfig.ToolsDisabled)
}

func TestBridgeClientServiceCompletionRejectsAllowedTools(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLM("ignored"))
	}

	client := e.CreateBridgeClient()
	_, err := client.ServiceCompletion("service-id", bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Hi"},
		},
		AllowedTools: []string{"eligible_tool"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "allowed_tools is only supported for agent completion endpoints")
}

func TestBridgeGetAgentToolsReturnsEligibleOnly(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := setupBridgeEligibleMCPServer(t, []string{"eligible_tool"})
	defer server.Close()

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "service-account-server",
				Enabled: true,
				BaseURL: server.URL,
				Headers: map[string]string{"Authorization": "Bearer test-token"},
			},
			{
				Name:    "non-eligible-no-headers",
				Enabled: true,
				BaseURL: server.URL,
			},
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "eligible_tool",
					Description: "eligible from context",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
				{
					Name:        "ineligible_tool",
					Description: "should be filtered out",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	client := e.CreateBridgeClient()
	tools, err := client.GetAgentTools(testBotUserID, "")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "eligible_tool", tools[0].Name)
	require.Equal(t, "eligible from context", tools[0].Description)
}

func TestBridgeGetAgentToolsSkipsUnreachableEligibleServer(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := setupBridgeEligibleMCPServer(t, []string{"eligible_tool"})
	defer server.Close()

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "unreachable-server",
				Enabled: true,
				BaseURL: "http://127.0.0.1:1",
				Headers: map[string]string{"Authorization": "Bearer bad"},
			},
			{
				Name:    "reachable-server",
				Enabled: true,
				BaseURL: server.URL,
				Headers: map[string]string{"Authorization": "Bearer good"},
			},
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "eligible_tool",
					Description: "eligible from context",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	client := e.CreateBridgeClient()
	tools, err := client.GetAgentTools(testBotUserID, "")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "eligible_tool", tools[0].Name)
}

func TestBridgeGetAgentToolsReturnsSortedToolsForAllowedUser(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := setupBridgeEligibleMCPServer(t, []string{"z_tool", "a_tool"})
	defer server.Close()

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "service-account-server",
				Enabled: true,
				BaseURL: server.URL,
				Headers: map[string]string{"Authorization": "Bearer test-token"},
			},
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "z_tool",
					Description: "tool z",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
				{
					Name:        "a_tool",
					Description: "tool a",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAllow,
		UserIDs:         []string{testUserID},
	}
	e.setupTestBot(botConfig)

	client := e.CreateBridgeClient()
	tools, err := client.GetAgentTools(testBotUserID, testUserID)
	require.NoError(t, err)
	require.Len(t, tools, 2)
	require.Equal(t, "a_tool", tools[0].Name)
	require.Equal(t, "z_tool", tools[1].Name)
}

func TestBridgeClientAgentCompletionAllowedToolsEnablesAutoRun(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := setupBridgeEligibleMCPServer(t, []string{"eligible_tool"})
	defer server.Close()

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "service-account-server",
				Enabled: true,
				BaseURL: server.URL,
				Headers: map[string]string{"Authorization": "Bearer test-token"},
			},
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "eligible_tool",
					Description: "eligible from context",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLM("auto run enabled")
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()
	result, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Use the tool"},
		},
		AllowedTools: []string{"eligible_tool"},
	})
	require.NoError(t, err)
	require.Equal(t, "auto run enabled", result)
	require.False(t, fakeLLM.LastConfig.ToolsDisabled)
	require.Equal(t, []string{"eligible_tool"}, fakeLLM.LastConfig.AutoRunTools)
	require.NotNil(t, fakeLLM.LastConversation.Context)
	require.NotNil(t, fakeLLM.LastConversation.Context.Tools)
	require.Len(t, fakeLLM.LastConversation.Context.Tools.GetTools(), 1)
}

func TestBridgeClientAgentCompletionStreamAllowedToolsEnablesAutoRun(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := setupBridgeEligibleMCPServer(t, []string{"eligible_tool"})
	defer server.Close()

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "service-account-server",
				Enabled: true,
				BaseURL: server.URL,
				Headers: map[string]string{"Authorization": "Bearer test-token"},
			},
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "eligible_tool",
					Description: "eligible from context",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
		{Type: llm.EventTypeText, Value: "stream"},
		{Type: llm.EventTypeEnd, Value: nil},
	})
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()
	result, err := client.AgentCompletionStream(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Use tool in stream"},
		},
		AllowedTools: []string{"eligible_tool"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	text, readErr := result.ReadAll()
	require.NoError(t, readErr)
	require.Equal(t, "stream", text)

	require.False(t, fakeLLM.LastConfig.ToolsDisabled)
	require.Equal(t, []string{"eligible_tool"}, fakeLLM.LastConfig.AutoRunTools)
	require.NotNil(t, fakeLLM.LastConversation.Context)
	require.NotNil(t, fakeLLM.LastConversation.Context.Tools)
	require.Len(t, fakeLLM.LastConversation.Context.Tools.GetTools(), 1)
}

func TestBridgeClientAgentCompletionStreamDisablesToolsByDefault(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
		{Type: llm.EventTypeText, Value: "default-stream"},
		{Type: llm.EventTypeEnd, Value: nil},
	})
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()
	result, err := client.AgentCompletionStream(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "No tools allowed"},
		},
	})
	require.NoError(t, err)

	text, readErr := result.ReadAll()
	require.NoError(t, readErr)
	require.Equal(t, "default-stream", text)

	require.True(t, fakeLLM.LastConfig.ToolsDisabled)
	require.Empty(t, fakeLLM.LastConfig.AutoRunTools)
}

func TestBridgeClientAgentCompletionAllowedToolsDeduplicatesList(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := setupBridgeEligibleMCPServer(t, []string{"eligible_tool"})
	defer server.Close()

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "service-account-server",
				Enabled: true,
				BaseURL: server.URL,
				Headers: map[string]string{"Authorization": "Bearer test-token"},
			},
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "eligible_tool",
					Description: "eligible from context",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLM("deduped")
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()
	result, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Run tool once"},
		},
		AllowedTools: []string{"eligible_tool", "eligible_tool"},
	})
	require.NoError(t, err)
	require.Equal(t, "deduped", result)
	require.Equal(t, []string{"eligible_tool"}, fakeLLM.LastConfig.AutoRunTools)
	require.NotNil(t, fakeLLM.LastConversation.Context.Tools)
	require.Len(t, fakeLLM.LastConversation.Context.Tools.GetTools(), 1)
}

func TestBridgeClientAgentCompletionAllowedToolsTrimsNames(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := setupBridgeEligibleMCPServer(t, []string{"eligible_tool"})
	defer server.Close()

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "service-account-server",
				Enabled: true,
				BaseURL: server.URL,
				Headers: map[string]string{"Authorization": "Bearer test-token"},
			},
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "eligible_tool",
					Description: "eligible from context",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLM("trimmed")
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()
	result, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Run trimmed tool"},
		},
		AllowedTools: []string{" eligible_tool "},
	})
	require.NoError(t, err)
	require.Equal(t, "trimmed", result)
	require.Equal(t, []string{"eligible_tool"}, fakeLLM.LastConfig.AutoRunTools)
}

func TestBridgeClientAgentCompletionRejectsIneligibleAllowedTool(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := setupBridgeEligibleMCPServer(t, []string{"eligible_tool"})
	defer server.Close()

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "service-account-server",
				Enabled: true,
				BaseURL: server.URL,
				Headers: map[string]string{"Authorization": "Bearer test-token"},
			},
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "eligible_tool",
					Description: "eligible from context",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(NewFakeLLM("ignored"))
	}

	client := e.CreateBridgeClient()
	_, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Try disallowed"},
		},
		AllowedTools: []string{"not_eligible_tool"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not eligible or not available for this agent")
}

func TestBridgeClientAgentCompletionStreamRejectsIneligibleAllowedTool(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := setupBridgeEligibleMCPServer(t, []string{"eligible_tool"})
	defer server.Close()

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "service-account-server",
				Enabled: true,
				BaseURL: server.URL,
				Headers: map[string]string{"Authorization": "Bearer test-token"},
			},
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "eligible_tool",
					Description: "eligible from context",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(NewFakeLLM("ignored"))
	}

	client := e.CreateBridgeClient()
	_, err := client.AgentCompletionStream(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Try disallowed in stream"},
		},
		AllowedTools: []string{"not_eligible_tool"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not eligible or not available for this agent")
}

func TestBridgeClientAgentCompletionAllowedToolsSkipsUnreachableEligibleServer(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := setupBridgeEligibleMCPServer(t, []string{"eligible_tool"})
	defer server.Close()

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "unreachable-server",
				Enabled: true,
				BaseURL: "http://127.0.0.1:1",
				Headers: map[string]string{"Authorization": "Bearer bad"},
			},
			{
				Name:    "reachable-server",
				Enabled: true,
				BaseURL: server.URL,
				Headers: map[string]string{"Authorization": "Bearer good"},
			},
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "eligible_tool",
					Description: "eligible from context",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLM("reachable still works")
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()
	result, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Run tool with partial server outage"},
		},
		AllowedTools: []string{"eligible_tool"},
	})
	require.NoError(t, err)
	require.Equal(t, "reachable still works", result)
}

func TestBridgeGetAgentToolsRespectsUserPermissions(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAllow,
		UserIDs:         []string{testOtherUserID},
	}
	e.setupTestBot(botConfig)

	client := e.CreateBridgeClient()
	_, err := client.GetAgentTools(testBotUserID, testUserID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")
}

func TestBridgeGetAgentToolsAgentNotFound(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	client := e.CreateBridgeClient()
	_, err := client.GetAgentTools(testNonexistentBot, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "bot not found")
}

func TestBridgeGetAgentToolsRejectsInvalidAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents/bad/tools", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "invalid agent ID")
}

func TestBridgeGetAgentToolsRejectsWhitespaceAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents/%20/tools", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "invalid agent ID")
}

func TestBridgeGetAgentToolsRejectsNewlineAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents/%0A/tools", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "invalid agent ID")
}

func TestBridgeGetAgentToolsTrimsAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
		DisableTools:    true,
	}
	e.setupTestBot(botConfig)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents/%20"+testBotUserID+"%20/tools", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), `"tools":[]`)
}

func TestBridgeGetAgentToolsTrimsTabbedAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
		DisableTools:    true,
	}
	e.setupTestBot(botConfig)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents/%09"+testBotUserID+"%09/tools", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), `"tools":[]`)
}

func TestBridgeGetAgentToolsTrimsNewlineAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
		DisableTools:    true,
	}
	e.setupTestBot(botConfig)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents/%0A"+testBotUserID+"%0A/tools", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), `"tools":[]`)
}

func TestBridgeGetAgentToolsRejectsInvalidUserIDQuery(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents/"+testBotUserID+"/tools?user_id=bad", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "invalid user_id")
}

func TestBridgeGetAgentsRejectsInvalidUserIDQuery(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents?user_id=bad", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "invalid user_id")
}

func TestBridgeGetServicesRejectsInvalidUserIDQuery(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/services?user_id=bad", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "invalid user_id")
}

func TestBridgeAgentCompletionRejectsInvalidAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/agent/bad/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "invalid agent ID")
}

func TestBridgeAgentCompletionRejectsNewlineAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/agent/%0A/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "invalid agent ID")
}

func TestBridgeAgentCompletionStreamRejectsInvalidAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/agent/bad",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "invalid agent ID")
}

func TestBridgeAgentCompletionStreamRejectsNewlineAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/agent/%0A",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "invalid agent ID")
}

func TestBridgeAgentCompletionTrimsAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(NewFakeLLM("trimmed-agent-ok"))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/agent/%20"+testBotUserID+"%20/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-agent-ok")
}

func TestBridgeAgentCompletionTrimsTabbedAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(NewFakeLLM("trimmed-agent-tab-ok"))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/agent/%09"+testBotUserID+"%09/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-agent-tab-ok")
}

func TestBridgeAgentCompletionTrimsNewlineAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(NewFakeLLM("trimmed-agent-newline-ok"))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/agent/%0A"+testBotUserID+"%0A/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-agent-newline-ok")
}

func TestBridgeAgentCompletionStreamTrimsAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "trimmed-agent-stream-ok"},
			{Type: llm.EventTypeEnd, Value: nil},
		}))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/agent/%20"+testBotUserID+"%20",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-agent-stream-ok")
}

func TestBridgeAgentCompletionStreamTrimsTabbedAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "trimmed-agent-stream-tab-ok"},
			{Type: llm.EventTypeEnd, Value: nil},
		}))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/agent/%09"+testBotUserID+"%09",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-agent-stream-tab-ok")
}

func TestBridgeAgentCompletionStreamTrimsNewlineAgentPath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "trimmed-agent-stream-newline-ok"},
			{Type: llm.EventTypeEnd, Value: nil},
		}))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/agent/%0A"+testBotUserID+"%0A",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-agent-stream-newline-ok")
}

func TestBridgeServiceCompletionRejectsWhitespaceServicePath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/%20/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "service parameter is required")
}

func TestBridgeServiceCompletionRejectsNewlineServicePath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/%0A/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "service parameter is required")
}

func TestBridgeServiceCompletionStreamRejectsWhitespaceServicePath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/%20",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "service parameter is required")
}

func TestBridgeServiceCompletionStreamRejectsNewlineServicePath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/%0A",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "service parameter is required")
}

func TestBridgeServiceCompletionTrimsServicePath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLM("trimmed-service-ok"))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/%20service-id%20/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-service-ok")
}

func TestBridgeServiceCompletionTrimsTabbedServicePath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLM("trimmed-service-tab-ok"))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/%09service-id%09/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-service-tab-ok")
}

func TestBridgeServiceCompletionTrimsNewlineServicePath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLM("trimmed-service-newline-ok"))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/%0Aservice-id%0A/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-service-newline-ok")
}

func TestBridgeServiceCompletionStreamTrimsServicePath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "trimmed-stream-ok"},
			{Type: llm.EventTypeEnd, Value: nil},
		}))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/%20service-id%20",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-stream-ok")
}

func TestBridgeServiceCompletionStreamTrimsTabbedServicePath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "trimmed-service-stream-tab-ok"},
			{Type: llm.EventTypeEnd, Value: nil},
		}))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/%09service-id%09",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-service-stream-tab-ok")
}

func TestBridgeServiceCompletionStreamTrimsNewlineServicePath(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
		bot.SetLLMForTest(NewFakeLLMWithStreamEvents([]llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "trimmed-service-stream-newline-ok"},
			{Type: llm.EventTypeEnd, Value: nil},
		}))
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/%0Aservice-id%0A",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "trimmed-service-stream-newline-ok")
}

func TestBridgeGetAgentsTreatsWhitespaceUserIDQueryAsUnset(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAllow,
		UserIDs:         []string{testOtherUserID},
	}
	e.setupTestBot(botConfig)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents?user_id=%20%09%20", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), testBotUserID)
}

func TestBridgeGetAgentsTreatsNewlineUserIDQueryAsUnset(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAllow,
		UserIDs:         []string{testOtherUserID},
	}
	e.setupTestBot(botConfig)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents?user_id=%0A", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), testBotUserID)
}

func TestBridgeGetServicesTreatsWhitespaceUserIDQueryAsUnset(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAllow,
		UserIDs:         []string{testOtherUserID},
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "svc-trim-test", Name: "trim-service", Type: "openai"})
	}

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/services?user_id=%20%09%20", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "svc-trim-test")
}

func TestBridgeGetServicesTreatsNewlineUserIDQueryAsUnset(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAllow,
		UserIDs:         []string{testOtherUserID},
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "svc-trim-test", Name: "trim-service", Type: "openai"})
	}

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/services?user_id=%0A", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(respBody), "svc-trim-test")
}

func TestBridgeGetAgentToolsTreatsWhitespaceUserIDQueryAsUnset(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAllow,
		UserIDs:         []string{testOtherUserID},
		DisableTools:    true,
	}
	e.setupTestBot(botConfig)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents/"+testBotUserID+"/tools?user_id=%20%09%20", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestBridgeGetAgentToolsTreatsNewlineUserIDQueryAsUnset(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAllow,
		UserIDs:         []string{testOtherUserID},
		DisableTools:    true,
	}
	e.setupTestBot(botConfig)

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents/"+testBotUserID+"/tools?user_id=%0A", nil)
	require.NoError(t, err)

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestBridgeClientAgentCompletionRejectsExplicitEmptyAllowedToolsArray(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	// Send a raw JSON payload to explicitly include allowed_tools: [].
	rawBody := `{"posts":[{"role":"user","message":"Hello"}],"allowed_tools":[]}`
	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/mattermost-ai/bridge/v1/completion/agent/%s/nostream", testBotUserID),
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(respBody), "allowed_tools cannot be empty")
}

func TestBridgeServiceCompletionRejectsExplicitEmptyAllowedToolsArray(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}],"allowed_tools":[]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/service-id/nostream",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(respBody), "allowed_tools is only supported for agent completion endpoints")
}

func TestBridgeServiceCompletionStreamRejectsExplicitEmptyAllowedToolsArray(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)
	for _, bot := range e.bots.GetAllBots() {
		bot.SetServiceForTest(llm.ServiceConfig{ID: "service-id", Name: "service-name"})
	}

	rawBody := `{"posts":[{"role":"user","message":"Hello"}],"allowed_tools":[]}`
	req, err := http.NewRequest(
		http.MethodPost,
		"/mattermost-ai/bridge/v1/completion/service/service-id",
		strings.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp := (&testPluginAPI{api: e.api}).PluginHTTP(req)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(respBody), "allowed_tools is only supported for agent completion endpoints")
}

func TestBridgeClientAgentCompletionRejectsInvalidAllowedToolsEntry(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	client := e.CreateBridgeClient()
	_, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Hello"},
		},
		AllowedTools: []string{"   "},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "allowed_tools cannot contain empty tool names")
}

func TestBridgeClientAgentCompletionRejectsAllowedToolsWhenAgentToolsDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
		DisableTools:    true,
	}
	e.setupTestBot(botConfig)

	client := e.CreateBridgeClient()
	_, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Hello"},
		},
		AllowedTools: []string{"eligible_tool"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent has tools disabled")
}

func TestBridgeGetAgentToolsReturnsEmptyWhenAgentToolsDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
		DisableTools:    true,
	}
	e.setupTestBot(botConfig)

	client := e.CreateBridgeClient()
	tools, err := client.GetAgentTools(testBotUserID, "")
	require.NoError(t, err)
	require.Empty(t, tools)
}

func TestBridgeGetAgentToolsReturnsEmptyWhenMCPDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	// MCP disabled means no bridge-eligible tools even if context has tools.
	e.config.mcpConfig = mcp.Config{
		Enabled: false,
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "context_only_tool",
					Description: "should not be bridge-eligible without MCP",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	client := e.CreateBridgeClient()
	tools, err := client.GetAgentTools(testBotUserID, "")
	require.NoError(t, err)
	require.Empty(t, tools)
}

func TestBridgeClientAgentCompletionAllowedToolsFailsWhenNoEligibleToolsAvailable(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	// MCP disabled means allowed_tools cannot resolve any bridge-eligible tools.
	e.config.mcpConfig = mcp.Config{
		Enabled: false,
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "context_only_tool",
					Description: "present in context but not bridge-eligible",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	client := e.CreateBridgeClient()
	_, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Try tool call"},
		},
		AllowedTools: []string{"context_only_tool"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no eligible tools available for this agent")
}

func TestBridgeClientAgentCompletionStreamAllowedToolsFailsWhenNoEligibleToolsAvailable(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	// MCP disabled means allowed_tools cannot resolve any bridge-eligible tools.
	e.config.mcpConfig = mcp.Config{
		Enabled: false,
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:        "context_only_tool",
					Description: "present in context but not bridge-eligible",
					Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
			},
		},
		nil,
		&testLLMContextConfigProvider{},
	)

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	client := e.CreateBridgeClient()
	_, err := client.AgentCompletionStream(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Try tool call in stream"},
		},
		AllowedTools: []string{"context_only_tool"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no eligible tools available for this agent")
}

func TestNormalizeBridgeOptionalUserID(t *testing.T) {
	t.Run("empty and whitespace user IDs become unset", func(t *testing.T) {
		normalized, err := normalizeBridgeOptionalUserID("")
		require.NoError(t, err)
		require.Equal(t, "", normalized)

		normalized, err = normalizeBridgeOptionalUserID(" \t ")
		require.NoError(t, err)
		require.Equal(t, "", normalized)
	})

	t.Run("valid user ID is trimmed", func(t *testing.T) {
		normalized, err := normalizeBridgeOptionalUserID("  abcdefghijklmnopqrstuvwxyz  ")
		require.NoError(t, err)
		require.Equal(t, "abcdefghijklmnopqrstuvwxyz", normalized)

		normalized, err = normalizeBridgeOptionalUserID("\nabcdefghijklmnopqrstuvwxyz\t")
		require.NoError(t, err)
		require.Equal(t, "abcdefghijklmnopqrstuvwxyz", normalized)
	})

	t.Run("invalid user ID returns validation error", func(t *testing.T) {
		_, err := normalizeBridgeOptionalUserID("bad")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid user_id")
	})
}

func TestNormalizeBridgeCompletionPrincipalIDs(t *testing.T) {
	t.Run("empty and whitespace principal IDs become unset", func(t *testing.T) {
		userID, channelID, err := normalizeBridgeCompletionPrincipalIDs("", " \t ")
		require.NoError(t, err)
		require.Equal(t, "", userID)
		require.Equal(t, "", channelID)
	})

	t.Run("valid principal IDs are trimmed", func(t *testing.T) {
		userID, channelID, err := normalizeBridgeCompletionPrincipalIDs(
			"  abcdefghijklmnopqrstuvwxyz ",
			"\tzyxwvutsrqponmlkjihgfedcba  ",
		)
		require.NoError(t, err)
		require.Equal(t, "abcdefghijklmnopqrstuvwxyz", userID)
		require.Equal(t, "zyxwvutsrqponmlkjihgfedcba", channelID)

		userID, channelID, err = normalizeBridgeCompletionPrincipalIDs(
			"\nabcdefghijklmnopqrstuvwxyz\t",
			"\nzyxwvutsrqponmlkjihgfedcba\t",
		)
		require.NoError(t, err)
		require.Equal(t, "abcdefghijklmnopqrstuvwxyz", userID)
		require.Equal(t, "zyxwvutsrqponmlkjihgfedcba", channelID)
	})

	t.Run("invalid user ID returns validation error", func(t *testing.T) {
		_, _, err := normalizeBridgeCompletionPrincipalIDs("bad", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid user_id")
	})

	t.Run("invalid channel ID returns validation error", func(t *testing.T) {
		_, _, err := normalizeBridgeCompletionPrincipalIDs("", "bad")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid channel_id")
	})
}

func TestNormalizeBridgeAgentID(t *testing.T) {
	t.Run("valid agent ID is trimmed", func(t *testing.T) {
		normalized, err := normalizeBridgeAgentID(" \tabcdefghijklmnopqrstuvwxyz ")
		require.NoError(t, err)
		require.Equal(t, "abcdefghijklmnopqrstuvwxyz", normalized)
	})

	t.Run("invalid agent ID returns validation error", func(t *testing.T) {
		_, err := normalizeBridgeAgentID("bad")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid agent ID")
	})

	t.Run("whitespace-only agent ID returns validation error", func(t *testing.T) {
		_, err := normalizeBridgeAgentID(" \t ")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid agent ID")
	})
}

func TestNormalizeBridgeServiceIdentifier(t *testing.T) {
	t.Run("valid service identifier is trimmed", func(t *testing.T) {
		normalized, err := normalizeBridgeServiceIdentifier(" \topenai ")
		require.NoError(t, err)
		require.Equal(t, "openai", normalized)

		normalized, err = normalizeBridgeServiceIdentifier("\nservice-id\t")
		require.NoError(t, err)
		require.Equal(t, "service-id", normalized)
	})

	t.Run("empty and whitespace service values are rejected", func(t *testing.T) {
		_, err := normalizeBridgeServiceIdentifier("")
		require.Error(t, err)
		require.Contains(t, err.Error(), "service parameter is required")

		_, err = normalizeBridgeServiceIdentifier(" \n\t ")
		require.Error(t, err)
		require.Contains(t, err.Error(), "service parameter is required")
	})
}

func TestBridgeToolDiscoveryUserID(t *testing.T) {
	t.Run("empty and whitespace user IDs fallback to synthetic user", func(t *testing.T) {
		require.Equal(t, bridgeSyntheticUserID, bridgeToolDiscoveryUserID(""))
		require.Equal(t, bridgeSyntheticUserID, bridgeToolDiscoveryUserID(" \t "))
		require.Equal(t, bridgeSyntheticUserID, bridgeToolDiscoveryUserID(" \n\t "))
	})

	t.Run("non-empty user ID is trimmed and preserved", func(t *testing.T) {
		require.Equal(t, "abcdefghijklmnopqrstuvwxyz", bridgeToolDiscoveryUserID("  abcdefghijklmnopqrstuvwxyz  "))
	})
}
