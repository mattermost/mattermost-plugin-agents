// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func setupAgentTestEnvironment(t *testing.T) *TestEnvironment {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)

	// Wire up a real license checker so license checks can be mocked
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)

	// Seed a config store with one service so service validation passes
	e.api.configStore = &mockConfigStore{
		cfg: &config.Config{
			Services: []llm.ServiceConfig{
				{ID: "svc-1", Name: "Test Service", Type: "openai"},
			},
		},
	}

	return e
}

// mockConfigStore is a minimal ConfigStore for agent tests.
type mockConfigStore struct {
	cfg    *config.Config
	getErr error
}

func (m *mockConfigStore) GetConfig() (*config.Config, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.cfg, nil
}

func (m *mockConfigStore) SaveConfig(cfg config.Config) error {
	return nil
}

// mockLicensed sets up mock expectations so IsMultiLLMLicensed() returns true.
func mockLicensed(mockAPI *plugintest.API) {
	mockAPI.On("GetConfig").Return(&model.Config{
		ServiceSettings: model.ServiceSettings{
			SiteURL: model.NewPointer("http://localhost"),
		},
	}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{
		Features: &model.Features{
			LDAP: model.NewPointer(true),
		},
		SkuShortName: "enterprise",
	}).Maybe()
}

// mockUnlicensed sets up mock expectations so IsMultiLLMLicensed() returns false.
func mockUnlicensed(mockAPI *plugintest.API) {
	mockAPI.On("GetConfig").Return(&model.Config{
		ServiceSettings: model.ServiceSettings{
			SiteURL: model.NewPointer("http://localhost"),
		},
	}).Maybe()
	mockAPI.On("GetLicense").Return((*model.License)(nil)).Maybe()
}

func doRequest(api *API, method, path string, body interface{}, userID string) *httptest.ResponseRecorder {
	var reqBody io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Mattermost-User-Id", userID)
	recorder := httptest.NewRecorder()
	api.ServeHTTP(&plugin.Context{}, recorder, req)
	return recorder
}

// createAgentBody returns a CreateAgentRequest populated via a JSON map.
// Callers may override any field via `overrides`. By default the agent auto-enables
// every MCP tool, which matches the UI default for new agents.
func createAgentBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"displayName":           "My Agent",
		"username":              "my-agent",
		"serviceID":             "svc-1",
		"autoEnableNewMCPTools": true,
		"mcpDynamicToolLoading": true,
	}
	for k, v := range overrides {
		body[k] = v
	}
	return body
}

// updateAgentBodyFromStored builds a full-object PUT body from a stored agent,
// applying the caller's overrides. All fields the backend cares about are
// included so the request satisfies the full-replacement contract.
func updateAgentBodyFromStored(cfg *llm.BotConfig, overrides map[string]any) map[string]any {
	body := map[string]any{
		"displayName":             cfg.DisplayName,
		"username":                cfg.Name,
		"serviceID":               cfg.ServiceID,
		"customInstructions":      cfg.CustomInstructions,
		"channelAccessLevel":      int(cfg.ChannelAccessLevel),
		"channelIDs":              cfg.ChannelIDs,
		"userAccessLevel":         int(cfg.UserAccessLevel),
		"userIDs":                 cfg.UserIDs,
		"teamIDs":                 cfg.TeamIDs,
		"adminUserIDs":            cfg.AdminUserIDs,
		"enabledMCPTools":         cfg.EnabledMCPTools,
		"autoEnableNewMCPTools":   cfg.AutoEnableNewMCPTools,
		"mcpDynamicToolLoading":   cfg.MCPDynamicToolLoading,
		"model":                   cfg.Model,
		"enableVision":            cfg.EnableVision,
		"disableTools":            cfg.DisableTools,
		"enabledNativeTools":      cfg.EnabledNativeTools,
		"reasoningEnabled":        cfg.ReasoningEnabled,
		"reasoningEffort":         cfg.ReasoningEffort,
		"thinkingBudget":          cfg.ThinkingBudget,
		"structuredOutputEnabled": cfg.StructuredOutputEnabled,
	}
	for k, v := range overrides {
		body[k] = v
	}
	return body
}

func TestCreateAgentWithPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
		UserId:      "bot-user-id-created",
		Username:    "my-agent",
		DisplayName: "My Agent",
		Description: "User-created AI agent",
	}, nil)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := createAgentBody(map[string]any{
		"reasoningEnabled":        true,
		"structuredOutputEnabled": false,
	})

	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	resp := recorder.Result()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agent))
	assert.Equal(t, "My Agent", agent.DisplayName)
	assert.Equal(t, "my-agent", agent.Name)
	assert.Equal(t, testUserID, agent.CreatorID)
	assert.NotEmpty(t, agent.ID)
	assert.True(t, agent.MCPDynamicToolLoading)
	assert.True(t, agent.ReasoningEnabled)
	assert.False(t, agent.StructuredOutputEnabled)
}

