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

// migrateAgentStructuredOutputToServicePolicy carries the removed per-agent
// structured output toggle over to the service policy that replaced it, so an
// install that had it enabled keeps sending native JSON schemas instead of
// silently dropping to the prompt fallback.
//
// It needs no completion flag: the migration only fills in services whose policy
// is unset (see config.MigrateServiceStructuredOutputPolicies), so it is a no-op
// on every activation after the first one that had something to do.
func migrateAgentStructuredOutputToServicePolicy(api plugin.API, pluginAPI *pluginapi.Client, st *store.Store, cfg *config.Container) error {
	mtx, err := cluster.NewMutex(api, "ai_agent_structured_output_migration")
	if err != nil {
		return fmt.Errorf("failed to create structured output migration mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	dbCfg, err := st.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if dbCfg == nil || len(dbCfg.Services) == 0 {
		return nil
	}

	agents, err := st.ListAgents()
	if err != nil {
		return fmt.Errorf("failed to list agents: %w", err)
	}

	migrated := config.MigrateServiceStructuredOutputPolicies(dbCfg, agents)
	if len(migrated) == 0 {
		return nil
	}

	if saveErr := st.SaveConfig(*dbCfg); saveErr != nil {
		return fmt.Errorf("failed to save config after structured output migration: %w", saveErr)
	}
	reloaded, err := st.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}
	if reloaded != nil {
		if storeErr := cfg.StorePersistedConfigWithoutNotify(reloaded); storeErr != nil {
			return fmt.Errorf("failed to store config after structured output migration: %w", storeErr)
		}
	}

	pluginAPI.Log.Info("Migrated deprecated agent structured output to a native service policy", "service_ids", migrated)
	return nil
}
