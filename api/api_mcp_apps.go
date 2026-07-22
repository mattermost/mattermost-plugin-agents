// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/sandbox"
)

// mcpAppsSameOriginSandboxPath is the plugin-route path of the insecure
// same-origin sandbox page. The webapp receives the absolute URL via the
// mcpApps bootstrap on GET /ai_bots; it never builds this path itself.
const mcpAppsSameOriginSandboxPath = "/mcp/apps/sandbox"

// MCPApps disabled reasons (bootstrap contract with Phase 1c).
const (
	mcpAppsDisabledReasonOff             = "apps_disabled"
	mcpAppsDisabledReasonNoSandboxOrigin = "no_sandbox_origin"
)

// MCPAppsInfo tells the webapp whether MCP Apps rendering is available and
// where the sandbox page lives. Returned inside AIBotsResponse (camelCase,
// matching that DTO's convention).
type MCPAppsInfo struct {
	Enabled bool `json:"enabled"`
	// SandboxURL is the absolute URL of the sandbox page (ready to be used
	// as @mcp-ui/client's sandbox.url). Empty when Enabled is false.
	SandboxURL string `json:"sandboxURL,omitempty"`
	// DisabledReason is set when Enabled is false: "apps_disabled" or
	// "no_sandbox_origin".
	DisabledReason string `json:"disabledReason,omitempty"`
}

// siteURLOrigin returns scheme://host[:port] of the configured Site URL.
func (a *API) siteURLOrigin() (string, error) {
	cfg := a.pluginAPI.Configuration.GetConfig()
	if cfg.ServiceSettings.SiteURL == nil || *cfg.ServiceSettings.SiteURL == "" {
		return "", fmt.Errorf("site URL is empty")
	}
	parsed, err := url.Parse(strings.TrimSpace(*cfg.ServiceSettings.SiteURL))
	if err != nil {
		return "", fmt.Errorf("invalid site URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("site URL must include scheme and host")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

// resolveMCPAppsInfo computes the effective MCP Apps state from config.
// Precedence (Phase 1c contract): apps disabled → off; SandboxURL set →
// external sandbox page URL; else insecure opt-in → same-origin plugin
// route; else off with reason.
func (a *API) resolveMCPAppsInfo() MCPAppsInfo {
	apps := a.config.MCP().Apps
	if !apps.Enabled {
		return MCPAppsInfo{DisabledReason: mcpAppsDisabledReasonOff}
	}
	if sandboxURL := strings.TrimSpace(apps.SandboxURL); sandboxURL != "" {
		return MCPAppsInfo{Enabled: true, SandboxURL: strings.TrimRight(sandboxURL, "/") + "/sandbox.html"}
	}
	if apps.AllowInsecureSameOriginSandbox {
		origin, err := a.siteURLOrigin()
		if err != nil {
			a.pluginAPI.Log.Error("MCP Apps: cannot resolve Site URL origin", "error", err)
			return MCPAppsInfo{DisabledReason: mcpAppsDisabledReasonNoSandboxOrigin}
		}
		return MCPAppsInfo{Enabled: true, SandboxURL: origin + "/plugins/mattermost-ai/mcp/apps/sandbox"}
	}
	return MCPAppsInfo{DisabledReason: mcpAppsDisabledReasonNoSandboxOrigin}
}

// handleGetSameOriginSandbox serves sandbox.html from the Mattermost origin.
// 404 unless the insecure same-origin mode is the effective mode (apps
// enabled + opt-in true + no external SandboxURL).
func (a *API) handleGetSameOriginSandbox(c *gin.Context) {
	apps := a.config.MCP().Apps
	if !apps.Enabled || !apps.AllowInsecureSameOriginSandbox || strings.TrimSpace(apps.SandboxURL) != "" {
		c.Status(http.StatusNotFound)
		return
	}
	origin, err := a.siteURLOrigin()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to resolve site URL origin: %w", err))
		return
	}
	sandbox.ServePage(c.Writer, c.Request, origin, &a.pluginAPI.Log)
}
