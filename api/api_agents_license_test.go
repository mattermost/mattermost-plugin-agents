// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestAgentCreateQuotaByLicense(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	tests := []struct {
		name        string
		level       enterprise.Level
		existing    int
		configBots  int
		wantCreated bool
	}{
		{name: "unlicensed first agent", level: enterprise.LevelUnlicensed, existing: 0, wantCreated: true},
		{name: "unlicensed at cap", level: enterprise.LevelUnlicensed, existing: 1, wantCreated: false},
		{name: "unlicensed with a config bot", level: enterprise.LevelUnlicensed, configBots: 1, wantCreated: false},
		{name: "professional under cap", level: enterprise.LevelProfessional, existing: 2, wantCreated: true},
		{name: "professional at cap", level: enterprise.LevelProfessional, existing: 3, wantCreated: false},
		{name: "enterprise uncapped", level: enterprise.LevelEnterprise, existing: 10, wantCreated: true},
		{name: "enterprise advanced uncapped", level: enterprise.LevelEnterpriseAdvanced, existing: 10, wantCreated: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(tc.level))
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false).Maybe()
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
				UserId: "bot-user-id-created", Username: "my-agent", DisplayName: "My Agent",
			}, nil).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			for i := 0; i < tc.existing; i++ {
				id := model.NewId()
				e.agentStore.agents[id] = &llm.BotConfig{ID: id, Name: "existing-" + id, DisplayName: "Existing", CreatorID: "other"}
			}
			if tc.configBots > 0 {
				store := e.api.configStore.(*mockConfigStore)
				for i := 0; i < tc.configBots; i++ {
					store.cfg.Bots = append(store.cfg.Bots, llm.BotConfig{
						ID: "cfg-bot", Name: "cfgbot", DisplayName: "Cfg Bot", ServiceID: "svc-1",
					})
				}
			}

			recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(nil), testUserID)
			if tc.wantCreated {
				require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)
				return
			}
			require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
			var body licenseErrorResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&body))
			assert.Contains(t, body.Error, "AI agents")
			assert.NotEmpty(t, body.LicenseRequired)
		})
	}
}

func TestAgentAccessControlsLicenseGate(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	restricted := map[string]any{
		"channelAccessLevel": int(llm.ChannelAccessLevelAllow),
		"channelIDs":         []string{"ch1"},
	}

	for _, level := range enterprisetest.AllLevels {
		t.Run("create "+level.String(), func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
				UserId: "bot-user-id-created", Username: "my-agent", DisplayName: "My Agent",
			}, nil).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(restricted), testUserID)
			if level >= enterprise.LevelProfessional {
				require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)
				return
			}
			require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
			var body licenseErrorResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&body))
			assert.Equal(t, enterprise.LevelProfessional.Key(), body.LicenseRequired)
		})
	}

	t.Run("update opening restrictions is accepted when unlicensed", func(t *testing.T) {
		e := setupAgentTestEnvironment(t)
		defer e.Cleanup(t)
		e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
		e.OverrideLicense(enterprisetest.LicenseFor(enterprise.LevelUnlicensed))
		e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
		e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

		stored := &llm.BotConfig{
			ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
			DisplayName: "Original", Name: "original", ServiceID: "svc-1",
			ChannelAccessLevel: llm.ChannelAccessLevelAllow, ChannelIDs: []string{"ch1"},
		}
		e.agentStore.agents["agent-1"] = stored
		body := updateAgentBodyFromStored(stored, map[string]any{
			"channelAccessLevel": int(llm.ChannelAccessLevelAll),
			"channelIDs":         []string{},
		})
		recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
		require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	})
}

func TestAgentAttributeBasedLicenseGate(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
				UserId: "bot-user-id-created", Username: "my-agent", DisplayName: "My Agent",
			}, nil).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(map[string]any{
				"userAccessLevel": int(llm.UserAccessLevelAttributeBased),
			}), testUserID)
			if level >= enterprise.LevelEnterpriseAdvanced {
				// Advanced still needs ABAC to be available on the server; the
				// passthrough checker reports no_policy so the save may 400.
				require.NotEqual(t, http.StatusForbidden, recorder.Result().StatusCode)
				return
			}
			require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
			var body licenseErrorResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&body))
			assert.Equal(t, enterprise.LevelEnterpriseAdvanced.Key(), body.LicenseRequired)
		})
	}
}

