// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestOAuthStartLicenseGate(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			req := httptest.NewRequest(http.MethodGet, "/mcp/oauth/jira/start", nil)
			req.Header.Add("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			resp := recorder.Result()

			if level >= enterprise.LevelEnterprise {
				requireNotLicenseDenied(t, resp)
				return
			}
			requireLicenseDenied(t, resp, enterprise.CapRemoteMCP)
		})
	}

	t.Run("nil checker fails closed", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)
		e.api.licenseChecker = nil
		e.mockAPI.On("LogError", mock.Anything).Maybe()

		req := httptest.NewRequest(http.MethodGet, "/mcp/oauth/jira/start", nil)
		req.Header.Add("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		requireLicenseDenied(t, recorder.Result(), enterprise.CapRemoteMCP)
	})
}

func TestMCPRegisterLicenseGate(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	body := mcp.PluginServerConfig{
		PluginID: testCallerPluginID,
		Name:     "Playbooks MCP",
		Path:     "/mcp",
		Enabled:  true,
	}

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			req := mcpRegisterRequest(t, body)
			req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
			resp := serveAndReturn(e, req)

			if level >= enterprise.LevelEnterprise {
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Len(t, e.mcp.registerCalls, 1)
				return
			}
			requireLicenseDenied(t, resp, enterprise.CapRemoteMCP)
			require.Empty(t, e.mcp.registerCalls)
		})
	}
}

func TestMCPUnregisterStaysOpen(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			req := mcpUnregisterRequest(t, map[string]string{})
			req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
			resp := serveAndReturn(e, req)
			requireNotLicenseDenied(t, resp)
			require.Equal(t, []string{testCallerPluginID}, e.mcp.unregisterCalls)
		})
	}
}

func TestGetUserMCPToolsStaysOpen(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.api.mcpClientManager = &mockMCPClientManager{}

			_, status := requestUserMCPTools(t, e.api, "")
			require.Equal(t, http.StatusOK, status)
		})
	}
}

func TestDeleteMCPOAuthStaysOpen(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			req := httptest.NewRequest(http.MethodDelete, "/mcp/oauth/TestServer", nil)
			req.Header.Add("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			requireNotLicenseDenied(t, recorder.Result())
		})
	}
}

func TestMCPServiceAccountCatalogLicenseGate(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.agentStore.agents["agent-1"] = &llm.BotConfig{
				ID:                    "agent-1",
				CreatorID:             testUserID,
				BotUserID:             testBotUserID,
				UseServiceAccountAuth: true,
			}
			mcpMock := &mockMCPClientManager{
				tools:               []llm.Tool{{Name: "user_tool", ServerOrigin: "https://user.example.com"}},
				serviceAccountTools: []llm.Tool{{Name: "sa_tool", ServerOrigin: "https://sa.example.com"}},
			}
			e.api.mcpClientManager = mcpMock

			_, status := requestUserMCPTools(t, e.api, "catalog=service_account&agent_id=agent-1")
			require.Equal(t, http.StatusOK, status)

			if level >= enterprise.LevelEnterprise {
				require.Equal(t, []string{testBotUserID}, mcpMock.getServiceAccountCalls)
				require.Empty(t, mcpMock.getContexts)
				return
			}

			require.Empty(t, mcpMock.getServiceAccountCalls)
			require.Len(t, mcpMock.getContexts, 1)
		})
	}

	t.Run("nil checker fails closed to user catalog", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		e.api.licenseChecker = nil
		e.agentStore.agents["agent-1"] = &llm.BotConfig{
			ID:                    "agent-1",
			CreatorID:             testUserID,
			BotUserID:             testBotUserID,
			UseServiceAccountAuth: true,
		}
		mcpMock := &mockMCPClientManager{}
		e.api.mcpClientManager = mcpMock

		_, status := requestUserMCPTools(t, e.api, "catalog=service_account&agent_id=agent-1")
		require.Equal(t, http.StatusOK, status)
		require.Empty(t, mcpMock.getServiceAccountCalls)
		require.Len(t, mcpMock.getContexts, 1)
	})
}

func TestRefreshUserMCPToolsStaysOpen(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.api.mcpClientManager = &mockMCPClientManager{}

			req := httptest.NewRequest(http.MethodPost, "/mcp/tools/refresh", nil)
			req.Header.Add("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			requireNotLicenseDenied(t, recorder.Result())
		})
	}
}
