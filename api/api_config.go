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
	if cfg.Services != nil {
		cfg.Services = append([]llm.ServiceConfig{}, cfg.Services...)
	}

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
		normalized := normalizeAdminConfig(config.Config{
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
		})
		c.JSON(http.StatusOK, config.RedactSecrets(normalized))
		return
	}

	normalized := normalizeAdminConfig(*cfg)
	c.JSON(http.StatusOK, config.RedactSecrets(normalized))
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

	var storedForAudit *config.Config
	saved, err := a.configStore.UpdateConfig(func(stored *config.Config) (config.Config, error) {
		storedForAudit = stored
		if stored == nil {
			return config.RestoreSecrets(cfg, config.Config{}), nil
		}
		return config.RestoreSecrets(cfg, *stored), nil
	})
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save config: %w", err))
		return
	}

	// Audit which top-level config sections changed, never their values.
	audit.AddParam(auditRec(c), "changed_keys", audit.ChangedJSONKeys(storedForAudit, saved))

	// From here on the config HAS changed in the database. If a later step
	// fails (cluster notify), the audit record's fail status would otherwise
	// hide a real mutation — mark it explicitly.
	audit.AddParam(auditRec(c), "persisted", true)

	// Update in-memory config on this node
	a.configUpdater.Update(&saved)

	// Notify other cluster nodes to reload config from DB
	if err := a.clusterNotifier.PublishConfigUpdate(); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to notify cluster of config update: %w", err))
		return
	}

	c.Status(http.StatusOK)
}