func TestCreateAgentPersistsExplicitRequestValues(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
		UserId:      "bot-user-id-created",
		Username:    "my-agent",
		DisplayName: "My Agent",
		Description: "User-created AI agent",
	}, nil)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// The backend no longer injects hidden defaults; whatever the client sends is
	// what gets persisted verbatim.
	body := createAgentBody(map[string]any{
		"enableVision":            false,
		"disableTools":            true,
		"enabledNativeTools":      []string{},
		"reasoningEnabled":        false,
		"reasoningEffort":         "high",
		"structuredOutputEnabled": false,
	})

	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agent))
	assert.False(t, agent.EnableVision)
	assert.True(t, agent.DisableTools)
	assert.False(t, agent.ReasoningEnabled)
	assert.Equal(t, "high", agent.ReasoningEffort)
	assert.False(t, agent.StructuredOutputEnabled)
	assert.Empty(t, agent.EnabledNativeTools)
}

func TestCreateAgentAutoEnableAllMCPTools(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
		UserId:      "bot-user-id-created",
		Username:    "my-agent",
		DisplayName: "My Agent",
		Description: "User-created AI agent",
	}, nil)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := createAgentBody(map[string]any{"autoEnableNewMCPTools": true})
	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agent))
	assert.True(t, agent.AutoEnableNewMCPTools)
}

func TestCreateAgentMCPDynamicToolLoading(t *testing.T) {
	tests := []struct {
		name     string
		body     map[string]any
		omit     bool
		expected bool
	}{
		{
			name:     "defaults false when omitted",
			omit:     true,
			expected: false,
		},
		{
			name:     "persists explicit false",
			body:     map[string]any{"mcpDynamicToolLoading": false},
			expected: false,
		},
		{
			name:     "persists explicit true",
			body:     map[string]any{"mcpDynamicToolLoading": true},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)

			mockLicensed(e.mockAPI)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
				UserId:      "bot-user-id-created",
				Username:    "my-agent",
				DisplayName: "My Agent",
				Description: "User-created AI agent",
			}, nil)
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			body := createAgentBody(tt.body)
			if tt.omit {
				delete(body, "mcpDynamicToolLoading")
			}
			recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
			require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)

			var agent llm.BotConfig
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agent))
			assert.Equal(t, tt.expected, agent.MCPDynamicToolLoading)
			assert.Equal(t, tt.expected, e.agentStore.agents[agent.ID].MCPDynamicToolLoading)
		})
	}
}

func TestCreateAgentNoMCPToolsByDefault(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
		UserId:      "bot-user-id-created",
		Username:    "my-agent",
		DisplayName: "My Agent",
		Description: "User-created AI agent",
	}, nil)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// autoEnableNewMCPTools=false + empty allowlist ⇒ no MCP tools for this agent.
	body := createAgentBody(map[string]any{
		"autoEnableNewMCPTools": false,
		"enabledMCPTools":       []any{},
	})
	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agent))
	assert.False(t, agent.AutoEnableNewMCPTools)
	assert.Empty(t, agent.EnabledMCPTools)
}

func TestCreateAgentEnabledMCPToolsAllowlist(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
		UserId:      "bot-user-id-created",
		Username:    "my-agent",
		DisplayName: "My Agent",
		Description: "User-created AI agent",
	}, nil)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := createAgentBody(map[string]any{
		"autoEnableNewMCPTools": false,
		"enabledMCPTools": []llm.EnabledMCPTool{
			{ServerOrigin: "embedded://mattermost", ToolName: "read_post"},
		},
	})

	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agent))
	assert.False(t, agent.AutoEnableNewMCPTools)
	require.Len(t, agent.EnabledMCPTools, 1)
	assert.Equal(t, "embedded://mattermost", agent.EnabledMCPTools[0].ServerOrigin)
	assert.Equal(t, "read_post", agent.EnabledMCPTools[0].ToolName)
}

func TestCreateAgentForbiddenWithoutManageOwnPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := createAgentBody(map[string]any{
		"displayName": "Sysadmin Agent",
		"username":    "sysadmin-agent",
	})

	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestCreateAgentWithoutPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(nil), testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestCreateAgentFreeTierAllowsFirstAgent(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockUnlicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
		UserId:      "bot-user-id-created",
		Username:    "my-agent",
		DisplayName: "My Agent",
		Description: "User-created AI agent",
	}, nil)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(nil), testUserID)
	require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)
}

