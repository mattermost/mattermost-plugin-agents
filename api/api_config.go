// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"
	"strings"

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
	// Credentials are masked in the response; the admin console sends the mask back unchanged
	// for the ones it does not edit.
	c.JSON(http.StatusOK, config.RedactSecrets(normalizeAdminConfig(*cfg.Clone())))
}

// handleSaveConfig saves a new plugin configuration to the database,
// updates the in-memory configuration, and notifies other cluster nodes.
// It responds with the saved config, credentials masked as in GET.
// PUT /admin/config
func (a *API) handleSaveConfig(c *gin.Context) {
	var cfg config.Config
	if err := c.BindJSON(&cfg); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	cfg = normalizeAdminConfig(cfg)

	prev, err := a.configStore.GetConfig()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get config: %w", err))
		return
	}

	// A masked credential means "keep the stored one"; any other value is what
	// the admin typed, including an empty one that clears it.
	cfg = config.RestoreSecrets(cfg, prev)
	if fields := config.MaskedCredentialFields(cfg); len(fields) > 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("the configuration was not saved because these credentials hold a mask with no stored value to restore: %s. Reload the System Console to load the current configuration, then re-enter them", strings.Join(fields, ", ")),
		})
		return
	}

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

	c.JSON(http.StatusOK, config.RedactSecrets(cfg))
}
