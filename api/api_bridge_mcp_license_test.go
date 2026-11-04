// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"io"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/stretchr/testify/require"
)

func TestHandleMCPRegisterLicenseGate(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	validCfg := mcp.PluginServerConfig{
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
			records := e.CaptureAuditRecords()

			req := mcpRegisterRequest(t, validCfg)
			req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
			resp := serveAndReturn(e, req)

			require.Len(t, *records, 1)
			require.Equal(t, testCallerPluginID, (*records)[0].EventData.Parameters[audit.KeyCallerPluginID],
				"the caller is attributed on success and on license denial")

			if level >= enterprise.LevelEnterprise {
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Len(t, e.mcp.registerCalls, 1)
				return
			}

			requireLicenseDenied(t, resp, enterprise.CapRemoteMCP)
			require.Empty(t, e.mcp.registerCalls)
		})
	}

	t.Run("nil checker fails closed", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)
		e.api.licenseChecker = nil

		req := mcpRegisterRequest(t, validCfg)
		req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
		resp := serveAndReturn(e, req)
		requireLicenseDenied(t, resp, enterprise.CapRemoteMCP)
		require.Empty(t, e.mcp.registerCalls)
	})
}

func TestHandleMCPUnregisterOpenAtEveryLevel(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))

			req := mcpUnregisterRequest(t, map[string]string{"plugin_id": testCallerPluginID})
			req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
			resp := serveAndReturn(e, req)
			requireNotLicenseDenied(t, resp)
			require.Equal(t, []string{testCallerPluginID}, e.mcp.unregisterCalls)
		})
	}

	t.Run("nil checker stays open", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)
		e.api.licenseChecker = nil

		req := mcpUnregisterRequest(t, map[string]string{"plugin_id": testCallerPluginID})
		req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
		resp := serveAndReturn(e, req)
		requireNotLicenseDenied(t, resp)
		require.Equal(t, []string{testCallerPluginID}, e.mcp.unregisterCalls)
	})
}
