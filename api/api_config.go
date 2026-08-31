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
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

func normalizeAdminConfig(cfg config.Config) config.Config {
	cfg.MCP.Enabled = true
	cfg.MCP.EmbeddedServer.Enabled = true

	for i := range cfg.Services {
		if cfg.Services[i].Type == llm.ServiceTypeOpenAI {
			cfg.Services[i].UseResponsesAPI = true
		}
	}

	return cfg
}

// mintEmptyAdminIDs assigns a stable ID to every service and MCP server
// (external, embedded, plugin) that arrived without one. Empty IDs are
// creates; this runs on write only so GET cannot invent unpersisted identities.
func mintEmptyAdminIDs(cfg config.Config) config.Config {
	for i := range cfg.Services {
		if cfg.Services[i].ID == "" {
			cfg.Services[i].ID = model.NewId()
		}
	}
	for i := range cfg.MCP.Servers {
		if cfg.MCP.Servers[i].ID == "" {
			cfg.MCP.Servers[i].ID = model.NewId()
		}
	}
	if cfg.MCP.EmbeddedServer.ID == "" {
		cfg.MCP.EmbeddedServer.ID = model.NewId()
	}
	for i := range cfg.MCP.PluginServers {
		if cfg.MCP.PluginServers[i].ID == "" {
			cfg.MCP.PluginServers[i].ID = model.NewId()
		}
	}
	return cfg
}

// handleGetConfig returns the current plugin configuration from the database.
// GET /admin/config
func (a *API) handleGetConfig(c *gin.Context) {
	cfg, err := a.configStore.GetConfig()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get config: %w", err))
		return
	}

	if cfg == nil {
		c.JSON(http.StatusOK, normalizeAdminConfig(config.Config{
			Services: []llm.ServiceConfig{},
			Bots:     []llm.BotConfig{},
			MCP: mcp.Config{
				Enabled: true,
				Servers: []mcp.ServerConfig{},
				EmbeddedServer: mcp.EmbeddedServerConfig{
					Enabled: true,
				},
			},
			WebSearch: config.WebSearchConfig{
				DomainDenylist: []string{},
			},
		}))
		return
	}

	// Clone before normalizeAdminConfig: it mutates Services (e.g. UseResponsesAPI); the store
	// pointer may alias the in-memory cached config, and GET must not mutate shared state.
	c.JSON(http.StatusOK, normalizeAdminConfig(*cfg.Clone()))
}

// handleSaveConfig saves a new plugin configuration to the database,
// updates the in-memory configuration, and notifies other cluster nodes.
// It responds with the normalized saved config so clients can adopt
// server-minted service/MCP server IDs without a refetch.
// PUT /admin/config
func (a *API) handleSaveConfig(c *gin.Context) {
	var cfg config.Config
	if err := c.BindJSON(&cfg); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Read-previous → identity checks → mint empty IDs → save runs atomically
	// under the config advisory lock. Duplicate IDs and embedded ID mismatch
	// abort with 409. Empty IDs stay empty so mintEmptyAdminIDs assigns them
	// (the add-service/add-server path).
	var changedKeys []string
	saved, err := a.configStore.UpdateConfig(func(prev *config.Config) (config.Config, error) {
		next := cfg
		if err := config.ValidateServiceIDUniqueness(next.Services); err != nil {
			return config.Config{}, err
		}
		var prevMCP config.MCPConfig
		if prev != nil {
			prevMCP = prev.MCP
		}
		reconciledMCP, reconcileErr := config.ReconcileMCPConfigIDs(next.MCP, prevMCP)
		if reconcileErr != nil {
			return config.Config{}, reconcileErr
		}
		next.MCP = reconciledMCP
		normalized := mintEmptyAdminIDs(normalizeAdminConfig(next))
		changedKeys = audit.ChangedJSONKeys(prev, normalized)
		return normalized, nil
	})
	switch {
	case errors.Is(err, config.ErrServiceIDConflict), errors.Is(err, config.ErrMCPServerIDConflict):
		// Duplicate payload IDs, or an embedded server ID that does not match storage.
		c.AbortWithError(http.StatusConflict, fmt.Errorf("configuration payload has duplicate service or MCP server IDs, or an embedded server ID that does not match the stored identity: %w", err))
		return
	case errors.Is(err, store.ErrLegacyUUIDServiceID):
		// After the ABAC ID migration, a dashed UUID is an invalid service ID format.
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid service ID format: %w", err))
		return
	case err != nil:
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save config: %w", err))
		return
	}

	// From here on the config HAS changed in the database. If a later step
	// fails (cluster notify), the audit record's fail status would otherwise
	// hide a real mutation — mark it explicitly.
	audit.AddParam(auditRec(c), "changed_keys", changedKeys)
	audit.AddParam(auditRec(c), "persisted", true)

	// Update in-memory config on this node
	a.configUpdater.Update(&saved)

	// Notify other cluster nodes to reload config from DB
	if err := a.clusterNotifier.PublishConfigUpdate(); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to notify cluster of config update: %w", err))
		return
	}

	c.JSON(http.StatusOK, saved)
}
