// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/sandbox"
)

// mcpAppsSameOriginSandboxPath is the plugin-route path of the insecure
// same-origin sandbox page. Kept equal to sandbox.SameOriginPluginPath.
// The webapp receives the absolute URL via the mcpApps bootstrap on
// GET /ai_bots; it never builds this path itself.
const mcpAppsSameOriginSandboxPath = sandbox.SameOriginPluginPath

// MCPAppsInfo tells the webapp whether MCP Apps rendering is available and
// where the sandbox page lives. Returned inside AIBotsResponse (camelCase,
// matching that DTO's convention).
type MCPAppsInfo struct {
	Enabled bool `json:"enabled"`
	// SandboxURL is the absolute URL of the sandbox page (ready to be used
	// as @mcp-ui/client's sandbox.url). Empty when Enabled is false.
	SandboxURL string `json:"sandboxURL,omitempty"`
	// DisabledReason is set when Enabled is false: "apps_disabled",
	// "no_sandbox_origin", or "invalid_sandbox_url".
	DisabledReason string `json:"disabledReason,omitempty"`
}

func (a *API) siteURLString() string {
	cfg := a.pluginAPI.Configuration.GetConfig()
	if cfg.ServiceSettings.SiteURL == nil {
		return ""
	}
	return strings.TrimSpace(*cfg.ServiceSettings.SiteURL)
}

// resolveMCPAppsInfo computes the effective MCP Apps state via the
// canonical sandbox.Resolve (single source of truth for bootstrap, listener,
// and same-origin route).
func (a *API) resolveMCPAppsInfo() MCPAppsInfo {
	resolved := sandbox.Resolve(a.config.MCP().Apps, a.siteURLString())
	switch resolved.Mode {
	case sandbox.ModeExternal, sandbox.ModeSameOrigin:
		return MCPAppsInfo{Enabled: true, SandboxURL: resolved.PageURL}
	default:
		return MCPAppsInfo{Enabled: false, DisabledReason: resolved.DisabledReason}
	}
}

// handleGetSameOriginSandbox serves sandbox.html from the Mattermost origin.
// 404 unless the insecure same-origin mode is the effective mode.
func (a *API) handleGetSameOriginSandbox(c *gin.Context) {
	resolved := sandbox.Resolve(a.config.MCP().Apps, a.siteURLString())
	if resolved.Mode != sandbox.ModeSameOrigin {
		c.Status(http.StatusNotFound)
		return
	}
	if resolved.HostOrigin == "" {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to resolve site URL origin"))
		return
	}
	sandbox.ServePage(c.Writer, c.Request, resolved.HostOrigin, sandbox.PageModeSameOrigin, &a.pluginAPI.Log)
}
