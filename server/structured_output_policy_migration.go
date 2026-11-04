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

// migrateAgentStructuredOutputToServicePolicy runs store.MigrateStructuredOutputPolicies
// under a cluster mutex, carrying the removed per-agent structured output toggle
// over to the service policy that replaced it so an install that had it enabled
// keeps sending native JSON schemas instead of silently dropping to the prompt
// fallback.
//
// It needs no completion flag: only services whose policy is unset are filled
// in, so it is a no-op on every activation after the first one that had
// something to do. Callers must reload config from the store afterwards —
// migrated=false means another node may already have written, not that this
// process's memory is current.
func migrateAgentStructuredOutputToServicePolicy(api plugin.API, pluginAPI *pluginapi.Client, st *store.Store) (config.Config, bool, error) {
	mtx, err := cluster.NewMutex(api, "ai_agent_structured_output_migration")
	if err != nil {
		return config.Config{}, false, fmt.Errorf("failed to create structured output migration mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	saved, migrated, err := st.MigrateStructuredOutputPolicies()
	if err != nil {
		return config.Config{}, false, err
	}
	if len(migrated) == 0 {
		return config.Config{}, false, nil
	}

	pluginAPI.Log.Info("Migrated deprecated agent structured output to a native service policy", "service_ids", migrated)
	return saved, true, nil
}
