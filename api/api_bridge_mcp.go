// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/model"
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
	// Attribute the caller before anything can fail, so every audit fail
	// path carries it. The header is set by the Mattermost server for
	// inter-plugin requests and is the registered PluginID too, so one
	// parameter covers both the actor and the affected server.
	trustedPluginID := c.GetHeader("Mattermost-Plugin-ID")
	audit.AddParam(auditRec(c), audit.KeyCallerPluginID, audit.TruncateID(trustedPluginID))

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

	// Name and path are unvalidated caller text; clamp them. Whether tool
	// configs were sent is recorded — never the configs themselves.
	audit.AddParam(auditRec(c), "server_name", audit.TruncateID(req.Name))
	audit.AddParam(auditRec(c), "path", audit.TruncateID(req.Path))
	audit.AddParam(auditRec(c), "expose_external", req.ExposeExternal)
	audit.AddParam(auditRec(c), "tool_configs_provided", req.ToolConfigs != nil)

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

	// Overlay admin-owned fields (Enabled, ToolConfigs, ID). Live entry first,
	// then persisted config — so a re-register after unregister recovers from
	// config, while a live-only ID is not rotated when the config row is absent.
	if existing, found := a.mcpClientManager.GetPluginServer(trustedPluginID); found {
		cfg = mcp.ApplyPersistedPluginServerFields(cfg, existing)
	}
	persisted, hasPersisted := a.findPersistedPluginServer(trustedPluginID)
	if hasPersisted {
		cfg = mcp.ApplyPersistedPluginServerFields(cfg, persisted)
	}

	// Mint only when neither live nor persisted carried an ID. Persist when the
	// config row is missing or ID-less so a live-only identity is written.
	if cfg.ID == "" {
		cfg.ID = model.NewId()
	}
	if !hasPersisted || persisted.ID == "" {
		if err := a.persistPluginServerID(trustedPluginID, &cfg); err != nil {
			c.JSON(http.StatusInternalServerError, bridgeclient.ErrorResponse{
				Error: fmt.Sprintf("failed to persist plugin server ID: %v", err),
			})
			return
		}
	}

	// Effective final value after the preserve-on-reregister merge, not the
	// raw request flag.
	audit.AddParam(auditRec(c), "enabled", cfg.Enabled)

	a.mcpClientManager.RegisterPluginServer(cfg)

	newEffectiveExternal := cfg.Enabled && cfg.ExposeExternal
	if prevEffectiveExternal || newEffectiveExternal {
		if rb := a.resolveExternalServerRebuilder(); rb != nil {
			rb.RebuildExternalServer()
		}
	}

	c.Status(http.StatusOK)
}

// persistPluginServerID ensures cfg.MCP.PluginServers holds a stable ID for
// pluginID. Only mints when the entry is missing or ID-less; concurrent
// writers that already assigned an ID win (their ID is adopted onto
// registration).
func (a *API) persistPluginServerID(pluginID string, registration *mcp.PluginServerConfig) error {
	if a.configStore == nil || registration == nil {
		return nil
	}
	saved, err := a.configStore.UpdateConfig(func(prev *config.Config) (config.Config, error) {
		if prev == nil {
			return config.Config{}, errors.New("no plugin configuration available")
		}
		cfg := prev.Clone()
		for i := range cfg.MCP.PluginServers {
			if cfg.MCP.PluginServers[i].PluginID != pluginID {
				continue
			}
			if cfg.MCP.PluginServers[i].ID == "" {
				cfg.MCP.PluginServers[i].ID = uniquePluginServerID(cfg.MCP, registration.ID)
			}
			return *cfg, nil
		}
		id := uniquePluginServerID(cfg.MCP, registration.ID)
		cfg.MCP.PluginServers = append(cfg.MCP.PluginServers, config.PluginServerConfig{
			ID:             id,
			PluginID:       registration.PluginID,
			Name:           registration.Name,
			Path:           registration.Path,
			Enabled:        registration.Enabled,
			ExposeExternal: registration.ExposeExternal,
			ToolConfigs:    registration.ToolConfigs,
		})
		return *cfg, nil
	})
	if err != nil {
		return err
	}
	// Adopt the persisted ID in case another writer raced and minted first.
	for i := range saved.MCP.PluginServers {
		if saved.MCP.PluginServers[i].PluginID == pluginID && saved.MCP.PluginServers[i].ID != "" {
			registration.ID = saved.MCP.PluginServers[i].ID
			break
		}
	}
	if a.clusterNotifier != nil {
		if notifyErr := a.clusterNotifier.PublishConfigUpdate(); notifyErr != nil {
			return fmt.Errorf("failed to notify cluster of plugin-server ID: %w", notifyErr)
		}
	}
	if a.configUpdater != nil {
		a.configUpdater.Update(&saved)
	}
	return nil
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
	// Attribute the caller before anything can fail; the trusted header is
	// also the PluginID being unregistered.
	trustedPluginID := c.GetHeader("Mattermost-Plugin-ID")
	audit.AddParam(auditRec(c), audit.KeyCallerPluginID, audit.TruncateID(trustedPluginID))

	var req struct{}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}

	a.mcpClientManager.UnregisterPluginServer(trustedPluginID)

	// Always rebuild on unregister so stale proxy tools disappear.
	if rb := a.resolveExternalServerRebuilder(); rb != nil {
		rb.RebuildExternalServer()
	}

	c.Status(http.StatusOK)
}

// uniquePluginServerID keeps candidate when free across all MCP kinds; otherwise mints.
func uniquePluginServerID(mcpCfg config.MCPConfig, candidate string) string {
	occupied := config.OccupiedMCPServerIDs(mcpCfg)
	if candidate != "" {
		if _, taken := occupied[candidate]; !taken {
			return candidate
		}
	}
	for {
		id := model.NewId()
		if _, taken := occupied[id]; !taken {
			return id
		}
	}
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
