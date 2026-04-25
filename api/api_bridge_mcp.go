// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/public/bridgeclient"
)

// externalServerRebuilder is the minimal contract for live-updating the
// external MCP aggregation after plugin server registry changes.
type externalServerRebuilder interface {
	RebuildExternalServer()
}

// unregisterRequest is the minimal body shape for POST /bridge/v1/mcp/unregister.
// The handler only needs the plugin_id; we intentionally do NOT reuse
// mcp.PluginServerConfig here to avoid implying that Name/Path/Enabled/
// ExposeExternal are honored on unregister (they are not).
type unregisterRequest struct {
	PluginID string `json:"plugin_id"`
}

func (a *API) resolveExternalServerRebuilder() externalServerRebuilder {
	if a.externalRebuilderForTest != nil {
		return a.externalRebuilderForTest
	}
	if a.mcpHandlers == nil {
		return nil
	}
	return a.mcpHandlers
}

// handleMCPRegister handles POST /bridge/v1/mcp/register.
//
// Called by source plugins via mcphelper.Server.Register(). The
// request MUST originate from an inter-plugin HTTP call — the
// interPluginAuthorizationRequired middleware on the parent group ensures
// Mattermost-Plugin-ID is set and trustworthy (the Mattermost server strips
// this header on external requests, see
// server/channels/app/plugin_requests.go:188-189).
//
// Security:
//  1. Middleware rejects missing Mattermost-Plugin-ID with 401.
//  2. Handler rejects a body claiming a different PluginID than the header
//     with 403 — this is the release-gate security test. Without this check,
//     any authenticated plugin could register a fake server claiming to be
//     com.mattermost.plugin-playbooks and intercept that plugin's tool calls.
func (a *API) handleMCPRegister(c *gin.Context) {
	var cfg mcp.PluginServerConfig
	if err := c.BindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}

	// Required-field validation. Booleans (Enabled, ExposeExternal) have valid
	// zero values and are not required to be true.
	if cfg.PluginID == "" {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "plugin_id is required",
		})
		return
	}
	if cfg.Name == "" {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "name is required",
		})
		return
	}
	if cfg.Path == "" {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "path is required",
		})
		return
	}

	// CRITICAL SECURITY CHECK: enforce that the plugin registering this
	// server is the one the config claims to be from. The middleware only
	// validates that Mattermost-Plugin-ID is present (identity of the caller
	// is trusted because the Mattermost server sets it on inter-plugin
	// dispatch); the handler validates that the body does not claim a
	// different identity than the caller.
	//
	// If these diverge, RegisterPluginServer is NOT called and we return 403.
	callerPluginID := c.GetHeader("Mattermost-Plugin-ID")
	if cfg.PluginID != callerPluginID {
		c.JSON(http.StatusForbidden, bridgeclient.ErrorResponse{
			Error: "plugin_id does not match Mattermost-Plugin-ID header",
		})
		return
	}

	// Preserve admin-managed fields across re-registration.
	//
	// Enabled, ExposeExternal, and ToolConfigs are admin-owned after first
	// registration; they are toggled via PUT /admin/mcp/plugin-servers/:pluginID.
	// The source plugin's wire payload does NOT carry them (see
	// public/mcphelper/mcphelper.go: PluginMCPServer is {PluginID, Name, Path,
	// Version}), so they arrive as zero values — letting the plugin payload win
	// would silently clobber admin state. Identity fields (PluginID/Name/Path)
	// are owned by the plugin and ARE refreshed from the new request.
	//
	// We consult two sources in priority order before falling through to the
	// plugin's payload:
	//
	//  1. The in-memory entry (GetPluginServer): refreshed on every Register +
	//     hydrated from config on ReInit. The steady-state source of truth.
	//
	//  2. The persisted config (configStore.GetConfig().MCP.PluginServers):
	//     fallback used when the in-memory entry was just wiped by an
	//     Unregister and ReInit hasn't re-hydrated yet. This is the
	//     OnDeactivate→OnActivate (plugin restart) sequence: Unregister
	//     deletes the in-memory entry, the immediately-following Register
	//     would otherwise see the entry as missing and store zero-valued
	//     admin fields. syncPluginServersFromConfig only runs on
	//     Container.Update (admin save / cluster broadcast), NOT on plugin
	//     restart, so without this fallback admin state drifts to zero until
	//     the next admin action.
	//
	// First-time registration (no entry in either source) falls through to
	// use the plugin's cfg as-is. This lets first-party plugins opt into
	// ExposeExternal by default on install; subsequent admin edits take
	// precedence.
	if existing, found := a.mcpClientManager.GetPluginServer(cfg.PluginID); found {
		cfg.Enabled = existing.Enabled
		cfg.ExposeExternal = existing.ExposeExternal
		cfg.ToolConfigs = existing.ToolConfigs
	} else if persisted, ok := a.findPersistedPluginServer(cfg.PluginID); ok {
		cfg.Enabled = persisted.Enabled
		cfg.ExposeExternal = persisted.ExposeExternal
		cfg.ToolConfigs = persisted.ToolConfigs
	}
	a.mcpClientManager.RegisterPluginServer(cfg)

	if cfg.ExposeExternal {
		if rb := a.resolveExternalServerRebuilder(); rb != nil {
			rb.RebuildExternalServer()
		}
	}

	c.Status(http.StatusOK)
}

// handleMCPUnregister handles POST /bridge/v1/mcp/unregister.
//
// Called synchronously by source plugins from OnDeactivate via
// mcphelper.Server.Unregister. Body is {"plugin_id": "..."}.
//
// Same security model as handleMCPRegister: middleware ensures header
// presence; handler enforces body.plugin_id == header to prevent one plugin
// from unregistering another plugin's server.
func (a *API) handleMCPUnregister(c *gin.Context) {
	var req unregisterRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}
	if req.PluginID == "" {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "plugin_id is required",
		})
		return
	}

	callerPluginID := c.GetHeader("Mattermost-Plugin-ID")
	if req.PluginID != callerPluginID {
		c.JSON(http.StatusForbidden, bridgeclient.ErrorResponse{
			Error: "plugin_id does not match Mattermost-Plugin-ID header",
		})
		return
	}

	a.mcpClientManager.UnregisterPluginServer(req.PluginID)

	if rb := a.resolveExternalServerRebuilder(); rb != nil {
		rb.RebuildExternalServer()
	}

	c.Status(http.StatusOK)
}

// findPersistedPluginServer looks up admin-owned state for pluginID in the
// persisted plugin config. Used by handleMCPRegister to recover admin fields
// when the in-memory entry was wiped by a recent Unregister (typically a
// source-plugin OnDeactivate→OnActivate cycle), before
// syncPluginServersFromConfig has fired again on ReInit.
//
// Returns (PluginServerConfig{}, false) if configStore is nil, GetConfig
// errors, the loaded config is nil, or no matching pluginID is found.
//
// Iteration uses index access (not range-with-value) to avoid copying each
// PluginServerConfig (it carries a ToolConfigs slice header per element).
func (a *API) findPersistedPluginServer(pluginID string) (mcp.PluginServerConfig, bool) {
	if a.configStore == nil {
		return mcp.PluginServerConfig{}, false
	}
	cfg, err := a.configStore.GetConfig()
	if err != nil || cfg == nil {
		return mcp.PluginServerConfig{}, false
	}
	for i := range cfg.MCP.PluginServers {
		if cfg.MCP.PluginServers[i].PluginID == pluginID {
			return cfg.MCP.PluginServers[i], true
		}
	}
	return mcp.PluginServerConfig{}, false
}