func TestCreateAgentFreeTierBlocksWhenQuotaReached(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockUnlicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// One existing agent is already at the free-tier quota.
	e.agentStore.agents["existing"] = &llm.BotConfig{
		ID: "existing", CreatorID: "someone-else", Name: "existing", DisplayName: "Existing",
	}

	recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(nil), testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestListAgentsIncludesActiveCountHeaderWhenUnlicensed(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockUnlicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: "other-user", DisplayName: "Private Agent",
		UserAccessLevel: llm.UserAccessLevelNone,
	}

	recorder := doRequest(e.api, http.MethodGet, "/agents", nil, testUserID)
	resp := recorder.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "1", resp.Header.Get(AgentActiveCountHeader))

	var agents []*llm.BotConfig
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agents))
	assert.Empty(t, agents)
}

func TestListAgentsOmitsActiveCountHeaderWhenCountFails(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockUnlicensed(e.mockAPI)
	e.mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.countErr = errors.New("boom")

	recorder := doRequest(e.api, http.MethodGet, "/agents", nil, testUserID)
	resp := recorder.Result()

	// A count failure must not fail the list request; the header is simply omitted.
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get(AgentActiveCountHeader))
}

func TestListAgentsFiltersByAccess(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	// sanitizeAgentForUser → canManageAgent checks PermissionManageOthersAgent for each accessible agent.
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Seed agents: one accessible (UserAccessLevelAll, with sensitive customInstructions),
	// one blocked (UserAccessLevelNone)
	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: "other-user", DisplayName: "Public Agent",
		UserAccessLevel:    llm.UserAccessLevelAll,
		CustomInstructions: "internal procedures",
	}
	e.agentStore.agents["agent-2"] = &llm.BotConfig{
		ID: "agent-2", CreatorID: "other-user", DisplayName: "Private Agent",
		UserAccessLevel: llm.UserAccessLevelNone,
	}

	recorder := doRequest(e.api, http.MethodGet, "/agents", nil, testUserID)
	resp := recorder.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var agents []*llm.BotConfig
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agents))
	assert.Len(t, agents, 1)
	assert.Equal(t, "Public Agent", agents[0].DisplayName)
	// Non-managers must not see customInstructions.
	assert.Empty(t, agents[0].CustomInstructions)
}

func TestUpdateAgentAsCreator(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
		DisplayName: "Original", Name: "original", ServiceID: "svc-1",
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{"displayName": "Updated"})

	// Mock bot patch for display name sync
	e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Result().Body).Decode(&agent))
	assert.Equal(t, "Updated", agent.DisplayName)
}

func TestUpdateAgentAsAdminUser(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID: "agent-1", CreatorID: "other-user", BotUserID: "bot-1",
		DisplayName: "Original", Name: "original", ServiceID: "svc-1",
		AdminUserIDs: []string{testUserID},
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{"customInstructions": "Be brief"})

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
}

func TestUpdateAgentAsNonAdmin(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID: "agent-1", CreatorID: "other-user", BotUserID: "bot-1",
		DisplayName: "Not Mine", Name: "not-mine", ServiceID: "svc-1",
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{"displayName": "Hacked"})

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestUpdateAgentOwnedByOtherWithManageOthersPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID: "agent-1", CreatorID: "other-user", BotUserID: "bot-1",
		DisplayName: "Theirs", Name: "theirs", ServiceID: "svc-1",
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{"displayName": "Admin Renamed"})
	e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Result().Body).Decode(&agent))
	assert.Equal(t, "Admin Renamed", agent.DisplayName)
}

func TestDeleteAgentDeactivatesBot(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	// When EnsureBots succeeds, handleDeleteAgent skips explicit UpdateActive (EnsureBots reconciles).
	e.mockAPI.On("UpdateBotActive", "bot-1", false).Return(&model.Bot{}, nil).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Seed an agent owned by testUserID
	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
		DisplayName: "Doomed", Name: "doomed", ServiceID: "svc-1",
	}

	recorder := doRequest(e.api, http.MethodDelete, "/agents/agent-1", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	// Verify soft-deleted in store
	agent := e.agentStore.agents["agent-1"]
	assert.NotZero(t, agent.DeleteAt)
}

