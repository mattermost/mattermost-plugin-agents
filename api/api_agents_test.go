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
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/useragents"
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
	e.mockAPI.On("HasPermissionTo", testUserID, PermissionCreateAgent).Return(true)
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

	var agent useragents.UserAgent
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agent))
	assert.Equal(t, "My Agent", agent.DisplayName)
	assert.Equal(t, "my-agent", agent.Username)
	assert.Equal(t, testUserID, agent.CreatorID)
	assert.NotEmpty(t, agent.ID)
}

func TestCreateAgentWithManageSystemOnly(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, PermissionCreateAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true)
	e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
		UserId:      "bot-user-id-created",
		Username:    "sysadmin-agent",
		DisplayName: "Sysadmin Agent",
		Description: "User-created AI agent",
	}, nil)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := CreateAgentRequest{
		DisplayName: "Sysadmin Agent",
		Username:    "sysadmin-agent",
		ServiceID:   "svc-1",
	}

	recorder := doRequest(e.api, http.MethodPost, "/agents", body, testUserID)
	resp := recorder.Result()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var agent useragents.UserAgent
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agent))
	assert.Equal(t, "Sysadmin Agent", agent.DisplayName)
}

func TestCreateAgentWithoutPermission(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, PermissionCreateAgent).Return(false)
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

	// Seed agents: one accessible (UserAccessLevelAll=0), one blocked (UserAccessLevelNone=3)
	e.agentStore.agents["agent-1"] = &useragents.UserAgent{
		ID: "agent-1", CreatorID: "other-user", DisplayName: "Public Agent",
		UserAccessLevel: 0, // All
	}
	e.agentStore.agents["agent-2"] = &useragents.UserAgent{
		ID: "agent-2", CreatorID: "other-user", DisplayName: "Private Agent",
		UserAccessLevel: 3, // None
	}

	recorder := doRequest(e.api, http.MethodGet, "/agents", nil, testUserID)
	resp := recorder.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var agents []*useragents.UserAgent
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
	e.agentStore.agents["agent-1"] = &useragents.UserAgent{
		ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
		DisplayName: "Original", Username: "original", ServiceID: "svc-1",
	}

	newName := "Updated"
	body := UpdateAgentRequest{DisplayName: &newName}

	// Mock bot patch for display name sync
	e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var agent useragents.UserAgent
	require.NoError(t, json.NewDecoder(recorder.Result().Body).Decode(&agent))
	assert.Equal(t, "Updated", agent.DisplayName)
}

func TestUpdateAgentAsAdminUser(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Seed an agent where testUserID is an admin but NOT the creator
	e.agentStore.agents["agent-1"] = &useragents.UserAgent{
		ID: "agent-1", CreatorID: "other-user", BotUserID: "bot-1",
		DisplayName: "Original", Username: "original", ServiceID: "svc-1",
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
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Seed an agent NOT owned by testUserID
	e.agentStore.agents["agent-1"] = &useragents.UserAgent{
		ID: "agent-1", CreatorID: "other-user", BotUserID: "bot-1",
		DisplayName: "Not Mine", Username: "not-mine", ServiceID: "svc-1",
	}

	newName := "Hacked"
	body := UpdateAgentRequest{DisplayName: &newName}

	recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}

func TestDeleteAgentDeactivatesBot(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("UpdateBotActive", "bot-1", false).Return(&model.Bot{}, nil)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Seed an agent owned by testUserID
	e.agentStore.agents["agent-1"] = &useragents.UserAgent{
		ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
		DisplayName: "Doomed", Username: "doomed", ServiceID: "svc-1",
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

	// Verify no secret fields leak through
	raw, _ := json.Marshal(services[0])
	assert.NotContains(t, string(raw), "apiKey")
	assert.NotContains(t, string(raw), "awsSecret")
}

// Suppress unused import warnings for multipart (used for avatar test below)
var _ = multipart.NewWriter
