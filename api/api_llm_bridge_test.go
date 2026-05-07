// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockEmbeddedMCPServer implements mcp.EmbeddedMCPServer for testing.
// It creates a simple in-memory MCP server with predefined tools.
type mockEmbeddedMCPServer struct {
	mcpServer *gosdkmcp.Server
}

func newMockEmbeddedMCPServer(toolNames []string) *mockEmbeddedMCPServer {
	server := gosdkmcp.NewServer(
		&gosdkmcp.Implementation{
			Name:    "test-embedded-server",
			Version: "1.0.0",
		},
		nil,
	)
	for _, name := range toolNames {
		tool := &gosdkmcp.Tool{
			Name:        name,
			Description: "embedded " + name,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		}
		server.AddTool(tool, func(ctx context.Context, req *gosdkmcp.CallToolRequest) (*gosdkmcp.CallToolResult, error) {
			return &gosdkmcp.CallToolResult{}, nil
		})
	}
	return &mockEmbeddedMCPServer{mcpServer: server}
}

func (m *mockEmbeddedMCPServer) CreateClientTransport(userID, sessionID string, pluginAPI *pluginapi.Client) (*gosdkmcp.InMemoryTransport, error) {
	serverTransport, clientTransport := gosdkmcp.NewInMemoryTransports()
	go func() {
		_ = m.mcpServer.Run(context.Background(), serverTransport)
	}()
	return clientTransport, nil
}

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