func TestListServicesNoSecrets(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	recorder := doRequest(e.api, http.MethodGet, "/services", nil, testUserID)
	resp := recorder.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var services []ServiceInfo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&services))
	require.Len(t, services, 1)
	assert.Equal(t, "svc-1", services[0].ID)
	assert.Equal(t, "Test Service", services[0].Name)
	assert.Equal(t, "openai", services[0].Type)
	assert.True(t, services[0].UseResponsesAPI)

	// Verify no secret fields leak through
	raw, _ := json.Marshal(services[0])
	assert.NotContains(t, string(raw), "apiKey")
	assert.NotContains(t, string(raw), "awsSecret")
}

func TestUpdateMigratedAgentWithManageOthersPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID: "agent-1", CreatorID: "", BotUserID: "bot-1",
		DisplayName: "Migrated", Name: "migrated", ServiceID: "svc-1",
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{"displayName": "Updated Migrated"})
	e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Result().Body).Decode(&agent))
	assert.Equal(t, "Updated Migrated", agent.DisplayName)
}

func TestUpdateMigratedAgentForbiddenWithoutManageOthersPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID: "agent-1", CreatorID: "", BotUserID: "bot-1",
		DisplayName: "Migrated", Name: "migrated", ServiceID: "svc-1",
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{"displayName": "Hacked"})

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestUpdateMigratedAgentWithManageSystemPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID: "agent-1", CreatorID: "", BotUserID: "bot-1",
		DisplayName: "Migrated", Name: "migrated", ServiceID: "svc-1",
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{"displayName": "Updated By System Admin"})
	e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	resp := recorder.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agent))
	assert.Equal(t, "Updated By System Admin", agent.DisplayName)
}

