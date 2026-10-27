// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// enableScopedSessions gives e the context builder of a node whose server
// passed the scoped session probe at activation.
func enableScopedSessions(t *testing.T, e *TestEnvironment) {
	t.Helper()
	e.mockAPI.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
	manager := mcp.NewClientManager(mcp.Config{IdleTimeoutMinutes: 30}, e.client.Log, e.client, nil, nil, http.DefaultClient, nil, mcp.RemoteMCPAlwaysAllowed)
	t.Cleanup(manager.Close)
	manager.SetScopedSessions(&scopedBridgeMinter{})
	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(e.client, &testLLMContextToolProvider{}, manager, &testLLMContextConfigProvider{})
}

func TestAIBotsReportsScopedSessionSupport(t *testing.T) {
	tests := []struct {
		name      string
		supported bool
	}{
		{name: "server can create scoped sessions", supported: true},
		{name: "server cannot create scoped sessions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			if tt.supported {
				enableScopedSessions(t, e)
			}
			e.setBots(bots.NewBot(llm.BotConfig{Name: "ai", DisplayName: "Agent"}, llm.ServiceConfig{}, &model.Bot{UserId: model.NewId(), Username: "ai"}, nil))
			e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{}).Maybe()

			request := httptest.NewRequest(http.MethodGet, "/ai_bots", nil)
			request.Header.Add("Mattermost-User-ID", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, tt.supported, response["scopedSessionsSupported"])
		})
	}
}

func TestAgentScopedChannelModeSave(t *testing.T) {
	publicID, privateID, dmID, archivedID, missingID := model.NewId(), model.NewId(), model.NewId(), model.NewId(), model.NewId()
	allowlist := []llm.EnabledMCPTool{{ServerOrigin: "https://jira.example.com", ToolName: "get_issue"}}
	wildcard := []llm.EnabledMCPTool{{ServerOrigin: "https://jira.example.com", ToolName: llm.MCPServerToolWildcard}}

	tests := []struct {
		name        string
		stored      llm.BotConfig
		overrides   map[string]any
		systemAdmin bool
		noScoped    bool
		wantStatus  int
		wantErr     string
		wantScoped  bool
		wantExtras  []string
	}{
		{
			name:        "admin turns it on with extra channels",
			stored:      llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist},
			overrides:   map[string]any{"scopedChannelMode": true, "scopedExtraChannelIDs": []string{publicID, privateID}},
			systemAdmin: true,
			wantStatus:  http.StatusOK,
			wantScoped:  true,
			wantExtras:  []string{publicID, privateID},
		},
		{
			name:       "agent managers cannot turn it on",
			stored:     llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist},
			overrides:  map[string]any{"scopedChannelMode": true},
			wantStatus: http.StatusForbidden,
			wantErr:    "system administrators",
		},
		{
			name:       "agent managers cannot change the extra channels",
			stored:     llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist, ScopedChannelMode: true, ScopedExtraChannelIDs: []string{publicID}},
			overrides:  map[string]any{"scopedExtraChannelIDs": []string{publicID, privateID}},
			wantStatus: http.StatusForbidden,
			wantErr:    "system administrators",
			wantScoped: true,
			wantExtras: []string{publicID},
		},
		{
			name:       "turning service accounts off clears both fields",
			stored:     llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist, ScopedChannelMode: true, ScopedExtraChannelIDs: []string{publicID}},
			overrides:  map[string]any{"useServiceAccountAuth": false},
			wantStatus: http.StatusOK,
		},
		{
			name:        "it cannot be staged while service accounts are off",
			stored:      llm.BotConfig{EnabledMCPTools: allowlist},
			overrides:   map[string]any{"scopedChannelMode": true, "scopedExtraChannelIDs": []string{publicID}},
			systemAdmin: true,
			wantStatus:  http.StatusOK,
		},
		{
			name:        "it needs an explicit allowlist",
			stored:      llm.BotConfig{UseServiceAccountAuth: true},
			overrides:   map[string]any{"scopedChannelMode": true, "autoEnableNewMCPTools": true},
			systemAdmin: true,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "explicit MCP tool allowlist",
		},
		{
			name:        "it needs every tool named",
			stored:      llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist},
			overrides:   map[string]any{"scopedChannelMode": true, "enabledMCPTools": wildcard},
			systemAdmin: true,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "every MCP tool to be named",
		},
		{
			name:        "an all-tools entry cannot be added to a scoped agent",
			stored:      llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist, ScopedChannelMode: true},
			overrides:   map[string]any{"enabledMCPTools": append(slices.Clone(allowlist), wildcard...)},
			systemAdmin: true,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "every MCP tool to be named",
			wantScoped:  true,
		},
		{
			name:        "a server that cannot create scoped sessions cannot turn it on",
			stored:      llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist},
			overrides:   map[string]any{"scopedChannelMode": true},
			systemAdmin: true,
			noScoped:    true,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "can create scoped sessions",
		},
		{
			name:        "a DM cannot be an extra channel",
			stored:      llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist},
			overrides:   map[string]any{"scopedChannelMode": true, "scopedExtraChannelIDs": []string{dmID}},
			systemAdmin: true,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "active public or private channel",
		},
		{
			name:        "an archived channel cannot be added",
			stored:      llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist},
			overrides:   map[string]any{"scopedChannelMode": true, "scopedExtraChannelIDs": []string{archivedID}},
			systemAdmin: true,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "active public or private channel",
		},
		{
			name:        "a missing channel cannot be added",
			stored:      llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist},
			overrides:   map[string]any{"scopedChannelMode": true, "scopedExtraChannelIDs": []string{missingID}},
			systemAdmin: true,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "could not be found",
		},
		{
			name:        "an already stored channel that was archived does not block saves",
			stored:      llm.BotConfig{UseServiceAccountAuth: true, EnabledMCPTools: allowlist, ScopedChannelMode: true, ScopedExtraChannelIDs: []string{archivedID}},
			overrides:   map[string]any{"customInstructions": "be brief"},
			systemAdmin: true,
			wantStatus:  http.StatusOK,
			wantScoped:  true,
			wantExtras:  []string{archivedID},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)

			mockLicensed(e.mockAPI)
			if !tt.noScoped {
				enableScopedSessions(t, e)
			}
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true).Maybe()
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(tt.systemAdmin).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
			e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
			e.mockAPI.On("GetChannel", publicID).Return(&model.Channel{Id: publicID, Type: model.ChannelTypeOpen}, nil).Maybe()
			e.mockAPI.On("GetChannel", privateID).Return(&model.Channel{Id: privateID, Type: model.ChannelTypePrivate}, nil).Maybe()
			e.mockAPI.On("GetChannel", dmID).Return(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}, nil).Maybe()
			e.mockAPI.On("GetChannel", archivedID).Return(&model.Channel{Id: archivedID, Type: model.ChannelTypeOpen, DeleteAt: 1}, nil).Maybe()
			e.mockAPI.On("GetChannel", missingID).Return(nil, model.NewAppError("GetChannel", "app.channel.missing", nil, "", http.StatusNotFound)).Maybe()

			stored := tt.stored
			stored.ID, stored.CreatorID, stored.BotUserID = "agent-1", testUserID, "bot-1"
			stored.DisplayName, stored.Name, stored.ServiceID = "Scoped", "scoped", "svc-1"
			stored.UserAccessLevel = llm.UserAccessLevelNone
			e.agentStore.agents["agent-1"] = &stored
			before := stored

			overrides := map[string]any{"scopedChannelMode": stored.ScopedChannelMode, "scopedExtraChannelIDs": stored.ScopedExtraChannelIDs}
			maps.Copy(overrides, tt.overrides)
			recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", updateAgentBodyFromStored(&before, overrides), testUserID)
			require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)
			if tt.wantErr != "" {
				assert.Contains(t, decodeAgentError(t, recorder), tt.wantErr)
			}

			saved := e.agentStore.agents["agent-1"]
			assert.Equal(t, tt.wantScoped, saved.ScopedChannelMode)
			assert.Equal(t, tt.wantExtras, saved.ScopedExtraChannelIDs)
		})
	}
}

