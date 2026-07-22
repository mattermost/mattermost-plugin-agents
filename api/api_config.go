// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

func normalizeAdminConfig(cfg config.Config) config.Config {
	cfg.MCP.Enabled = true
	cfg.MCP.EmbeddedServer.Enabled = true
	cfg.MCP.Apps.SandboxURL = strings.TrimSpace(cfg.MCP.Apps.SandboxURL)
	cfg.MCP.Apps.SandboxListenAddress = strings.TrimSpace(cfg.MCP.Apps.SandboxListenAddress)

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

	if err := cfg.MCP.Apps.Validate(); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid MCP Apps configuration: %w", err))
		return
	}

	cfg = normalizeAdminConfig(cfg)

	prevCfg, prevErr := a.configStore.GetConfig()
	if prevErr != nil {
		a.pluginAPI.Log.Warn("Failed to load previous config for MCP Apps audit comparison", "error", prevErr)
	}

	if err := a.configStore.SaveConfig(cfg); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save config: %w", err))
		return
	}

	// Update in-memory config on this node
	a.configUpdater.Update(&cfg)

	wasInsecure := prevCfg != nil && prevCfg.MCP.Apps.AllowInsecureSameOriginSandbox
	if !wasInsecure && cfg.MCP.Apps.AllowInsecureSameOriginSandbox {
		a.pluginAPI.Log.Warn(
			"MCP Apps: insecure same-origin sandbox fallback ENABLED — app content will execute on the Mattermost origin without iframe origin isolation",
			"actor_user_id", c.GetHeader("Mattermost-User-Id"),
		)
	}

	// Notify other cluster nodes to reload config from DB
	if err := a.clusterNotifier.PublishConfigUpdate(); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to notify cluster of config update: %w", err))
		return
	}

	c.Status(http.StatusOK)
}
