// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestResolveMCPAppsInfo(t *testing.T) {
	tests := []struct {
		name    string
		apps    config.MCPAppsConfig
		siteURL string
		want    MCPAppsInfo
	}{
		{
			name: "apps disabled",
			apps: config.MCPAppsConfig{Enabled: false},
			want: MCPAppsInfo{Enabled: false, DisabledReason: mcpAppsDisabledReasonOff},
		},
		{
			name: "external URL",
			apps: config.MCPAppsConfig{Enabled: true, SandboxURL: "https://apps.example.com"},
			want: MCPAppsInfo{Enabled: true, SandboxURL: "https://apps.example.com/sandbox.html"},
		},
		{
			name: "external URL trailing slash",
			apps: config.MCPAppsConfig{Enabled: true, SandboxURL: "https://apps.example.com/"},
			want: MCPAppsInfo{Enabled: true, SandboxURL: "https://apps.example.com/sandbox.html"},
		},
		{
			name: "external URL with subpath",
			apps: config.MCPAppsConfig{Enabled: true, SandboxURL: "https://mm.example.com:8443/apps"},
			want: MCPAppsInfo{Enabled: true, SandboxURL: "https://mm.example.com:8443/apps/sandbox.html"},
		},
		{
			name: "external URL wins over insecure",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				SandboxURL:                     "https://apps.example.com",
				AllowInsecureSameOriginSandbox: true,
			},
			siteURL: "http://localhost:8065",
			want:    MCPAppsInfo{Enabled: true, SandboxURL: "https://apps.example.com/sandbox.html"},
		},
		{
			name: "insecure only",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				AllowInsecureSameOriginSandbox: true,
			},
			siteURL: "http://localhost:8065",
			want: MCPAppsInfo{
				Enabled:    true,
				SandboxURL: "http://localhost:8065/plugins/mattermost-ai/mcp/apps/sandbox",
			},
		},
		{
			name: "enabled, nothing configured",
			apps: config.MCPAppsConfig{Enabled: true},
			want: MCPAppsInfo{Enabled: false, DisabledReason: mcpAppsDisabledReasonNoSandboxOrigin},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			for i := 1; i <= 10; i++ {
				args := make([]interface{}, i)
				for j := range args {
					args[j] = mock.Anything
				}
				mockAPI.On("LogError", args...).Maybe()
			}
			siteURL := tt.siteURL
			mockAPI.On("GetConfig").Return(&model.Config{
				ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
			}).Maybe()

			a := &API{
				config:    &testConfigImpl{mcpConfig: mcp.Config{Apps: tt.apps}},
				pluginAPI: pluginapi.NewClient(mockAPI, nil),
			}
			require.Equal(t, tt.want, a.resolveMCPAppsInfo())
		})
	}
}

func TestHandleGetSameOriginSandbox(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	siteURL := "http://localhost:8065"

	tests := []struct {
		name           string
		apps           config.MCPAppsConfig
		query          string
		wantStatus     int
		wantBodySubstr string
		wantCSPContain string
	}{
		{
			name: "effective insecure mode",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				AllowInsecureSameOriginSandbox: true,
			},
			wantStatus:     http.StatusOK,
			wantBodySubstr: "ALLOWED_HOST_ORIGIN",
		},
		{
			name: "csp param honored",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				AllowInsecureSameOriginSandbox: true,
			},
			query:          "csp=" + url.QueryEscape(`{"connectDomains":["https://a.example.com"]}`),
			wantStatus:     http.StatusOK,
			wantCSPContain: "connect-src 'self' https://a.example.com",
		},
		{
			name: "apps disabled",
			apps: config.MCPAppsConfig{
				Enabled:                        false,
				AllowInsecureSameOriginSandbox: true,
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "opt-in off",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				AllowInsecureSameOriginSandbox: false,
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "external URL configured",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				AllowInsecureSameOriginSandbox: true,
				SandboxURL:                     "https://apps.example.com",
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.config.mcpConfig.Apps = tt.apps
			e.mockAPI.On("GetConfig").Unset()
			e.mockAPI.On("GetConfig").Return(&model.Config{
				ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
			}).Maybe()

			target := mcpAppsSameOriginSandboxPath
			if tt.query != "" {
				target += "?" + tt.query
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			// Intentionally no Mattermost-User-Id — route is unauthenticated.
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, rec, req)

			require.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantStatus != http.StatusOK {
				return
			}
			body := rec.Body.String()
			if tt.wantBodySubstr != "" {
				require.Contains(t, body, tt.wantBodySubstr)
			}
			require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
			require.Equal(t, "no-cache, no-store, must-revalidate", rec.Header().Get("Cache-Control"))
			csp := rec.Header().Get("Content-Security-Policy")
			require.NotEmpty(t, csp)
			if tt.wantCSPContain != "" {
				require.Contains(t, csp, tt.wantCSPContain)
			}
		})
	}
}

func TestHandleGetAIBotsIncludesMCPApps(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name               string
		apps               config.MCPAppsConfig
		wantEnabled        bool
		wantSandboxURL     string
		wantDisabledReason string
	}{
		{
			name:               "disabled by default",
			apps:               config.MCPAppsConfig{},
			wantEnabled:        false,
			wantDisabledReason: mcpAppsDisabledReasonOff,
		},
		{
			name: "external sandbox",
			apps: config.MCPAppsConfig{
				Enabled:    true,
				SandboxURL: "https://apps.example.com",
			},
			wantEnabled:    true,
			wantSandboxURL: "https://apps.example.com/sandbox.html",
		},
		{
			name: "enabled with no origin",
			apps: config.MCPAppsConfig{
				Enabled: true,
			},
			wantEnabled:        false,
			wantDisabledReason: mcpAppsDisabledReasonNoSandboxOrigin,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.config.mcpConfig.Apps = tt.apps
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})
			e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})

			req := httptest.NewRequest(http.MethodGet, "/ai_bots", nil)
			req.Header.Add("Mattermost-User-ID", "userid")
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, rec, req)
			require.Equal(t, http.StatusOK, rec.Code)

			raw := rec.Body.Bytes()
			require.Contains(t, string(raw), `"mcpApps"`)

			var response AIBotsResponse
			require.NoError(t, json.Unmarshal(raw, &response))
			require.Equal(t, tt.wantEnabled, response.MCPApps.Enabled)
			require.Equal(t, tt.wantSandboxURL, response.MCPApps.SandboxURL)
			require.Equal(t, tt.wantDisabledReason, response.MCPApps.DisabledReason)

			var asMap map[string]any
			require.NoError(t, json.Unmarshal(raw, &asMap))
			mcpApps, ok := asMap["mcpApps"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, tt.wantEnabled, mcpApps["enabled"])
			if tt.wantSandboxURL != "" {
				require.Equal(t, tt.wantSandboxURL, mcpApps["sandboxURL"])
			}
			if tt.wantDisabledReason != "" {
				require.Equal(t, tt.wantDisabledReason, mcpApps["disabledReason"])
			}
		})
	}
}
