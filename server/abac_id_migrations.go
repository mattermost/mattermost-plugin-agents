// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// runABACIDMigrations runs the one-time ABAC identity migrations: legacy UUID
// service IDs are rewritten to model.NewId() values, and MCP servers (external,
// embedded, and plugin-registered) get stable IDs assigned. All run in a
// single idempotent store transaction, guarded by one cluster mutex. Returns
// whether the migration wrote to the DB. Callers must load config from the
// store afterwards — Migrated=false means another node already wrote, not that
// this process's memory is current.
func runABACIDMigrations(api plugin.API, pluginAPI *pluginapi.Client, st *store.Store) (bool, error) {
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
		pluginAPI.Log.Info("ABAC ID migrations applied",
			"services_remapped", report.ServicesRemapped,
			"agent_rows_updated", report.AgentRowsUpdated,
			"mcp_ids_assigned", report.MCPServerIDsAssigned,
			"embedded_plugin_mcp_ids_assigned", report.EmbeddedPluginServerIDsAssigned,
		)
	}
	for _, ref := range report.DanglingServiceRefs {
		pluginAPI.Log.Warn("Dangling service reference left unchanged by service ID migration", "reference", ref)
	}

	return report.Migrated, nil
}
