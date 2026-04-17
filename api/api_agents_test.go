// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
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
	cfg *config.Config
}

func (m *mockConfigStore) GetConfig() (*config.Config, error) {
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

	body := CreateAgentRequest{
		DisplayName: "My Agent",
		Username:    "my-agent",
		ServiceID:   "svc-1",
	}

	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	resp := recorder.Result()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agent))
	assert.Equal(t, "My Agent", agent.DisplayName)
	assert.Equal(t, "my-agent", agent.Name)
	assert.Equal(t, testUserID, agent.CreatorID)
	assert.NotEmpty(t, agent.ID)
	assert.True(t, agent.ReasoningEnabled)
	assert.False(t, agent.StructuredOutputEnabled)
}

func TestCreateAgentUsesServerDefaultsWhenOptionalFieldsOmitted(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	e.api.configStore = &mockConfigStore{
		cfg: &config.Config{
			Services: []llm.ServiceConfig{
				{ID: "svc-1", Name: "Test Service", Type: "openai"},
			},
			SelfServiceAgentDefaults: config.SelfServiceAgentDefaults{
				EnableVision:            model.NewPointer(false),
				DisableTools:            model.NewPointer(true),
				ReasoningEnabled:        model.NewPointer(false),
				ReasoningEffort:         model.NewPointer("high"),
				StructuredOutputEnabled: model.NewPointer(false),
			},
		},
	}

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
		UserId:      "bot-user-id-created",
		Username:    "my-agent",
		DisplayName: "My Agent",
		Description: "User-created AI agent",
	}, nil)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := CreateAgentRequest{
		DisplayName: "My Agent",
		Username:    "my-agent",
		ServiceID:   "svc-1",
	}

	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agent))
	assert.False(t, agent.EnableVision)
	assert.True(t, agent.DisableTools)
	assert.False(t, agent.ReasoningEnabled)
	assert.Equal(t, "high", agent.ReasoningEffort)
	assert.False(t, agent.StructuredOutputEnabled)
	assert.Equal(t, []string{"web_search"}, agent.EnabledNativeTools)
}

func TestCreateAgentHonorsExplicitNativeToolOverrides(t *testing.T) {
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

	emptyNativeTools := []string{}
	body := CreateAgentRequest{
		DisplayName:        "My Agent",
		Username:           "my-agent",
		ServiceID:          "svc-1",
		EnabledNativeTools: &emptyNativeTools,
	}

	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)

	var agent llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agent))
	assert.Empty(t, agent.EnabledNativeTools)
}

func TestCreateAgentForbiddenWithoutManageOwnPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := CreateAgentRequest{
		DisplayName: "Sysadmin Agent",
		Username:    "sysadmin-agent",
		ServiceID:   "svc-1",
	}

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

	body := CreateAgentRequest{
		DisplayName: "My Agent",
		Username:    "my-agent",
		ServiceID:   "svc-1",
	}

	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestCreateAgentWithoutLicense(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockUnlicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := CreateAgentRequest{
		DisplayName: "My Agent",
		Username:    "my-agent",
		ServiceID:   "svc-1",
	}

	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestListAgentsFiltersByAccess(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Seed agents: one accessible (UserAccessLevelAll), one blocked (UserAccessLevelNone)
	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: "other-user", DisplayName: "Public Agent",
		UserAccessLevel: llm.UserAccessLevelAll,
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
}

func TestUpdateAgentAsCreator(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Seed an agent owned by testUserID
	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
		DisplayName: "Original", Name: "original", ServiceID: "svc-1",
	}

	newName := "Updated"
	body := UpdateAgentRequest{DisplayName: &newName}

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

	// Seed an agent where testUserID is an admin but NOT the creator
	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: "other-user", BotUserID: "bot-1",
		DisplayName: "Original", Name: "original", ServiceID: "svc-1",
		AdminUserIDs: []string{testUserID},
	}

	newInstructions := "Be brief"
	body := UpdateAgentRequest{CustomInstructions: &newInstructions}

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
}

