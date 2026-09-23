// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"net/http"
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
		name       string
		level      enterprise.Level
		existing   int
		configBots int
		// onInactive agents reference the second configured service, which
		// is inactive below Enterprise and so takes no slot in the cap.
		onInactive  int
		wantCreated bool
	}{
		{name: "unlicensed first agent", level: enterprise.LevelUnlicensed, existing: 0, wantCreated: true},
		{name: "unlicensed with an agent on an inactive service", level: enterprise.LevelUnlicensed, onInactive: 1, wantCreated: true},
		{name: "professional with agents on an inactive service", level: enterprise.LevelProfessional, existing: 2, onInactive: 2, wantCreated: true},
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

			store := e.api.configStore.(*mockConfigStore)
			store.cfg.Services = append(store.cfg.Services, llm.ServiceConfig{ID: "svc-2", Name: "Second", Type: "openai"})
			addAgents := func(n int, serviceID string) {
				for i := 0; i < n; i++ {
					id := model.NewId()
					e.agentStore.agents[id] = &llm.BotConfig{ID: id, Name: "existing" + id, DisplayName: "Existing", CreatorID: "other", ServiceID: serviceID}
				}
			}
			addAgents(tc.existing, "svc-1")
			addAgents(tc.onInactive, "svc-2")
			if tc.configBots > 0 {
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
			assert.Contains(t, body.Error, "AI agent")
			assert.NotEmpty(t, body.LicenseRequired)
		})
	}
}

func TestCreateAgentLicenseGates(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	tests := []struct {
		name     string
		fields   map[string]any
		minLevel enterprise.Level
	}{
		{name: "access controls", fields: map[string]any{"channelAccessLevel": int(llm.ChannelAccessLevelAllow), "channelIDs": []string{"ch1"}}, minLevel: enterprise.LevelProfessional},
		{name: "provider web search", fields: map[string]any{"enabledNativeTools": []string{llm.NativeToolWebSearch}}, minLevel: enterprise.LevelProfessional},
		{name: "service-account auth", fields: map[string]any{"useServiceAccountAuth": true}, minLevel: enterprise.LevelEnterprise},
		{name: "second LLM service", fields: map[string]any{"serviceID": "svc-2"}, minLevel: enterprise.LevelEnterprise},
		{name: "attribute-based access", fields: map[string]any{"userAccessLevel": int(llm.UserAccessLevelAttributeBased)}, minLevel: enterprise.LevelEnterpriseAdvanced},
	}

	for _, tc := range tests {
		for _, level := range enterprisetest.AllLevels {
			t.Run(tc.name+"/"+level.String(), func(t *testing.T) {
				e := setupAgentTestEnvironment(t)
				defer e.Cleanup(t)
				e.OverrideLicense(enterprisetest.LicenseFor(level))
				store := e.api.configStore.(*mockConfigStore)
				store.cfg.Services = append(store.cfg.Services, llm.ServiceConfig{ID: "svc-2", Name: "Other", Type: "openai"})
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true).Maybe()
				e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{
					UserId: "bot-user-id-created", Username: "my-agent", DisplayName: "My Agent",
				}, nil).Maybe()
				e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

				recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(tc.fields), testUserID)
				if level >= tc.minLevel {
					// Attribute-based access also needs ABAC on the server, so
					// a licensed save can still fail validation; it must not
					// fail for licensing.
					require.NotEqual(t, http.StatusForbidden, recorder.Result().StatusCode)
					return
				}
				require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
				var body licenseErrorResponse
				require.NoError(t, json.NewDecoder(recorder.Body).Decode(&body))
				assert.Equal(t, tc.minLevel.Key(), body.LicenseRequired)
			})
		}
	}
}

// TestUpdateAgentLicensedSettingsStayEditableWhenUnlicensed pins that turning
// a gated setting off, and saving an agent whose stored gated settings are
// unchanged, never needs a license.
func TestUpdateAgentLicensedSettingsStayEditableWhenUnlicensed(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	tests := []struct {
		name   string
		stored llm.BotConfig
		change map[string]any
	}{
		{name: "opening access controls", stored: llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAllow, ChannelIDs: []string{"ch1"}}, change: map[string]any{"channelAccessLevel": int(llm.ChannelAccessLevelAll), "channelIDs": []string{}}},
		{name: "turning service-account auth off", stored: llm.BotConfig{UseServiceAccountAuth: true}, change: map[string]any{"useServiceAccountAuth": false}},
		{name: "renaming an agent on a non-active service", stored: llm.BotConfig{ServiceID: "svc-2"}, change: map[string]any{"displayName": "Renamed"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			e.OverrideLicense(enterprisetest.LicenseFor(enterprise.LevelUnlicensed))
			store := e.api.configStore.(*mockConfigStore)
			store.cfg.Services = append(store.cfg.Services, llm.ServiceConfig{ID: "svc-2", Name: "Other", Type: "openai"})
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true).Maybe()
			e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			stored := tc.stored
			stored.ID, stored.CreatorID, stored.BotUserID = "agent-1", testUserID, "bot-1"
			stored.DisplayName, stored.Name = "Original", "original"
			if stored.ServiceID == "" {
				stored.ServiceID = "svc-1"
			}
			e.agentStore.agents["agent-1"] = &stored
			recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", updateAgentBodyFromStored(&stored, tc.change), testUserID)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
		})
	}
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
			if _, capped := enterprise.AgentLimitFor(level); !capped {
				assert.Empty(t, recorder.Result().Header.Get(AgentActiveCountHeader))
				return
			}
			assert.Equal(t, "1", recorder.Result().Header.Get(AgentActiveCountHeader))
		})
	}
}
