// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// runABACIDMigrations runs the one-time ABAC identity migrations: legacy UUID
// service IDs are rewritten to model.NewId() values, and external MCP servers
// get stable IDs assigned. Both run in a single idempotent store transaction,
// guarded by one cluster mutex. Returns whether the migration wrote to the DB.
func runABACIDMigrations(api plugin.API, pluginAPI *pluginapi.Client, st *store.Store, cfg *config.Container) (bool, error) {
	mtx, err := cluster.NewMutex(api, "ai_abac_id_migration")
	if err != nil {
		return false, fmt.Errorf("failed to create ABAC ID migration mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	report, err := st.MigrateABACIDs()
	if err != nil {
		return false, fmt.Errorf("failed to migrate ABAC IDs: %w", err)
	}

	if report.Migrated {
		reloaded, reloadErr := st.GetConfig()
		if reloadErr != nil {
			return false, fmt.Errorf("failed to reload config after ID migrations: %w", reloadErr)
		}
		if reloaded != nil {
			if storeErr := cfg.StorePersistedConfigWithoutNotify(reloaded); storeErr != nil {
				return false, fmt.Errorf("failed to store config after ID migrations: %w", storeErr)
			}
		}
		pluginAPI.Log.Info("ABAC ID migrations applied",
			"services_remapped", report.ServicesRemapped,
			"agent_rows_updated", report.AgentRowsUpdated,
			"mcp_ids_assigned", report.MCPServerIDsAssigned,
		)
	}
	for _, ref := range report.DanglingServiceRefs {
		pluginAPI.Log.Warn("Dangling service reference left unchanged by service ID migration", "reference", ref)
	}

	return report.Migrated, nil
}
