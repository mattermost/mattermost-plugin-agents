// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestLLMBridgePermissions(t *testing.T) {
	// Disable gin debug output
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	endpoints := []struct {
		name string
		url  string
	}{
		{"agent_streaming", "/bridge/v1/completion/agent/testbot"},
		{"agent_nostream", "/bridge/v1/completion/agent/testbot/nostream"},
		{"service_streaming", "/bridge/v1/completion/service/testservice"},
		{"service_nostream", "/bridge/v1/completion/service/testservice/nostream"},
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			tests := []struct {
				name           string
				userID         string
				channelID      string
				botConfig      llm.BotConfig
				envSetup       func(e *TestEnvironment)
				expectedStatus int
			}{
				{
					name:           "no UserID or ChannelID - succeeds (backward compatibility)",
					userID:         "",
					channelID:      "",
					botConfig:      llm.BotConfig{},
					envSetup:       func(e *TestEnvironment) {},
					expectedStatus: http.StatusInternalServerError, // Will fail at LLM call but pass permission check
				},
				{
					name:      "UserID only with allowed user - succeeds",
					userID:    "user-123",
					channelID: "",
					botConfig: llm.BotConfig{
						UserAccessLevel: llm.UserAccessLevelAll,
					},
					envSetup:       func(e *TestEnvironment) {},
					expectedStatus: http.StatusInternalServerError, // Will fail at LLM call but pass permission check
				},
				{
					name:      "UserID only with blocked user - returns 403",
					userID:    "user-123",
					channelID: "",
					botConfig: llm.BotConfig{
						UserAccessLevel: llm.UserAccessLevelBlock,
						UserIDs:         []string{"user-123"},
					},
					envSetup:       func(e *TestEnvironment) {},
					expectedStatus: http.StatusForbidden,
				},
				{
					name:      "UserID + ChannelID with allowed user and channel - succeeds",
					userID:    "user-123",
					channelID: "channel-123",
					botConfig: llm.BotConfig{
						UserAccessLevel:    llm.UserAccessLevelAll,
						ChannelAccessLevel: llm.ChannelAccessLevelAll,
					},
					envSetup: func(e *TestEnvironment) {
						e.mockAPI.On("GetChannel", "channel-123").Return(&model.Channel{
							Id:     "channel-123",
							Type:   model.ChannelTypeOpen,
							TeamId: "team-123",
						}, nil).Once()
					},
					expectedStatus: http.StatusInternalServerError, // Will fail at LLM call but pass permission check
				},
				{
					name:      "UserID + ChannelID with blocked channel - returns 403",
					userID:    "user-123",
					channelID: "channel-123",
					botConfig: llm.BotConfig{
						UserAccessLevel:    llm.UserAccessLevelAll,
						ChannelAccessLevel: llm.ChannelAccessLevelBlock,
						ChannelIDs:         []string{"channel-123"},
					},
					envSetup: func(e *TestEnvironment) {
						e.mockAPI.On("GetChannel", "channel-123").Return(&model.Channel{
							Id:     "channel-123",
							Type:   model.ChannelTypeOpen,
							TeamId: "team-123",
						}, nil).Once()
					},
					expectedStatus: http.StatusForbidden,
				},
				{
					name:      "UserID + ChannelID with blocked user - returns 403",
					userID:    "user-123",
					channelID: "channel-123",
					botConfig: llm.BotConfig{
						UserAccessLevel: llm.UserAccessLevelBlock,
						UserIDs:         []string{"user-123"},
					},
					envSetup: func(e *TestEnvironment) {
						e.mockAPI.On("GetChannel", "channel-123").Return(&model.Channel{
							Id:     "channel-123",
							Type:   model.ChannelTypeOpen,
							TeamId: "team-123",
						}, nil).Once()
					},
					expectedStatus: http.StatusForbidden,
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

					// Set the service for testing (needed for service endpoints)
					serviceConfig := llm.ServiceConfig{
						ID:   "testservice",
						Name: "Test Service",
					}
					for _, bot := range e.bots.GetAllBots() {
						bot.SetServiceForTest(serviceConfig)
					}

					// Setup environment
					tc.envSetup(e)

					// Allow error logging
					e.mockAPI.On("LogError", mock.Anything).Maybe()

					// Create request body
					reqBody := bridgeclient.CompletionRequest{
						Posts: []bridgeclient.Post{
							{
								Role:    "user",
								Message: "test message",
							},
						},
						UserID:    tc.userID,
						ChannelID: tc.channelID,
					}
					bodyJSON, err := json.Marshal(reqBody)
					require.NoError(t, err)

					// Create request
					req := httptest.NewRequest(http.MethodPost, endpoint.url, bytes.NewReader(bodyJSON))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Mattermost-Plugin-ID", "test-plugin")

					// Execute request
					recorder := httptest.NewRecorder()
					e.api.ServeHTTP(&plugin.Context{}, recorder, req)
					resp := recorder.Result()

					// Assert status code
					require.Equal(t, tc.expectedStatus, resp.StatusCode)

					// If expecting 403, verify error message
					if tc.expectedStatus == http.StatusForbidden {
						body, err := io.ReadAll(resp.Body)
						require.NoError(t, err)
						var errResp bridgeclient.ErrorResponse
						err = json.Unmarshal(body, &errResp)
						require.NoError(t, err)
						require.Contains(t, errResp.Error, "permission denied")
					}
				})
			}
		})
	}
}