func TestAgentServiceAccountAuthLicenseGate(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true)
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
				UserId: "bot-user-id-created", Username: "my-agent", DisplayName: "My Agent",
			}, nil).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(map[string]any{
				"useServiceAccountAuth": true,
			}), testUserID)
			if level >= enterprise.LevelEnterprise {
				require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)
				return
			}
			require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
			var body licenseErrorResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&body))
			assert.Equal(t, enterprise.LevelEnterprise.Key(), body.LicenseRequired)
		})
	}

	t.Run("turning off is accepted when unlicensed", func(t *testing.T) {
		e := setupAgentTestEnvironment(t)
		defer e.Cleanup(t)
		e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
		e.OverrideLicense(enterprisetest.LicenseFor(enterprise.LevelUnlicensed))
		e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true).Maybe()
		e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
		e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

		stored := &llm.BotConfig{
			ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
			DisplayName: "Original", Name: "original", ServiceID: "svc-1",
			UseServiceAccountAuth: true,
		}
		e.agentStore.agents["agent-1"] = stored
		body := updateAgentBodyFromStored(stored, map[string]any{"useServiceAccountAuth": false})
		recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
		require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	})
}

func TestAgentProviderWebSearchLicenseGate(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
				UserId: "bot-user-id-created", Username: "my-agent", DisplayName: "My Agent",
			}, nil).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(map[string]any{
				"enabledNativeTools": []string{llm.NativeToolWebSearch},
			}), testUserID)
			if level >= enterprise.LevelProfessional {
				require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)
				return
			}
			require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
		})
	}
}

func TestAgentServiceIDLicenseGate(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			store := e.api.configStore.(*mockConfigStore)
			store.cfg.Services = append(store.cfg.Services, llm.ServiceConfig{ID: "svc-2", Name: "Other", Type: "openai"})
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
				UserId: "bot-user-id-created", Username: "my-agent", DisplayName: "My Agent",
			}, nil).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(map[string]any{
				"serviceID": "svc-2",
			}), testUserID)
			if level >= enterprise.LevelEnterprise {
				require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)
				return
			}
			require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
			var body licenseErrorResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&body))
			assert.Equal(t, enterprise.LevelEnterprise.Key(), body.LicenseRequired)
		})
	}

	t.Run("unchanged non-active service is accepted", func(t *testing.T) {
		e := setupAgentTestEnvironment(t)
		defer e.Cleanup(t)
		e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
		e.OverrideLicense(enterprisetest.LicenseFor(enterprise.LevelUnlicensed))
		store := e.api.configStore.(*mockConfigStore)
		store.cfg.Services = append(store.cfg.Services, llm.ServiceConfig{ID: "svc-2", Name: "Other", Type: "openai"})
		e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
		e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

		stored := &llm.BotConfig{
			ID: "agent-1", CreatorID: testUserID, BotUserID: "bot-1",
			DisplayName: "Original", Name: "original", ServiceID: "svc-2",
		}
		e.agentStore.agents["agent-1"] = stored
		body := updateAgentBodyFromStored(stored, map[string]any{"displayName": "Renamed"})
		recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
		require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	})
}

func TestListAgentsQuotaHeadersByLicense(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false).Maybe()
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
			e.agentStore.agents["agent-1"] = &llm.BotConfig{
				ID: "agent-1", CreatorID: testUserID, DisplayName: "A", Name: "a", ServiceID: "svc-1",
			}

			recorder := doRequest(e.api, http.MethodGet, "/agents", nil, testUserID)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
			limit, capped := enterprise.AgentLimitFor(level)
			if !capped {
				assert.Empty(t, recorder.Result().Header.Get(AgentActiveCountHeader))
				assert.Empty(t, recorder.Result().Header.Get(AgentLimitHeader))
				return
			}
			assert.Equal(t, "1", recorder.Result().Header.Get(AgentActiveCountHeader))
			assert.Equal(t, strconv.Itoa(limit), recorder.Result().Header.Get(AgentLimitHeader))
		})
	}
}
