// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestHandleGetAIBotsDefaultBotID verifies that /ai_bots returns the
// configured default bot's user ID as defaultBotID and places it first in
// the list (for backward compatibility with clients that infer the default
// from list order).
func TestHandleGetAIBotsDefaultBotID(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name                 string
		defaultBotName       string
		bots                 []*bots.Bot
		expectedDefaultBotID string
		expectedFirstBotID   string
		expectedBotCount     int
	}{
		{
			name:           "default bot is exposed explicitly and moved to front",
			defaultBotName: "ai",
			bots: []*bots.Bot{
				bots.NewBot(
					llm.BotConfig{Name: "aira", DisplayName: "Aira"},
					llm.ServiceConfig{},
					&model.Bot{UserId: "airabotuserid1234567890", Username: "aira", DisplayName: "Aira"},
					nil,
				),
				bots.NewBot(
					llm.BotConfig{Name: "ai", DisplayName: "Matty"},
					llm.ServiceConfig{},
					&model.Bot{UserId: "mattybotuserid1234567890", Username: "ai", DisplayName: "Matty"},
					nil,
				),
				bots.NewBot(
					llm.BotConfig{Name: "zorro", DisplayName: "Zorro"},
					llm.ServiceConfig{},
					&model.Bot{UserId: "zorrobotuserid1234567890", Username: "zorro", DisplayName: "Zorro"},
					nil,
				),
			},
			expectedDefaultBotID: "mattybotuserid1234567890",
			expectedFirstBotID:   "mattybotuserid1234567890",
			expectedBotCount:     3,
		},
		{
			name:           "no default configured returns empty defaultBotID",
			defaultBotName: "",
			bots: []*bots.Bot{
				bots.NewBot(
					llm.BotConfig{Name: "aira", DisplayName: "Aira"},
					llm.ServiceConfig{},
					&model.Bot{UserId: "airabotuserid1234567890", Username: "aira", DisplayName: "Aira"},
					nil,
				),
				bots.NewBot(
					llm.BotConfig{Name: "zorro", DisplayName: "Zorro"},
					llm.ServiceConfig{},
					&model.Bot{UserId: "zorrobotuserid1234567890", Username: "zorro", DisplayName: "Zorro"},
					nil,
				),
			},
			expectedDefaultBotID: "",
			expectedFirstBotID:   "airabotuserid1234567890",
			expectedBotCount:     2,
		},
		{
			name:           "configured default that doesn't match any bot returns empty defaultBotID",
			defaultBotName: "ghost",
			bots: []*bots.Bot{
				bots.NewBot(
					llm.BotConfig{Name: "aira", DisplayName: "Aira"},
					llm.ServiceConfig{},
					&model.Bot{UserId: "airabotuserid1234567890", Username: "aira", DisplayName: "Aira"},
					nil,
				),
			},
			expectedDefaultBotID: "",
			expectedFirstBotID:   "airabotuserid1234567890",
			expectedBotCount:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			name := tt.defaultBotName
			e.config.defaultBotName = &name
			e.bots.SetBotsForTesting(tt.bots)

			e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})

			request := httptest.NewRequest(http.MethodGet, "/ai_bots", nil)
			request.Header.Add("Mattermost-User-ID", "userid")
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			resp := recorder.Result()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var response AIBotsResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
			require.Len(t, response.Bots, tt.expectedBotCount)
			require.Equal(t, tt.expectedDefaultBotID, response.DefaultBotID,
				"defaultBotID should match the configured default's user ID")
			require.Equal(t, tt.expectedFirstBotID, response.Bots[0].ID,
				"first bot in list should match expected ordering")

			if response.DefaultBotID != "" {
				require.Equal(t, response.DefaultBotID, response.Bots[0].ID,
					"defaultBotID must match Bots[0].ID for backward compatibility")
			}
		})
	}
}
