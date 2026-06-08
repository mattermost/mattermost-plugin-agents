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

// externalServerRebuilder rebuilds the external MCP aggregate after plugin changes.
type externalServerRebuilder interface {
	RebuildExternalServer()
}

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

// handleMCPRegister handles POST /bridge/v1/mcp/register using the authenticated
// Mattermost-Plugin-ID header.
func (a *API) handleMCPRegister(c *gin.Context) {
	var req struct {
		PluginID       string           `json:"plugin_id"`
		Name           string           `json:"name"`
		Path           string           `json:"path"`
		Enabled        *bool            `json:"enabled"`
		ExposeExternal bool             `json:"expose_external"`
		ToolConfigs    []mcp.ToolConfig `json:"tool_configs,omitempty"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}

	trustedPluginID := c.GetHeader("Mattermost-Plugin-ID")
	cfg := mcp.PluginServerConfig{
		PluginID:       trustedPluginID,
		Name:           req.Name,
		Path:           req.Path,
		ExposeExternal: req.ExposeExternal,
		ToolConfigs:    req.ToolConfigs,
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	} else {
		cfg.Enabled = true
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
	// PluginHTTP builds "/{pluginID}{path}", so the path must be absolute.
	if cfg.Path[0] != '/' {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "path must be absolute (start with '/')",
		})
		return
	}

	// Snapshot effective external exposure so we rebuild when it turns on or off.
	prevEffectiveExternal := a.pluginServerExternallyExposed(trustedPluginID)

	// Preserve Enabled and ToolConfigs across re-registration, even after unregister.
	// A first-time registration with no explicit enabled flag defaults to enabled.
	persisted, hasPersisted := a.findPersistedPluginServer(trustedPluginID)
	if existing, found := a.mcpClientManager.GetPluginServer(trustedPluginID); found {
		cfg.Enabled = existing.Enabled
		cfg.ToolConfigs = existing.ToolConfigs
	} else if hasPersisted {
		cfg.Enabled = persisted.Enabled
		cfg.ToolConfigs = persisted.ToolConfigs
	}
	a.mcpClientManager.RegisterPluginServer(cfg)

	newEffectiveExternal := cfg.Enabled && cfg.ExposeExternal
	if prevEffectiveExternal || newEffectiveExternal {
		if rb := a.resolveExternalServerRebuilder(); rb != nil {
			rb.RebuildExternalServer()
		}
	}

	c.Status(http.StatusOK)
}

// pluginServerExternallyExposed reports whether the plugin should appear on the
// external MCP server.
func (a *API) pluginServerExternallyExposed(pluginID string) bool {
	if existing, found := a.mcpClientManager.GetPluginServer(pluginID); found {
		return existing.Enabled && existing.ExposeExternal
	}
	if persisted, ok := a.findPersistedPluginServer(pluginID); ok {
		return persisted.Enabled && persisted.ExposeExternal
	}
	return false
}

// handleMCPUnregister handles POST /bridge/v1/mcp/unregister using the
// authenticated Mattermost-Plugin-ID header.
func (a *API) handleMCPUnregister(c *gin.Context) {
	var req struct{}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}

	a.mcpClientManager.UnregisterPluginServer(c.GetHeader("Mattermost-Plugin-ID"))

	// Always rebuild on unregister so stale proxy tools disappear.
	if rb := a.resolveExternalServerRebuilder(); rb != nil {
		rb.RebuildExternalServer()
	}

	c.Status(http.StatusOK)
}

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