func TestScopedExtraChannelsHiddenFromNonManagers(t *testing.T) {
	extraID := model.NewId()

	tests := []struct {
		name       string
		path       string
		creatorID  string
		wantExtras []string
	}{
		{name: "list, non-manager", path: "/agents", creatorID: "other-user", wantExtras: []string{}},
		{name: "get, non-manager", path: "/agents/agent-1", creatorID: "other-user", wantExtras: []string{}},
		{name: "list, creator", path: "/agents", creatorID: testUserID, wantExtras: []string{extraID}},
		{name: "get, creator", path: "/agents/agent-1", creatorID: testUserID, wantExtras: []string{extraID}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)

			mockLicensed(e.mockAPI)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false).Maybe()
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			e.agentStore.agents["agent-1"] = &llm.BotConfig{
				ID: "agent-1", CreatorID: tt.creatorID, DisplayName: "Scoped Agent",
				ServiceID: "svc-1", UserAccessLevel: llm.UserAccessLevelAll,
				UseServiceAccountAuth: true, ScopedChannelMode: true, ScopedExtraChannelIDs: []string{extraID},
			}

			recorder := doRequest(e.api, http.MethodGet, tt.path, nil, testUserID)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())

			var agent llm.BotConfig
			if tt.path == "/agents" {
				var agents []llm.BotConfig
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &agents))
				require.Len(t, agents, 1)
				agent = agents[0]
			} else {
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &agent))
			}
			assert.Equal(t, tt.wantExtras, agent.ScopedExtraChannelIDs)
			assert.True(t, agent.ScopedChannelMode)
		})
	}
}

func TestRestoreAgentVersionKeepsScopedChannelMode(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	channelID := model.NewId()
	mockLicensed(e.mockAPI)
	enableScopedSessions(t, e)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true).Maybe()
	e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil).Maybe()

	original := versionTestAgent()
	original.UseServiceAccountAuth = true
	original.EnabledMCPTools = []llm.EnabledMCPTool{{ServerOrigin: "https://jira.example.com", ToolName: "get_issue"}}
	original.ScopedChannelMode = true
	original.ScopedExtraChannelIDs = []string{channelID}
	seedVersionedAgent(t, e, original, map[string]any{"useServiceAccountAuth": true, "enabledMCPTools": original.EnabledMCPTools})
	require.False(t, e.agentStore.agents[versionTestAgentID].ScopedChannelMode, "the edit turned scoped channel mode off")

	recorder := doRequest(e.api, http.MethodPost, "/agents/agent-1/versions/1/restore", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())

	restored := e.agentStore.agents[versionTestAgentID]
	assert.True(t, restored.ScopedChannelMode)
	assert.Equal(t, []string{channelID}, restored.ScopedExtraChannelIDs)
}
