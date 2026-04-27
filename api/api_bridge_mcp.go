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

// externalServerRebuilder lets bridge handlers refresh the external MCP
// aggregate when plugin servers change.
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
// or nil if none is available.
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
// Source plugins call this via mcphelper.Server.Register(). The route is
// protected by interPluginAuthorizationRequired, and the handler also requires
// the body PluginID to match Mattermost-Plugin-ID.
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

	// Prevent one authenticated plugin from registering a server under another
	// plugin's identity.
	callerPluginID := c.GetHeader("Mattermost-Plugin-ID")
	if cfg.PluginID != callerPluginID {
		c.JSON(http.StatusForbidden, bridgeclient.ErrorResponse{
			Error: "plugin_id does not match Mattermost-Plugin-ID header",
		})
		return
	}

	// Preserve admin-managed fields across re-registration. Source plugin
	// payloads carry identity, not admin state, so prefer the in-memory entry
	// and fall back to persisted config after unregister/register cycles.
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

	// Live-update external aggregation when this server is externally exposed.
	if cfg.ExposeExternal {
		if rb := a.resolveExternalServerRebuilder(); rb != nil {
			rb.RebuildExternalServer()
		}
	}

	c.Status(http.StatusOK)
}

// handleMCPUnregister handles POST /bridge/v1/mcp/unregister.
//
// Source plugins call this from OnDeactivate. The body PluginID must match the
// authenticated Mattermost-Plugin-ID header.
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

	// Always rebuild on unregister so stale proxy tools disappear.
	if rb := a.resolveExternalServerRebuilder(); rb != nil {
		rb.RebuildExternalServer()
	}

	c.Status(http.StatusOK)
}

// findPersistedPluginServer recovers admin-owned state when a plugin
// unregister/register cycle wiped the in-memory entry before ReInit hydrated it.
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
