// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPutPolicyRoutesRequireEnterpriseAdvanced(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	creatorID := model.NewId()
	adminID := model.NewId()
	agentID := model.NewId()
	serviceID := model.NewId()
	serverID := model.NewId()
	policyBody := map[string]any{
		"rules": []map[string]any{{"actions": []string{"use"}, "expression": `user.attributes.department == "eng"`}},
	}

	routes := []struct {
		name   string
		method string
		path   string
		body   any
		userID string
	}{
		{name: "put agent policy", method: http.MethodPut, path: "/agents/" + agentID + "/access_policy", body: policyBody, userID: creatorID},
		{name: "put service policy", method: http.MethodPut, path: "/admin/services/" + serviceID + "/access_policy", body: policyBody, userID: adminID},
		{name: "put mcp policy", method: http.MethodPut, path: "/admin/mcp/" + serverID + "/access_policy", body: policyBody, userID: adminID},
	}

	for _, route := range routes {
		for _, level := range enterprisetest.AllLevels {
			t.Run(route.name+" "+level.String(), func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)
				e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
				e.OverrideLicense(enterprisetest.LicenseFor(level))
				e.api.accessChecker = accesscontrol.New(accesscontrol.PassthroughClient{}, e.mockAPI, accesscontrol.NoMCPServerIDs, nil)

				e.agentStore.agents[agentID] = &llm.BotConfig{
					ID: agentID, Name: "policyagent", DisplayName: "Policy Agent",
					ServiceID: serviceID, CreatorID: creatorID,
				}
				e.api.configStore = &mockConfigStore{
					cfg: &config.Config{
						Services: []llm.ServiceConfig{{ID: serviceID, Name: "Svc", Type: "openai"}},
						MCP: config.MCPConfig{
							Servers: []config.MCPServerConfig{{ID: serverID, Name: "Ext", Enabled: true, BaseURL: "https://mcp.example.com"}},
						},
					},
				}

				e.mockAPI.On("HasPermissionTo", adminID, model.PermissionManageSystem).Return(true).Maybe()
				e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(false).Maybe()
				e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOthersAgent).Return(false).Maybe()
				e.mockAPI.On("SaveAccessControlPolicy", mock.Anything, mock.AnythingOfType("*model.AccessControlPolicy")).
					Return(&model.AccessControlPolicy{ID: agentID}, nil).Maybe()

				recorder := doRequest(e.api, route.method, route.path, route.body, route.userID)
				if level >= enterprise.LevelEnterpriseAdvanced {
					require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
					return
				}
				require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
				var body licenseErrorResponse
				require.NoError(t, json.NewDecoder(recorder.Body).Decode(&body))
				assert.Equal(t, enterprise.LevelEnterpriseAdvanced.Key(), body.LicenseRequired)
			})
		}
	}
}

func TestGetAndDeletePolicyRoutesStayOpen(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	creatorID := model.NewId()
	agentID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)
	e.OverrideLicense(enterprisetest.LicenseFor(enterprise.LevelUnlicensed))
	e.agentStore.agents[agentID] = &llm.BotConfig{
		ID: agentID, Name: "policyagent", DisplayName: "Policy Agent",
		ServiceID: "svc-1", CreatorID: creatorID,
	}
	e.mockAPI.On("GetAccessControlPolicy", agentID).Return(&model.AccessControlPolicy{ID: agentID}, nil).Once()
	e.mockAPI.On("DeleteAccessControlPolicy", creatorID, mock.Anything, agentID).Return(nil).Once()
	e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOthersAgent).Return(false).Maybe()
	e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(false).Maybe()

	getRecorder := doRequest(e.api, http.MethodGet, "/agents/"+agentID+"/access_policy", nil, creatorID)
	require.Equal(t, http.StatusOK, getRecorder.Result().StatusCode)

	delRecorder := doRequest(e.api, http.MethodDelete, "/agents/"+agentID+"/access_policy", nil, creatorID)
	require.Equal(t, http.StatusOK, delRecorder.Result().StatusCode)
}
