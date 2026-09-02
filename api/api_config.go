// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
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
// PUT /admin/config
func (a *API) handleSaveConfig(c *gin.Context) {
	var cfg config.Config
	if err := c.BindJSON(&cfg); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	cfg = normalizeAdminConfig(cfg)

	// Duplicate MCP server names or endpoints are rejected here rather than at
	// activation: names key the per-user client map, the shared tools cache,
	// and stored OAuth grants, so colliding entries silently shadow each other.
	if err := cfg.MCP.Validate(); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid MCP configuration: %w", err))
		return
	}

	// Audit which top-level config sections change — never their values,
	// since services/webSearch/mcp carry credentials. Best effort: a failed
	// read of the prior config must not block the save, so the record then
	// simply omits changed_keys.
	if rec := auditRec(c); rec != nil {
		if prev, err := a.configStore.GetConfig(); err == nil {
			audit.AddParam(rec, "changed_keys", audit.ChangedJSONKeys(prev, cfg))
		}
	}

	if err := a.configStore.SaveConfig(cfg); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save config: %w", err))
		return
	}

	// From here on the config HAS changed in the database. If a later step
	// fails (cluster notify), the audit record's fail status would otherwise
	// hide a real mutation — mark it explicitly.
	audit.AddParam(auditRec(c), "persisted", true)

	// Update in-memory config on this node
	a.configUpdater.Update(&cfg)

	// Notify other cluster nodes to reload config from DB
	if err := a.clusterNotifier.PublishConfigUpdate(); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to notify cluster of config update: %w", err))
		return
	}

	c.Status(http.StatusOK)
}
