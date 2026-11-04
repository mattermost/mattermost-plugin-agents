// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandleSaveConfigAgentReferences(t *testing.T) {
	const agentA = "agentabotuserid12345678901"
	const agentB = "agentbbotuserid12345678901"

	type refs struct {
		defaultBotUserID    string
		defaultBotName      string
		transcriptBotUserID string
		transcriptGenerator string
	}
	toConfig := func(r refs) config.Config {
		return config.Config{
			DefaultBotUserID:    r.defaultBotUserID,
			DefaultBotName:      r.defaultBotName,
			TranscriptBotUserID: r.transcriptBotUserID,
			TranscriptGenerator: r.transcriptGenerator,
		}
	}

	// The registry holds "alpha" (agentA) and "bravo" (agentB); no agent
	// account holds "a", "b", or "gone".
	tests := []struct {
		name           string
		stored         *refs
		request        refs
		expectedStatus int
		expected       refs
		expectWarned   []string
	}{
		{
			name:           "bot user IDs are saved",
			request:        refs{defaultBotUserID: agentA, transcriptBotUserID: agentB},
			expectedStatus: http.StatusOK,
			expected:       refs{defaultBotUserID: agentA, transcriptBotUserID: agentB},
		},
		{
			name:           "legacy names held by agents are saved as their bot user IDs",
			request:        refs{defaultBotName: "alpha", transcriptGenerator: "bravo"},
			expectedStatus: http.StatusOK,
			expected:       refs{defaultBotUserID: agentA, transcriptBotUserID: agentB},
		},
		{
			name:           "legacy names no agent holds are kept",
			request:        refs{defaultBotName: "a", transcriptGenerator: "b"},
			expectedStatus: http.StatusOK,
			expected:       refs{defaultBotName: "a", transcriptGenerator: "b"},
			expectWarned:   []string{"a", "b"},
		},
		{
			name:           "only the resolvable legacy name becomes an ID",
			request:        refs{defaultBotName: "alpha", transcriptGenerator: "gone"},
			expectedStatus: http.StatusOK,
			expected:       refs{defaultBotUserID: agentA, transcriptGenerator: "gone"},
			expectWarned:   []string{"gone"},
		},
		{
			name:           "a set bot user ID is kept over a legacy name for another agent",
			request:        refs{defaultBotUserID: agentA, defaultBotName: "bravo", transcriptBotUserID: agentB, transcriptGenerator: "alpha"},
			expectedStatus: http.StatusOK,
			expected:       refs{defaultBotUserID: agentA, transcriptBotUserID: agentB},
		},
		{
			name:           "a bot user ID drops the redundant legacy name",
			stored:         &refs{defaultBotName: "a", transcriptGenerator: "b"},
			request:        refs{defaultBotUserID: agentA, defaultBotName: "a", transcriptBotUserID: agentB, transcriptGenerator: "b"},
			expectedStatus: http.StatusOK,
			expected:       refs{defaultBotUserID: agentA, transcriptBotUserID: agentB},
		},
		{
			name:           "a legacy name edited by a client that round-trips the ID selects by that name",
			stored:         &refs{defaultBotUserID: agentA, transcriptBotUserID: agentA},
			request:        refs{defaultBotUserID: agentA, defaultBotName: "bravo", transcriptBotUserID: agentA, transcriptGenerator: "bravo"},
			expectedStatus: http.StatusOK,
			expected:       refs{defaultBotUserID: agentB, transcriptBotUserID: agentB},
		},
		{
			name:           "an edited legacy name no agent holds is kept",
			stored:         &refs{defaultBotUserID: agentA},
			request:        refs{defaultBotUserID: agentA, defaultBotName: "b"},
			expectedStatus: http.StatusOK,
			expected:       refs{defaultBotName: "b"},
			expectWarned:   []string{"b"},
		},
		{
			name:           "an unchanged round trip keeps the IDs",
			stored:         &refs{defaultBotUserID: agentA, transcriptBotUserID: agentB},
			request:        refs{defaultBotUserID: agentA, transcriptBotUserID: agentB},
			expectedStatus: http.StatusOK,
			expected:       refs{defaultBotUserID: agentA, transcriptBotUserID: agentB},
		},
		{
			name:           "clearing both fields clears the setting",
			stored:         &refs{defaultBotUserID: agentA, transcriptGenerator: "b"},
			request:        refs{},
			expectedStatus: http.StatusOK,
			expected:       refs{},
		},
		{
			name:           "a default agent reference that is not a bot user ID is rejected",
			stored:         &refs{defaultBotUserID: agentA},
			request:        refs{defaultBotUserID: "a"},
			expectedStatus: http.StatusBadRequest,
			expected:       refs{defaultBotUserID: agentA},
		},
		{
			name:           "a transcript agent reference that is not a bot user ID is rejected",
			stored:         &refs{transcriptBotUserID: agentB},
			request:        refs{transcriptBotUserID: "b"},
			expectedStatus: http.StatusBadRequest,
			expected:       refs{transcriptBotUserID: agentB},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)
			client := pluginapi.NewClient(mockAPI, nil)
			var warned []string
			mockAPI.On("LogWarn", mock.Anything, "username", mock.AnythingOfType("string")).Run(func(args mock.Arguments) {
				warned = append(warned, args.String(2))
			}).Maybe()
			allowAnyPluginAPILogging(mockAPI)
			mockAPI.On("GetUser", agentA).Return(&model.User{Id: agentA, Username: "alpha", IsBot: true}, nil).Maybe()
			mockAPI.On("GetUser", agentB).Return(&model.User{Id: agentB, Username: "bravo", IsBot: true}, nil).Maybe()
			mockAPI.On("GetUserByUsername", mock.AnythingOfType("string")).
				Return(nil, model.NewAppError("GetUserByUsername", "app.user.missing_account.const", nil, "", http.StatusNotFound)).Maybe()
			registry := createTestBots(mockAPI, client)
			registry.SetBotsForTesting([]*bots.Bot{
				bots.NewBot(llm.BotConfig{Name: "alpha"}, llm.ServiceConfig{}, &model.Bot{UserId: agentA, Username: "alpha"}, nil),
				bots.NewBot(llm.BotConfig{Name: "bravo"}, llm.ServiceConfig{}, &model.Bot{UserId: agentB, Username: "bravo"}, nil),
			})

			store := &testConfigStore{}
			if tc.stored != nil {
				stored := toConfig(*tc.stored)
				store.cfg = &stored
			}
			router := setupConfigTestRouter(&API{
				configStore:     store,
				configUpdater:   &testConfigUpdater{},
				clusterNotifier: &testClusterNotifier{},
				licenseChecker:  enterprisetest.CheckerAt(enterprise.LevelEnterpriseAdvanced),
				bots:            registry,
				pluginAPI:       client,
			})

			body, err := json.Marshal(toConfig(tc.request))
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, tc.expectedStatus, w.Code, w.Body.String())
			var saved config.Config
			if store.cfg != nil {
				saved = *store.cfg
			}
			assert.Equal(t, tc.expected, refs{
				defaultBotUserID:    saved.DefaultBotUserID,
				defaultBotName:      saved.DefaultBotName,
				transcriptBotUserID: saved.TranscriptBotUserID,
				transcriptGenerator: saved.TranscriptGenerator,
			})
			assert.ElementsMatch(t, tc.expectWarned, warned, "unresolvable legacy names are logged")
		})
	}
}