func TestBridgeClientContextEnrichment(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	request := bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "hello"},
		},
		UserID:    testUserID,
		ChannelID: testChannelID,
	}

	tests := []struct {
		name              string
		service           llm.ServiceConfig
		call              func(client *bridgeclient.Client, req bridgeclient.CompletionRequest) error
		expectedOperation string
		expectedSubType   string
	}{
		{
			name: "agent non-stream request",
			service: llm.ServiceConfig{
				ID:           "svc-agent",
				Name:         "svc-agent",
				Type:         "openai",
				DefaultModel: "gpt-4.1",
			},
			call: func(client *bridgeclient.Client, req bridgeclient.CompletionRequest) error {
				_, err := client.AgentCompletion(testBotUserID, req)
				return err
			},
			expectedOperation: llm.OperationBridgeAgent,
			expectedSubType:   llm.SubTypeNoStream,
		},
		{
			name: "service stream request",
			service: llm.ServiceConfig{
				ID:           "svc-service",
				Name:         "svc-service",
				Type:         "anthropic",
				DefaultModel: "claude-3-7-sonnet",
			},
			call: func(client *bridgeclient.Client, req bridgeclient.CompletionRequest) error {
				streamResult, err := client.ServiceCompletionStream("svc-service", req)
				if err != nil {
					return err
				}
				_, err = streamResult.ReadAll()
				return err
			},
			expectedOperation: llm.OperationBridgeService,
			expectedSubType:   llm.SubTypeStreaming,
		},
		{
			name: "agent non-stream request with caller operation override",
			service: llm.ServiceConfig{
				ID:           "svc-custom-operation",
				Name:         "svc-custom-operation",
				Type:         "openai",
				DefaultModel: "gpt-4.1",
			},
			call: func(client *bridgeclient.Client, req bridgeclient.CompletionRequest) error {
				req.Operation = "playbooks_summary"
				req.OperationSubType = "incident_report"
				_, err := client.AgentCompletion(testBotUserID, req)
				return err
			},
			expectedOperation: "playbooks_summary",
			expectedSubType:   "incident_report",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			botConfig := llm.BotConfig{
				Name:               "testbot",
				DisplayName:        "Test Bot",
				UserAccessLevel:    llm.UserAccessLevelAll,
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				ServiceID:          tc.service.ID,
			}
			e.setupTestBot(botConfig)

			fakeLLM := NewFakeLLM("Bridge response")
			for _, bot := range e.bots.GetAllBots() {
				bot.SetServiceForTest(tc.service)
				bot.SetLLMForTest(fakeLLM)
			}

			// Service path resolves channel/team; agent path does not.
			e.mockAPI.On("GetChannel", testChannelID).Return(&model.Channel{
				Id:     testChannelID,
				Type:   model.ChannelTypeOpen,
				TeamId: "team-bridge",
			}, nil).Maybe()

			client := e.CreateBridgeClient()
			require.NoError(t, tc.call(client, request))

			lastRequest := fakeLLM.LastRequest()
			require.NotNil(t, lastRequest.Context)
			require.NotNil(t, lastRequest.Context.RequestingUser)
			require.Equal(t, testUserID, lastRequest.Context.RequestingUser.Id)
			require.Equal(t, tc.expectedOperation, lastRequest.Operation)
			require.Equal(t, tc.expectedSubType, lastRequest.OperationSubType)
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

func TestBridgeCompletionEndpointsRejectInvalidPrincipalIDs(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	invokers := []struct {
		name string
		call func(client *bridgeclient.Client, req bridgeclient.CompletionRequest) (string, error)
	}{
		{
			name: "agent non-streaming",
			call: func(client *bridgeclient.Client, req bridgeclient.CompletionRequest) (string, error) {
				return client.AgentCompletion(testBotUserID, req)
			},
		},
		{
			name: "agent streaming",
			call: func(client *bridgeclient.Client, req bridgeclient.CompletionRequest) (string, error) {
				result, err := client.AgentCompletionStream(testBotUserID, req)
				if err != nil {
					return "", err
				}
				return result.ReadAll()
			},
		},
		{
			name: "service non-streaming",
			call: func(client *bridgeclient.Client, req bridgeclient.CompletionRequest) (string, error) {
				return client.ServiceCompletion("service-id", req)
			},
		},
		{
			name: "service streaming",
			call: func(client *bridgeclient.Client, req bridgeclient.CompletionRequest) (string, error) {
				result, err := client.ServiceCompletionStream("service-id", req)
				if err != nil {
					return "", err
				}
				return result.ReadAll()
			},
		},
	}

	scenarios := []struct {
		name    string
		req     bridgeclient.CompletionRequest
		wantErr string
	}{
		{
			name: "invalid user ID",
			req: bridgeclient.CompletionRequest{
				Posts:  []bridgeclient.Post{{Role: "user", Message: "hello"}},
				UserID: "bad",
			},
			wantErr: "invalid user_id",
		},
		{
			name: "invalid channel ID",
			req: bridgeclient.CompletionRequest{
				Posts:     []bridgeclient.Post{{Role: "user", Message: "hello"}},
				ChannelID: "bad",
			},
			wantErr: "invalid channel_id",
		},
	}

	for _, invoker := range invokers {
		invoker := invoker
		for _, scenario := range scenarios {
			scenario := scenario
			t.Run(invoker.name+"/"+scenario.name, func(t *testing.T) {
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
				_, err := invoker.call(client, scenario.req)
				require.Error(t, err)
				require.Contains(t, err.Error(), scenario.wantErr)
			})
		}
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

// setupMCPWithEligibleTools creates an MCP test server with the given tools,
// configures the environment to use it, and sets up a context builder with
// matching tools. Returns the server (caller must defer Close).
func (e *TestEnvironment) setupMCPWithEligibleTools(t *testing.T, toolNames []string) *httptest.Server {
	t.Helper()

	server := setupBridgeEligibleMCPServer(t, toolNames)

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
	e.api.mcpClientManager = newTestMCPClientManager(t)

	tools := make([]llm.Tool, len(toolNames))
	for i, name := range toolNames {
		tools[i] = llm.Tool{
			Name:         name,
			ServerOrigin: server.URL,
			Description:  name,
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "ok", nil
			},
		}
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{tools: tools},
		nil,
		&testLLMContextConfigProvider{},
	)

	return server
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
	e.api.mcpClientManager = newTestMCPClientManager(t)

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:         "eligible_tool",
					ServerOrigin: server.URL,
					Description:  "eligible from context",
					Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
					Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
						return "ok", nil
					},
				},
				{
					Name:         "ineligible_tool",
					ServerOrigin: server.URL,
					Description:  "should be filtered out",
					Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
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
	tools, err := client.GetAgentTools(testBotUserID, testUserID)
	require.NoError(t, err)
	require.Len(t, tools, 2)
	require.Equal(t, "eligible_tool", tools[0].Name)
	require.Equal(t, "eligible from context", tools[0].Description)
	require.Equal(t, "ineligible_tool", tools[1].Name)
}

func TestBridgeGetAgentToolsReturnsEmbeddedServerTools(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	embeddedServer := newMockEmbeddedMCPServer([]string{"embedded_tool"})

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		EmbeddedServer: mcp.EmbeddedServerConfig{
			Enabled: true,
		},
	}
	mcpManager := newTestMCPClientManager(t)
	mcpManager.embeddedServer = embeddedServer
	e.api.mcpClientManager = mcpManager

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:         "embedded_tool",
					ServerOrigin: mcp.EmbeddedClientKey,
					Description:  "tool from embedded server",
					Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
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
	tools, err := client.GetAgentTools(testBotUserID, testUserID)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "embedded_tool", tools[0].Name)
	require.Equal(t, "tool from embedded server", tools[0].Description)
	require.Equal(t, mcp.EmbeddedClientKey, tools[0].ServerOrigin)
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
	e.api.mcpClientManager = newTestMCPClientManager(t)

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{
				{
					Name:         "eligible_tool",
					ServerOrigin: server.URL,
					Description:  "eligible from context",
					Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
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
	tools, err := client.GetAgentTools(testBotUserID, testUserID)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "eligible_tool", tools[0].Name)
}

func TestBridgeGetAgentToolsReturnsSortedToolsForAllowedUser(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := e.setupMCPWithEligibleTools(t, []string{"z_tool", "a_tool"})
	defer server.Close()

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

// fakeLLMAutoRunSequence builds a two-call StreamEventSequence for FakeLLM:
// the first call emits a single tool_use for the named tool (with empty
// ServerOrigin, matching what real LLM providers produce), and the second
// call emits the given final text. Together with toolrunner this exercises
// the auto-execute / re-call loop end-to-end.
func fakeLLMAutoRunSequence(toolCallID, toolName, finalText string) [][]llm.TextStreamEvent {
	return [][]llm.TextStreamEvent{
		{
			{
				Type: llm.EventTypeToolCalls,
				Value: []llm.ToolCall{
					{
						ID:        toolCallID,
						Name:      toolName,
						Arguments: json.RawMessage(`{}`),
					},
				},
			},
			{Type: llm.EventTypeEnd},
		},
		{
			{Type: llm.EventTypeText, Value: finalText},
			{Type: llm.EventTypeEnd},
		},
	}
}

// findAutoApprovedToolUse scans request.Posts for a bot turn whose ToolUse
// includes a call to the named tool with the AutoApproved status. Returns the
// number of matching tool uses across all posts (used to assert dedup).
func findAutoApprovedToolUse(req llm.CompletionRequest, toolName string) int {
	var count int
	for _, post := range req.Posts {
		for _, tc := range post.ToolUse {
			if tc.Name == toolName && tc.Status == llm.ToolCallStatusAutoApproved {
				count++
			}
		}
	}
	return count
}

func TestBridgeClientAgentCompletionAllowedToolsEnablesAutoRun(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := e.setupMCPWithEligibleTools(t, []string{"eligible_tool"})
	defer server.Close()

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLM("auto run enabled")
	fakeLLM.StreamEventSequence = fakeLLMAutoRunSequence("tc1", "eligible_tool", "auto run enabled")
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()
	result, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Use the tool"},
		},
		AllowedTools: []string{"eligible_tool"},
		UserID:       testUserID,
	})
	require.NoError(t, err)
	require.Equal(t, "auto run enabled", result)
	require.False(t, fakeLLM.LastConfig.ToolsDisabled)

	// The runner must have looped: one call to receive the tool use, a second
	// call after executing it to produce the final text response.
	require.Len(t, fakeLLM.AllRequests, 2)

	// The second call must include the executed tool result attached to a bot
	// post, with the resolved status set to AutoApproved.
	require.Equal(t, 1, findAutoApprovedToolUse(fakeLLM.AllRequests[1], "eligible_tool"))

	require.NotNil(t, fakeLLM.LastConversation.Context)
	require.NotNil(t, fakeLLM.LastConversation.Context.Tools)
	require.Len(t, fakeLLM.LastConversation.Context.Tools.GetTools(), 1)
}