func TestFetchModelsForServiceMissingCredentials(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := map[string]string{"serviceID": "svc-1"}
	recorder := doRequest(e.api, http.MethodPost, "/agents/models/fetch", body, testUserID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

func TestFetchModelsForServiceUnknownService(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := map[string]string{"serviceID": "missing-svc"}
	recorder := doRequest(e.api, http.MethodPost, "/agents/models/fetch", body, testUserID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

// TestFetchModelsForServiceVertexMissingProject validates that the model fetch
// endpoint rejects a Vertex service config without the project/region fields
// required to address GCP, even when the allowlist now includes Vertex.
func TestFetchModelsForServiceVertexMissingProject(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	e.api.configStore = &mockConfigStore{
		cfg: &config.Config{
			Services: []llm.ServiceConfig{
				{ID: "vertex-svc", Type: llm.ServiceTypeVertex},
			},
		},
	}

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := map[string]string{"serviceID": "vertex-svc"}
	recorder := doRequest(e.api, http.MethodPost, "/agents/models/fetch", body, testUserID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

// TestFetchModelsForServiceGeminiMissingAPIKey validates that Gemini follows
// the standard API-key credential gate after being added to the allowlist.
func TestFetchModelsForServiceGeminiMissingAPIKey(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	e.api.configStore = &mockConfigStore{
		cfg: &config.Config{
			Services: []llm.ServiceConfig{
				{ID: "gemini-svc", Type: llm.ServiceTypeGemini},
			},
		},
	}

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := map[string]string{"serviceID": "gemini-svc"}
	recorder := doRequest(e.api, http.MethodPost, "/agents/models/fetch", body, testUserID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

func TestListServicesForbiddenWithoutManageOwnPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	recorder := doRequest(e.api, http.MethodGet, "/services", nil, testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestFetchModelsForbiddenWithoutManageOwnPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := map[string]string{"serviceID": "svc-1"}
	recorder := doRequest(e.api, http.MethodPost, "/agents/models/fetch", body, testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestListServicesWithManageOthersPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	recorder := doRequest(e.api, http.MethodGet, "/services", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
}

func TestFetchModelsForServiceWithManageOthersPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := map[string]string{"serviceID": "svc-1"}
	recorder := doRequest(e.api, http.MethodPost, "/agents/models/fetch", body, testUserID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

func TestUpdateAgentUsernameChangeForbidden(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
		DisplayName: "Agent", Name: "same-user", ServiceID: "svc-1",
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{"username": "other-user"})

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

func TestUpdateAgentInvalidServiceID(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
		DisplayName: "Agent", Name: "my-agent", ServiceID: "svc-1",
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{"serviceID": "not-a-configured-service"})

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

func TestUpdateAgentFlipsAutoEnableOff(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID:                    "agent-1",
		CreatorID:             testUserID,
		BotUserID:             "bot-1",
		DisplayName:           "Agent",
		Name:                  "my-agent",
		ServiceID:             "svc-1",
		AutoEnableNewMCPTools: true,
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{
		"autoEnableNewMCPTools": false,
		"enabledMCPTools": []llm.EnabledMCPTool{
			{ServerOrigin: "embedded://mattermost", ToolName: "read_post"},
		},
	})

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	updated := e.agentStore.agents["agent-1"]
	require.NotNil(t, updated)
	assert.False(t, updated.AutoEnableNewMCPTools)
	require.Len(t, updated.EnabledMCPTools, 1)
	assert.Equal(t, "read_post", updated.EnabledMCPTools[0].ToolName)
}

func TestUpdateAgentMCPDynamicToolLoading(t *testing.T) {
	tests := []struct {
		name          string
		storedValue   bool
		body          map[string]any
		omit          bool
		expectedValue bool
	}{
		{
			name:          "persists explicit false",
			storedValue:   true,
			body:          map[string]any{"mcpDynamicToolLoading": false},
			expectedValue: false,
		},
		{
			name:          "overwrites with false when omitted from body",
			storedValue:   true,
			omit:          true,
			expectedValue: false,
		},
		{
			name:          "persists explicit true",
			storedValue:   false,
			body:          map[string]any{"mcpDynamicToolLoading": true},
			expectedValue: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)

			mockLicensed(e.mockAPI)
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			stored := &llm.BotConfig{
				ID:                    "agent-1",
				CreatorID:             testUserID,
				BotUserID:             "bot-1",
				DisplayName:           "Agent",
				Name:                  "my-agent",
				ServiceID:             "svc-1",
				MCPDynamicToolLoading: tt.storedValue,
			}
			e.agentStore.agents["agent-1"] = stored

			body := updateAgentBodyFromStored(stored, tt.body)
			if tt.omit {
				delete(body, "mcpDynamicToolLoading")
			}

			recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

			updated := e.agentStore.agents["agent-1"]
			require.NotNil(t, updated)
			assert.Equal(t, tt.expectedValue, updated.MCPDynamicToolLoading)

			var response llm.BotConfig
			require.NoError(t, json.NewDecoder(recorder.Result().Body).Decode(&response))
			assert.Equal(t, tt.expectedValue, response.MCPDynamicToolLoading)
		})
	}
}

func TestGetAgentMCPDynamicToolLoadingRoundTrip(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID:                    "agent-1",
		CreatorID:             testUserID,
		BotUserID:             "bot-1",
		DisplayName:           "Agent",
		Name:                  "my-agent",
		ServiceID:             "svc-1",
		MCPDynamicToolLoading: false,
	}

	recorder := doRequest(e.api, http.MethodGet, "/agents/agent-1", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Result().Body).Decode(&agent))
	assert.False(t, agent.MCPDynamicToolLoading)
}

func TestListAgentsMCPDynamicToolLoadingRoundTrip(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID:                    "agent-1",
		CreatorID:             "other-user",
		DisplayName:           "Dynamic On",
		UserAccessLevel:       llm.UserAccessLevelAll,
		MCPDynamicToolLoading: true,
	}
	e.agentStore.agents["agent-2"] = &llm.BotConfig{
		ID:                    "agent-2",
		CreatorID:             "other-user",
		DisplayName:           "Dynamic Off",
		UserAccessLevel:       llm.UserAccessLevelAll,
		MCPDynamicToolLoading: false,
	}

	recorder := doRequest(e.api, http.MethodGet, "/agents", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var agents []*llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Result().Body).Decode(&agents))
	require.Len(t, agents, 2)

	byID := map[string]*llm.BotConfig{}
	for _, agent := range agents {
		byID[agent.ID] = agent
	}
	assert.True(t, byID["agent-1"].MCPDynamicToolLoading)
	assert.False(t, byID["agent-2"].MCPDynamicToolLoading)
}

func TestUpdateAgentEmptyAllowlistAllowsNone(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID:          "agent-1",
		CreatorID:   testUserID,
		BotUserID:   "bot-1",
		DisplayName: "Agent",
		Name:        "my-agent",
		ServiceID:   "svc-1",
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: "embedded://mattermost", ToolName: "read_post"},
		},
	}
	e.agentStore.agents["agent-1"] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{
		"autoEnableNewMCPTools": false,
		"enabledMCPTools":       []llm.EnabledMCPTool{},
	})

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	updated := e.agentStore.agents["agent-1"]
	require.NotNil(t, updated)
	assert.False(t, updated.AutoEnableNewMCPTools)
	assert.Empty(t, updated.EnabledMCPTools)
}

