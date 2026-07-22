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
	"github.com/mattermost/mattermost-plugin-agents/v2/sandbox"
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
			want: MCPAppsInfo{Enabled: false, DisabledReason: sandbox.DisabledReasonAppsDisabled},
		},
		{
			name:    "external URL",
			apps:    config.MCPAppsConfig{Enabled: true, SandboxURL: "https://apps.example.com"},
			siteURL: "https://mm.example.com",
			want:    MCPAppsInfo{Enabled: true, SandboxURL: "https://apps.example.com/sandbox.html"},
		},
		{
			name:    "external URL trailing slash",
			apps:    config.MCPAppsConfig{Enabled: true, SandboxURL: "https://apps.example.com/"},
			siteURL: "https://mm.example.com",
			want:    MCPAppsInfo{Enabled: true, SandboxURL: "https://apps.example.com/sandbox.html"},
		},
		{
			name:    "external URL with subpath",
			apps:    config.MCPAppsConfig{Enabled: true, SandboxURL: "https://mm.example.com:8443/apps"},
			siteURL: "https://mm.example.com",
			want:    MCPAppsInfo{Enabled: true, SandboxURL: "https://mm.example.com:8443/apps/sandbox.html"},
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
			name: "insecure with SiteURL subpath",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				AllowInsecureSameOriginSandbox: true,
			},
			siteURL: "https://example.com/mattermost",
			want: MCPAppsInfo{
				Enabled:    true,
				SandboxURL: "https://example.com/mattermost/plugins/mattermost-ai/mcp/apps/sandbox",
			},
		},
		{
			name: "same-origin sandboxURL without opt-in fails closed",
			apps: config.MCPAppsConfig{
				Enabled:    true,
				SandboxURL: "https://mm.example.com/apps",
			},
			siteURL: "https://mm.example.com",
			want:    MCPAppsInfo{Enabled: false, DisabledReason: sandbox.DisabledReasonInvalidSandboxURL},
		},
		{
			name: "same-origin sandboxURL with opt-in fails closed",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				SandboxURL:                     "https://mm.example.com/proxy",
				AllowInsecureSameOriginSandbox: true,
			},
			siteURL: "https://mm.example.com",
			want:    MCPAppsInfo{Enabled: false, DisabledReason: sandbox.DisabledReasonInvalidSandboxURL},
		},
		{
			name:    "enabled, nothing configured",
			apps:    config.MCPAppsConfig{Enabled: true},
			siteURL: "https://mm.example.com",
			want:    MCPAppsInfo{Enabled: false, DisabledReason: sandbox.DisabledReasonNoSandboxOrigin},
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
		siteURL        string
		query          string
		wantStatus     int
		wantBodySubstr string
		wantBodyAbsent string
		wantCSPContain string
	}{
		{
			name: "effective insecure mode skips isolation self-test",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				AllowInsecureSameOriginSandbox: true,
			},
			wantStatus:     http.StatusOK,
			wantBodySubstr: "REQUIRE_ORIGIN_ISOLATION = false",
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
			su := siteURL
			if tt.siteURL != "" {
				su = tt.siteURL
			}
			e.mockAPI.On("GetConfig").Return(&model.Config{
				ServiceSettings: model.ServiceSettings{SiteURL: &su},
			}).Maybe()

			target := mcpAppsSameOriginSandboxPath
			if tt.query != "" {
				target += "?" + tt.query
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
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
			if tt.wantBodyAbsent != "" {
				require.NotContains(t, body, tt.wantBodyAbsent)
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
			wantDisabledReason: sandbox.DisabledReasonAppsDisabled,
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
			wantDisabledReason: sandbox.DisabledReasonNoSandboxOrigin,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.config.mcpConfig.Apps = tt.apps
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})
			e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
			siteURL := "https://mm.example.com"
			e.mockAPI.On("GetConfig").Unset()
			e.mockAPI.On("GetConfig").Return(&model.Config{
				ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
			}).Maybe()

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
		})
	}
}