func TestPrepareAgentBridgeCompletionAllowedToolsRequiresUserID(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	_, _, _, _, _, statusCode, err := e.api.prepareAgentBridgeCompletion(
		testBotUserID,
		bridgeclient.CompletionRequest{
			Posts: []bridgeclient.Post{
				{Role: "user", Message: "Hi"},
			},
			AllowedTools: []string{"eligible_tool"},
		},
		"",
		llm.OperationBridgeAgent,
		llm.SubTypeNoStream,
	)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, statusCode)
	require.Contains(t, err.Error(), "allowed_tools requires user_id")
}

func TestPrepareAgentBridgeCompletionToolHooksRequiresPluginID(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := e.setupMCPWithEligibleTools(t, []string{"eligible_tool"})
	defer server.Close()

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	_, _, _, _, _, statusCode, err := e.api.prepareAgentBridgeCompletion(
		testBotUserID,
		bridgeclient.CompletionRequest{
			Posts: []bridgeclient.Post{
				{Role: "user", Message: "Hi"},
			},
			AllowedTools: []string{"eligible_tool"},
			UserID:       testUserID,
			ToolHooks: map[string]bridgeclient.ToolHookConfig{
				"eligible_tool": {BeforeCallback: "/hooks/before"},
			},
		},
		"",
		llm.OperationBridgeAgent,
		llm.SubTypeNoStream,
	)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, statusCode)
	require.Contains(t, err.Error(), "tool_hooks requires Mattermost-Plugin-ID header")
}