func TestUpdateAgentFullReplacementOverwritesMutableFields(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Seed an agent that has several fields populated. The caller will send a
	// request that explicitly zeros many of them; the contract is full-object
	// replacement, so those zero values must persist.
	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID:                      "agent-1",
		CreatorID:               testUserID,
		BotUserID:               "bot-1",
		DisplayName:             "Original",
		Name:                    "my-agent",
		ServiceID:               "svc-1",
		CustomInstructions:      "keep me",
		EnabledNativeTools:      []string{"web_search"},
		ReasoningEnabled:        true,
		ReasoningEffort:         "high",
		ThinkingBudget:          4096,
		StructuredOutputEnabled: true,
	}

	body := map[string]any{
		"displayName":             "Replaced",
		"username":                "my-agent",
		"serviceID":               "svc-1",
		"customInstructions":      "",
		"channelAccessLevel":      0,
		"channelIDs":              []string{},
		"userAccessLevel":         0,
		"userIDs":                 []string{},
		"teamIDs":                 []string{},
		"adminUserIDs":            []string{},
		"enabledMCPTools":         []llm.EnabledMCPTool{},
		"autoEnableNewMCPTools":   false,
		"model":                   "",
		"enableVision":            false,
		"disableTools":            false,
		"enabledNativeTools":      []string{},
		"reasoningEnabled":        false,
		"reasoningEffort":         "",
		"thinkingBudget":          0,
		"structuredOutputEnabled": false,
	}

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	updated := e.agentStore.agents["agent-1"]
	require.NotNil(t, updated)
	assert.Equal(t, "Replaced", updated.DisplayName)
	assert.Empty(t, updated.CustomInstructions)
	assert.Empty(t, updated.EnabledNativeTools)
	assert.False(t, updated.ReasoningEnabled)
	assert.Empty(t, updated.ReasoningEffort)
	assert.Zero(t, updated.ThinkingBudget)
	assert.False(t, updated.StructuredOutputEnabled)
}

// Suppress unused import warnings for multipart (used for avatar test below)
var _ = multipart.NewWriter

