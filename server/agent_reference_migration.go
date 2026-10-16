// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

var errNoAgentReferencesToMigrate = errors.New("no agent references to migrate")

type agentReferenceConfigStore interface {
	GetConfig() (*config.Config, error)
	UpdateConfig(transform func(prev *config.Config) (config.Config, error)) (config.Config, error)
}

// migrateAgentReferencesToBotUserIDs replaces the legacy username in the
// default and transcript agent settings with the bot user ID of the agent
// whose account currently holds that username, so a later rename cannot
// change which agent the setting selects. It must run after the bot registry
// is populated.
//
// A name no agent account holds is left in place, with a warning, and retried
// on the next activation; until then it keeps resolving by name. Settings that
// already hold an ID are never rewritten, so the migration needs no completion
// flag. Callers must store the returned config and publish it to the cluster
// when migrated is true.
func migrateAgentReferencesToBotUserIDs(api plugin.API, pluginAPI *pluginapi.Client, st agentReferenceConfigStore, resolver bots.UsernameResolver) (config.Config, bool, error) {
	mtx, err := cluster.NewMutex(api, "ai_agent_reference_migration")
	if err != nil {
		return config.Config{}, false, fmt.Errorf("failed to create agent reference migration mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	current, err := st.GetConfig()
	if err != nil {
		return config.Config{}, false, fmt.Errorf("failed to load config for agent reference migration: %w", err)
	}
	if current == nil {
		return config.Config{}, false, nil
	}

	resolved, unresolved := bots.ResolveAgentUsernames(resolver, current.LegacyAgentReferences())
	for _, username := range unresolved {
		pluginAPI.Log.Warn("Agent setting still selects an agent by username: no agent account holds it", "username", username)
	}
	if len(resolved) == 0 {
		return config.Config{}, false, nil
	}

	saved, err := st.UpdateConfig(func(prev *config.Config) (config.Config, error) {
		if prev == nil {
			return config.Config{}, errNoAgentReferencesToMigrate
		}
		next, migrated := prev.MigrateAgentReferences(resolved)
		if !migrated {
			return config.Config{}, errNoAgentReferencesToMigrate
		}
		return next, nil
	})
	if errors.Is(err, errNoAgentReferencesToMigrate) {
		return config.Config{}, false, nil
	}
	if err != nil {
		return config.Config{}, false, fmt.Errorf("failed to save migrated agent references: %w", err)
	}

	pluginAPI.Log.Info("Migrated agent settings from usernames to bot user IDs", "count", len(resolved))
	return saved, true, nil
}

// migrateAgentReferences runs migrateAgentReferencesToBotUserIDs and, when it
// wrote, loads the result and tells peers to reload. Failures are logged: an
// unmigrated setting keeps resolving by its legacy username.
func (p *Plugin) migrateAgentReferences(pluginAPI *pluginapi.Client, st agentReferenceConfigStore, resolver bots.UsernameResolver) {
	saved, migrated, err := migrateAgentReferencesToBotUserIDs(p.API, pluginAPI, st, resolver)
	if err != nil {
		pluginAPI.Log.Error("failed to migrate agent settings to bot user IDs", "error", err)
		return
	}
	if !migrated {
		return
	}
	// Stored without notify: only agent references changed, which are read
	// live, so the update listeners have nothing to do.
	if storeErr := p.configuration.StorePersistedConfigWithoutNotify(&saved); storeErr != nil {
		pluginAPI.Log.Error("failed to store config after agent reference migration", "error", storeErr)
	}
	if pubErr := p.PublishConfigUpdate(); pubErr != nil {
		pluginAPI.Log.Error("Failed to publish config update after agent reference migration", "error", pubErr.Error())
	}
}
