// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
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

	// Backstop for direct API automation and stale webapp bundles: every
	// persisted service and external MCP server must have a stable ID.
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

	// Read-previous → reconcile → normalize → save runs atomically under the
	// config advisory lock, so a concurrent save or migration cannot
	// interleave. Reconciliation carries stable MCP server IDs forward before
	// normalizeAdminConfig mints fresh ones, so payloads from clients that
	// drop the id field (stale webapp bundles, raw API automation) cannot
	// rotate IDs on every save; identity conflicts abort the save entirely.
	saved, err := a.configStore.UpdateConfig(func(prev *config.Config) (config.Config, error) {
		next := cfg
		if prev != nil {
			reconciled, reconcileErr := config.ReconcileMCPServerIDs(next.MCP.Servers, prev.MCP.Servers)
			if reconcileErr != nil {
				return config.Config{}, reconcileErr
			}
			next.MCP.Servers = reconciled
		}
		return normalizeAdminConfig(next), nil
	})
	switch {
	case errors.Is(err, config.ErrMCPServerIDConflict):
		// The payload's MCP server identities cannot be safely reconciled
		// with the stored config (stale or corrupt admin console state).
		// Minting fresh IDs here would silently detach existing policies.
		c.AbortWithError(http.StatusConflict, fmt.Errorf("configuration payload conflicts with the stored MCP server identities; reload the System Console and retry: %w", err))
		return
	case errors.Is(err, store.ErrStaleLegacyServiceIDs):
		// A pre-upgrade webapp bundle is echoing back UUID service IDs from
		// before the ID migration; writing them would undo it.
		c.AbortWithError(http.StatusConflict, fmt.Errorf("stale configuration payload; reload the System Console and retry: %w", err))
		return
	case err != nil:
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save config: %w", err))
		return
	}

	// Update in-memory config on this node
	a.configUpdater.Update(&saved)

	// Notify other cluster nodes to reload config from DB
	if err := a.clusterNotifier.PublishConfigUpdate(); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to notify cluster of config update: %w", err))
		return
	}

	c.Status(http.StatusOK)
}