// TestAgentSaveErrorsAreActionable confirms every failure path on the agent
// save endpoints writes a JSON {"error": ...} body. The webapp surfaces this
// message verbatim to the user, so an empty body or status-only response
// leaks the misleading generic "Failed to save agent. Please try again." hint.
func TestAgentSaveErrorsAreActionable(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(t *testing.T, e *TestEnvironment) (method, path string, body any)
		expectedStatus int
		errorContains  string
	}{
		{
			name: "create rejects oversized custom instructions",
			setup: func(_ *testing.T, e *TestEnvironment) (string, string, any) {
				mockLicensed(e.mockAPI)
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true).Maybe()
				e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

				oversized := make([]byte, llm.MaxCustomInstructionsRunes+1)
				for i := range oversized {
					oversized[i] = 'a'
				}
				body := createAgentBody(map[string]any{
					"customInstructions": string(oversized),
				})
				return http.MethodPost, "/agents", body
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "customInstructions exceeds maximum length",
		},
		{
			name: "create rejects invalid username",
			setup: func(_ *testing.T, e *TestEnvironment) (string, string, any) {
				mockLicensed(e.mockAPI)
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true).Maybe()
				e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

				body := createAgentBody(map[string]any{"username": "1nvalid"})
				return http.MethodPost, "/agents", body
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "invalid username",
		},
		{
			name: "create rejects unknown service id",
			setup: func(_ *testing.T, e *TestEnvironment) (string, string, any) {
				mockLicensed(e.mockAPI)
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true).Maybe()
				e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

				body := createAgentBody(map[string]any{"serviceID": "missing-service"})
				return http.MethodPost, "/agents", body
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "missing-service",
		},
		{
			name: "create returns reason when user lacks permission",
			setup: func(_ *testing.T, e *TestEnvironment) (string, string, any) {
				mockLicensed(e.mockAPI)
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(false)
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
				e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

				return http.MethodPost, "/agents", createAgentBody(nil)
			},
			expectedStatus: http.StatusForbidden,
			errorContains:  "does not have permission",
		},
		{
			name: "update rejects oversized custom instructions",
			setup: func(_ *testing.T, e *TestEnvironment) (string, string, any) {
				mockLicensed(e.mockAPI)
				e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

				stored := &llm.BotConfig{
					ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
					DisplayName: "Agent", Name: "my-agent", ServiceID: "svc-1",
				}
				e.agentStore.agents["agent-1"] = stored

				oversized := make([]byte, llm.MaxCustomInstructionsRunes+1)
				for i := range oversized {
					oversized[i] = 'a'
				}
				body := updateAgentBodyFromStored(stored, map[string]any{
					"customInstructions": string(oversized),
				})
				return http.MethodPut, "/agents/agent-1", body
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "customInstructions exceeds maximum length",
		},
		{
			name: "update rejects username change",
			setup: func(_ *testing.T, e *TestEnvironment) (string, string, any) {
				mockLicensed(e.mockAPI)
				e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

				stored := &llm.BotConfig{
					ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
					DisplayName: "Agent", Name: "original-user", ServiceID: "svc-1",
				}
				e.agentStore.agents["agent-1"] = stored

				body := updateAgentBodyFromStored(stored, map[string]any{"username": "different-user"})
				return http.MethodPut, "/agents/agent-1", body
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "username cannot be changed",
		},
		{
			name: "create sanitizes internal server error responses",
			setup: func(_ *testing.T, e *TestEnvironment) (string, string, any) {
				mockLicensed(e.mockAPI)
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true).Maybe()
				e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

				e.api.configStore = &mockConfigStore{getErr: errors.New("database connection secret-detail")}
				return http.MethodPost, "/agents", createAgentBody(nil)
			},
			expectedStatus: http.StatusInternalServerError,
			errorContains:  "internal server error",
		},
		{
			name: "update returns reason when caller cannot manage agent",
			setup: func(_ *testing.T, e *TestEnvironment) (string, string, any) {
				mockLicensed(e.mockAPI)
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false)
				e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

				stored := &llm.BotConfig{
					ID: "agent-1", CreatorID: "someone-else", BotUserID: "bot-1",
					DisplayName: "Agent", Name: "their-agent", ServiceID: "svc-1",
				}
				e.agentStore.agents["agent-1"] = stored

				body := updateAgentBodyFromStored(stored, map[string]any{"displayName": "Hijack"})
				return http.MethodPut, "/agents/agent-1", body
			},
			expectedStatus: http.StatusForbidden,
			errorContains:  "not authorized",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)

			method, path, body := tc.setup(t, e)
			recorder := doRequest(e.api, method, path, body, testUserID)
			resp := recorder.Result()

			require.Equal(t, tc.expectedStatus, resp.StatusCode)
			require.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"),
				"agent endpoints must return a JSON error body so the UI can show an actionable message")

			var payload agentErrorResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload),
				"error response body must be valid JSON")
			require.NotEmpty(t, payload.Error, "error message must not be empty")
			assert.Contains(t, payload.Error, tc.errorContains,
				"error message must surface the actionable cause")
		})
	}
}

func TestCreateAgentRequestJSONRoundTrip(t *testing.T) {
	req := CreateAgentRequest{
		DisplayName:           "My Agent",
		Username:              "my-agent",
		ServiceID:             "svc-1",
		CustomInstructions:    "Be brief",
		ChannelAccessLevel:    int(llm.ChannelAccessLevelAllow),
		ChannelIDs:            []string{"c1", "c2"},
		UserAccessLevel:       int(llm.UserAccessLevelBlock),
		UserIDs:               []string{"u1"},
		TeamIDs:               []string{"t1"},
		AdminUserIDs:          []string{"admin-1"},
		EnabledMCPTools:       []llm.EnabledMCPTool{{ServerOrigin: "https://x", ToolName: "t"}},
		MCPDynamicToolLoading: false,
		Model:                 "gpt-4",
		EnableVision:          true,
		ReasoningEffort:       "high",
		ThinkingBudget:        4096,
	}
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	s := string(raw)

	// Verify camelCase only — no snake_case escapees.
	assert.Contains(t, s, `"displayName"`)
	assert.Contains(t, s, `"serviceID"`)
	assert.Contains(t, s, `"channelAccessLevel"`)
	assert.Contains(t, s, `"adminUserIDs"`)
	assert.Contains(t, s, `"enabledMCPTools"`)
	assert.Contains(t, s, `"mcpDynamicToolLoading":false`)
	assert.Contains(t, s, `"reasoningEffort"`)
	assert.NotContains(t, s, `"display_name"`)
	assert.NotContains(t, s, `"service_id"`)
	assert.NotContains(t, s, `"admin_user_ids"`)
	assert.NotContains(t, s, `"enabled_tools"`)

	var decoded CreateAgentRequest
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, req.DisplayName, decoded.DisplayName)
	assert.Equal(t, req.ServiceID, decoded.ServiceID)
	assert.Equal(t, req.AdminUserIDs, decoded.AdminUserIDs)
	assert.Equal(t, req.EnabledMCPTools, decoded.EnabledMCPTools)
	assert.False(t, decoded.MCPDynamicToolLoading)
	assert.True(t, decoded.EnableVision)
}

