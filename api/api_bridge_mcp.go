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

// externalServerRebuilder is the minimal contract the bridge-MCP handlers need
// to live-update the external MCP aggregation when a plugin server registers
// or unregisters with ExposeExternal=true.
//
// It is implemented by *mcpserver.PluginMCPHandlers once Phase 1G-3 lands.
// Before 1G, the type assertion in resolveExternalServerRebuilder fails
// gracefully and the rebuild is skipped — the register/unregister endpoints
// still succeed, the registry is still updated, and 1G's first rebuild on
// startup picks everything up.
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

// resolveExternalServerRebuilder returns the active rebuilder implementation,
// or nil if none is available (production pre-1G, or EnablePluginServer=false
// disables mcpHandlers entirely).
//
// Tests inject a spy via externalRebuilderForTest (see api_test.go helpers).
func (a *API) resolveExternalServerRebuilder() externalServerRebuilder {
	if a.externalRebuilderForTest != nil {
		return a.externalRebuilderForTest
	}
	if a.mcpHandlers == nil {
		return nil
	}
	if rb, ok := any(a.mcpHandlers).(externalServerRebuilder); ok {
		return rb
	}
	return nil
}

// handleMCPRegister handles POST /bridge/v1/mcp/register.
//
// Called by source plugins via mcphelper.Server.Register() (Phase 1C-6). The
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
	// Enabled and ExposeExternal are admin-owned after first registration;
	// they are toggled via PUT /admin/mcp/plugin-servers/:pluginID. If a
	// plugin re-registers (e.g. OnActivate after a crash or plugin upgrade),
	// we must NOT let the plugin's self-declared defaults overwrite what the
	// admin already set. Identity fields (PluginID/Name/Path) are owned by
	// the plugin and ARE refreshed from the new request.
	//
	// On first registration (no existing entry): use the plugin's cfg as-is.
	// This lets first-party plugins opt into ExposeExternal by default on
	// install; subsequent admin edits take precedence.
	if existing, found := a.mcpClientManager.GetPluginServer(cfg.PluginID); found {
		cfg.Enabled = existing.Enabled
		cfg.ExposeExternal = existing.ExposeExternal
	}
	a.mcpClientManager.RegisterPluginServer(cfg)

	// Live-update external MCP aggregation if requested. Pre-1G this is a
	// no-op (resolveExternalServerRebuilder returns nil); post-1G it swaps
	// the external *mcp.Server so external clients see the new tools without
	// restarting the plugin.
	if cfg.ExposeExternal {
		if rb := a.resolveExternalServerRebuilder(); rb != nil {
			rb.RebuildExternalServer()
		}
	}

	c.Status(http.StatusOK)
}

// handleMCPUnregister handles POST /bridge/v1/mcp/unregister.
//
// Called synchronously by source plugins from OnDeactivate (Phase 1C-6
// mcphelper.Server.Unregister). Body is {"plugin_id": "..."}.
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

	// Always trigger rebuild on unregister — the unregistered plugin's tools
	// must disappear from the external surface regardless of what its
	// ExposeExternal flag was. Pre-1G this is a no-op; post-1G it strips
	// the stale proxy tools. Cheap operation (rebuild is O(enabled plugin
	// servers) with no persistent external sessions to disrupt per
	// StreamableHTTPOptions{Stateless: true} — see Phase 1G-3 plan).
	if rb := a.resolveExternalServerRebuilder(); rb != nil {
		rb.RebuildExternalServer()
	}

	c.Status(http.StatusOK)
}