func TestPrepareAgentBridgeCompletionStoresToolHookKeysInMCPMetadata(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := e.setupMCPWithEligibleTools(t, []string{"eligible_tool"})
	defer server.Close()

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	var storedKey string
	var storedEntry mcp.BeforeHookEntry
	e.mockAPI.On(
		"KVSetWithOptions",
		mock.MatchedBy(func(key string) bool {
			storedKey = key
			return strings.HasPrefix(key, "beforeHook:")
		}),
		mock.MatchedBy(func(data []byte) bool {
			if err := json.Unmarshal(data, &storedEntry); err != nil {
				return false
			}
			return storedEntry.UserID == testUserID &&
				storedEntry.ToolName == "eligible_tool" &&
				storedEntry.CallbackURL == "/plugins/com.example.caller/hooks/before"
		}),
		mock.MatchedBy(func(opts model.PluginKVSetOptions) bool {
			return opts.ExpireInSeconds == int64(mcp.BeforeHookKeyTTL.Seconds())
		}),
	).Return(true, (*model.AppError)(nil)).Once()

	_, llmRequest, _, _, beforeHookKeys, statusCode, err := e.api.prepareAgentBridgeCompletion(
		testBotUserID,
		bridgeclient.CompletionRequest{
			Posts: []bridgeclient.Post{
				{Role: "user", Message: "Hi"},
			},
			AllowedTools: []string{"eligible_tool"},
			UserID:       testUserID,
			ToolHooks: map[string]bridgeclient.ToolHookConfig{
				"eligible_tool": {BeforeCallback: "/hooks/before"},
			},
		},
		" com.example.caller ",
		llm.OperationBridgeAgent,
		llm.SubTypeNoStream,
	)
	require.NoError(t, err)
	require.Equal(t, 0, statusCode)
	require.NotNil(t, llmRequest.Context)
	require.Equal(t, []string{storedKey}, beforeHookKeys)

	require.NotNil(t, llmRequest.Context.Tools)
	scopedTool := llmRequest.Context.Tools.GetTool("eligible_tool")
	require.NotNil(t, scopedTool)
	require.NotNil(t, scopedTool.CallMetadata)
	require.NotContains(t, scopedTool.CallMetadata, "hook_plugin_id")
	hooks, ok := scopedTool.CallMetadata["tool_hooks"].(map[string]any)
	require.True(t, ok)
	eligible, ok := hooks["eligible_tool"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, storedKey, eligible["before_hook_key"])
	require.NotContains(t, eligible, "before_callback")
	require.Equal(t, testUserID, storedEntry.UserID)
	require.Equal(t, "eligible_tool", storedEntry.ToolName)
}

func TestCleanupBeforeHookKeysDeletesIssuedKeys(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.mockAPI.On("KVSetWithOptions", "beforeHook:key-1", []byte(nil), model.PluginKVSetOptions{}).Return(true, (*model.AppError)(nil)).Once()
	e.mockAPI.On("KVSetWithOptions", "beforeHook:key-2", []byte(nil), model.PluginKVSetOptions{}).Return(true, (*model.AppError)(nil)).Once()

	e.api.cleanupBeforeHookKeys([]string{"beforeHook:key-1", "beforeHook:key-2"})
}

