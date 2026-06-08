// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
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

	if err := a.configStore.SaveConfig(cfg); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save config: %w", err))
		return
	}

	// Update in-memory config on this node
	a.configUpdater.Update(&cfg)

	// Notify other cluster nodes to reload config from DB
	if err := a.clusterNotifier.PublishConfigUpdate(); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to notify cluster of config update: %w", err))
		return
	}

	c.Status(http.StatusOK)
}