func TestUpdateAgentAsNonAdmin(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Seed an agent NOT owned by testUserID
	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: "other-user", BotUserID: "bot-1",
		DisplayName: "Not Mine", Name: "not-mine", ServiceID: "svc-1",
	}

	newName := "Hacked"
	body := UpdateAgentRequest{DisplayName: &newName}

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestUpdateAgentOwnedByOtherWithManageOthersPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: "other-user", BotUserID: "bot-1",
		DisplayName: "Theirs", Name: "theirs", ServiceID: "svc-1",
	}

	newName := "Admin Renamed"
	body := UpdateAgentRequest{DisplayName: &newName}
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
	e.mockAPI.On("UpdateBotActive", "bot-1", false).Return(&model.Bot{}, nil)
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

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: "", BotUserID: "bot-1",
		DisplayName: "Migrated", Name: "migrated", ServiceID: "svc-1",
	}

	newName := "Updated Migrated"
	body := UpdateAgentRequest{DisplayName: &newName}
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

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: "", BotUserID: "bot-1",
		DisplayName: "Migrated", Name: "migrated", ServiceID: "svc-1",
	}

	newName := "Hacked"
	body := UpdateAgentRequest{DisplayName: &newName}

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

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: "", BotUserID: "bot-1",
		DisplayName: "Migrated", Name: "migrated", ServiceID: "svc-1",
	}

	newName := "Updated By System Admin"
	body := UpdateAgentRequest{DisplayName: &newName}
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

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
		DisplayName: "Agent", Name: "same-user", ServiceID: "svc-1",
	}

	newUsername := "other-user"
	body := UpdateAgentRequest{Username: &newUsername}

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

func TestUpdateAgentInvalidServiceID(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
		DisplayName: "Agent", Name: "my-agent", ServiceID: "svc-1",
	}

	badSvc := "not-a-configured-service"
	body := UpdateAgentRequest{ServiceID: &badSvc}

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

func TestUpdateAgentEnabledMCPToolsOmittedPreservesImplicitAll(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
		ID:              "agent-1",
		CreatorID:       testUserID,
		BotUserID:       "bot-1",
		DisplayName:     "Agent",
		Name:            "my-agent",
		ServiceID:       "svc-1",
		EnabledMCPTools: nil,
	}

	instructions := "Updated instructions"
	body := UpdateAgentRequest{CustomInstructions: &instructions}

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	updated := e.agentStore.agents["agent-1"]
	require.NotNil(t, updated)
	assert.Nil(t, updated.EnabledMCPTools)
	assert.Contains(t, recorder.Body.String(), `"enabledMCPTools":null`)
}

func TestUpdateAgentEnabledMCPToolsNullSetsImplicitAll(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
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

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", map[string]any{
		"enabledMCPTools": nil,
	}, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	updated := e.agentStore.agents["agent-1"]
	require.NotNil(t, updated)
	assert.Nil(t, updated.EnabledMCPTools)
	assert.Contains(t, recorder.Body.String(), `"enabledMCPTools":null`)
}

func TestUpdateAgentEnabledMCPToolsEmptyArraySetsNoTools(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.agents["agent-1"] = &llm.BotConfig{
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

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", map[string]any{
		"enabledMCPTools": []llm.EnabledMCPTool{},
	}, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	updated := e.agentStore.agents["agent-1"]
	require.NotNil(t, updated)
	require.NotNil(t, updated.EnabledMCPTools)
	assert.Empty(t, updated.EnabledMCPTools)
	assert.Contains(t, recorder.Body.String(), `"enabledMCPTools":[]`)
}

// Suppress unused import warnings for multipart (used for avatar test below)
var _ = multipart.NewWriter

func TestCreateAgentRequestJSONRoundTrip(t *testing.T) {
	enableVision := true
	req := CreateAgentRequest{
		DisplayName:        "My Agent",
		Username:           "my-agent",
		ServiceID:          "svc-1",
		CustomInstructions: "Be brief",
		ChannelAccessLevel: int(llm.ChannelAccessLevelAllow),
		ChannelIDs:         []string{"c1", "c2"},
		UserAccessLevel:    int(llm.UserAccessLevelBlock),
		UserIDs:            []string{"u1"},
		TeamIDs:            []string{"t1"},
		AdminUserIDs:       []string{"admin-1"},
		EnabledMCPTools:    []llm.EnabledMCPTool{{ServerOrigin: "https://x", ToolName: "t"}},
		Model:              "gpt-4",
		EnableVision:       &enableVision,
		ReasoningEffort:    "high",
		ThinkingBudget:     4096,
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
	require.NotNil(t, decoded.EnableVision)
	assert.Equal(t, enableVision, *decoded.EnableVision)
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
	assert.NotContains(t, s, `"deleteAt"`) // omitempty

	// Round-trip
	var decoded llm.BotConfig
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, cfg.ID, decoded.ID)
	assert.Equal(t, cfg.BotUserID, decoded.BotUserID)
	assert.Equal(t, cfg.CreatorID, decoded.CreatorID)
	assert.Equal(t, cfg.AdminUserIDs, decoded.AdminUserIDs)
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