func TestBotConfigJSONRoundTrip(t *testing.T) {
	cfg := llm.BotConfig{
		ID:           "agent-1",
		Name:         "my-agent",
		DisplayName:  "My Agent",
		ServiceID:    "svc-1",
		BotUserID:    "bot-user-id",
		CreatorID:    "creator-1",
		AdminUserIDs: []string{"admin-1"},
		CreateAt:     100,
		UpdateAt:     200,
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	s := string(raw)

	assert.Contains(t, s, `"botUserID":"bot-user-id"`)
	assert.Contains(t, s, `"creatorID":"creator-1"`)
	assert.Contains(t, s, `"adminUserIDs":["admin-1"]`)
	assert.Contains(t, s, `"createAt":100`)
	assert.Contains(t, s, `"updateAt":200`)
	assert.Contains(t, s, `"enabledMCPTools":null`)
	assert.Contains(t, s, `"mcpDynamicToolLoading":false`)
	assert.NotContains(t, s, `"deleteAt"`) // omitempty

	// Round-trip
	var decoded llm.BotConfig
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, cfg.ID, decoded.ID)
	assert.Equal(t, cfg.BotUserID, decoded.BotUserID)
	assert.Equal(t, cfg.CreatorID, decoded.CreatorID)
	assert.Equal(t, cfg.AdminUserIDs, decoded.AdminUserIDs)
	assert.False(t, decoded.MCPDynamicToolLoading)
	assert.Nil(t, decoded.EnabledMCPTools)
}

func TestBotConfigJSONPreservesEmptyEnabledMCPTools(t *testing.T) {
	cfg := llm.BotConfig{
		ID:              "agent-1",
		Name:            "my-agent",
		DisplayName:     "My Agent",
		ServiceID:       "svc-1",
		EnabledMCPTools: []llm.EnabledMCPTool{},
	}

	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"enabledMCPTools":[]`)

	var decoded llm.BotConfig
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NotNil(t, decoded.EnabledMCPTools)
	assert.Empty(t, decoded.EnabledMCPTools)
}

func TestBotConfigIsCreatorIsAdmin(t *testing.T) {
	cfg := llm.BotConfig{
		CreatorID:    "creator-1",
		AdminUserIDs: []string{"admin-1", "admin-2"},
	}
	assert.True(t, cfg.IsCreator("creator-1"))
	assert.False(t, cfg.IsCreator("admin-1"))
	assert.False(t, cfg.IsCreator(""))
	assert.False(t, cfg.IsCreator("other"))

	assert.True(t, cfg.IsAdmin("creator-1"))
	assert.True(t, cfg.IsAdmin("admin-1"))
	assert.True(t, cfg.IsAdmin("admin-2"))
	assert.False(t, cfg.IsAdmin("other"))
	assert.False(t, cfg.IsAdmin(""))

	// Migrated legacy bot: CreatorID == ""
	migrated := llm.BotConfig{CreatorID: "", AdminUserIDs: []string{"admin-1"}}
	assert.False(t, migrated.IsCreator(""))
	assert.False(t, migrated.IsCreator("anyone"))
	assert.True(t, migrated.IsAdmin("admin-1"))
	assert.False(t, migrated.IsAdmin(""))
}

func TestCanUserAccessAgentCreatorAdminBypass(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)
	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Agent that normally blocks every user, but grants access to creator + admin.
	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID:              "agent-1",
		CreatorID:       "creator-user",
		AdminUserIDs:    []string{"admin-user"},
		UserAccessLevel: llm.UserAccessLevelNone,
	}

	// Creator can see it via GET /agents.
	recorder := doRequest(e.api, http.MethodGet, "/agents", nil, "creator-user")
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	var agents []*llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Result().Body).Decode(&agents))
	require.Len(t, agents, 1)

	// Admin can see it.
	recorder = doRequest(e.api, http.MethodGet, "/agents", nil, "admin-user")
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	require.NoError(t, json.NewDecoder(recorder.Result().Body).Decode(&agents))
	require.Len(t, agents, 1)

	// Random user cannot — UserAccessLevelNone blocks them.
	recorder = doRequest(e.api, http.MethodGet, "/agents", nil, "random-user")
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	require.NoError(t, json.NewDecoder(recorder.Result().Body).Decode(&agents))
	require.Empty(t, agents)
}