func TestPrepareAgentBridgeCompletionToolHooksRequiresUserID(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := e.setupMCPWithEligibleTools(t, []string{"eligible_tool"})
	defer server.Close()

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	_, _, _, _, _, statusCode, err := e.api.prepareAgentBridgeCompletion(
		testBotUserID,
		bridgeclient.CompletionRequest{
			Posts: []bridgeclient.Post{
				{Role: "user", Message: "Hi"},
			},
			AllowedTools: []string{"eligible_tool"},
			ToolHooks: map[string]bridgeclient.ToolHookConfig{
				"eligible_tool": {BeforeCallback: "/hooks/before"},
			},
		},
		"com.example.caller",
		llm.OperationBridgeAgent,
		llm.SubTypeNoStream,
	)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, statusCode)
	require.Contains(t, err.Error(), "tool_hooks requires user_id")
}

func TestPrepareAgentBridgeCompletionToolHooksRequiresAllowedTools(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := e.setupMCPWithEligibleTools(t, []string{"eligible_tool"})
	defer server.Close()

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	_, _, _, _, _, statusCode, err := e.api.prepareAgentBridgeCompletion(
		testBotUserID,
		bridgeclient.CompletionRequest{
			Posts: []bridgeclient.Post{
				{Role: "user", Message: "Hi"},
			},
			UserID: testUserID,
			ToolHooks: map[string]bridgeclient.ToolHookConfig{
				"eligible_tool": {BeforeCallback: "/hooks/before"},
			},
		},
		"com.example.caller",
		llm.OperationBridgeAgent,
		llm.SubTypeNoStream,
	)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, statusCode)
	require.Contains(t, err.Error(), "tool_hooks requires allowed_tools")
}

func TestBridgeClientAgentCompletionAllowedToolsDeduplicatesList(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := e.setupMCPWithEligibleTools(t, []string{"eligible_tool"})
	defer server.Close()

	botConfig := llm.BotConfig{
		Name:            "testbot",
		DisplayName:     "Test Bot",
		UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.setupTestBot(botConfig)

	fakeLLM := NewFakeLLM("deduped")
	fakeLLM.StreamEventSequence = fakeLLMAutoRunSequence("tc1", "eligible_tool", "deduped")
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	client := e.CreateBridgeClient()
	result, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Run tool once"},
		},
		AllowedTools: []string{"eligible_tool", "eligible_tool"},
		UserID:       testUserID,
	})
	require.NoError(t, err)
	require.Equal(t, "deduped", result)

	// Despite being listed twice in AllowedTools, eligible_tool must be
	// scoped and executed exactly once.
	require.NotNil(t, fakeLLM.LastConversation.Context.Tools)
	require.Len(t, fakeLLM.LastConversation.Context.Tools.GetTools(), 1)
	require.Equal(t, 1, findAutoApprovedToolUse(fakeLLM.AllRequests[1], "eligible_tool"))
}

func TestBridgeClientAgentCompletionRejectsIneligibleAllowedTool(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := e.setupMCPWithEligibleTools(t, []string{"eligible_tool"})
	defer server.Close()

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
		UserID:       testUserID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not eligible or not available for this agent")
}

func TestBridgeClientAgentCompletionRejectsBuiltinToolInAllowedTools(t *testing.T) {
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
	e.api.mcpClientManager = newTestMCPClientManager(t)

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{tools: []llm.Tool{
			{
				Name:         "eligible_tool",
				ServerOrigin: server.URL,
				Description:  "eligible_tool",
				Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
				Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
					return "ok", nil
				},
			},
			{
				Name:        "builtin_only",
				Description: "built-in tool with no MCP origin",
				Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
				Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
					return "ok", nil
				},
			},
		}},
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
			{Role: "user", Message: "Hello"},
		},
		AllowedTools: []string{"builtin_only"},
		UserID:       testUserID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "built-in tools cannot be allowlisted")
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
		UserID:       testUserID,
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

	// No tool provider means the ToolStore will be empty.
	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&testLLMContextToolProvider{
			tools: []llm.Tool{},
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
		AllowedTools: []string{"nonexistent_tool"},
		UserID:       testUserID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no eligible tools available for this agent")
}
