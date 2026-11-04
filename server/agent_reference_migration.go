// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost/server/public/pluginapi"
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
// flag, and nodes racing at activation are serialized by UpdateConfig.
//
// It returns the stored config, nil when there is none, and whether it wrote
// it. Callers must publish the config to the cluster when migrated is true.
func migrateAgentReferencesToBotUserIDs(pluginAPI *pluginapi.Client, st agentReferenceConfigStore, resolver bots.UsernameResolver) (*config.Config, bool, error) {
	current, err := st.GetConfig()
	if err != nil {
		return nil, false, fmt.Errorf("failed to load config for agent reference migration: %w", err)
	}
	if current == nil {
		return nil, false, nil
	}

	resolved, unresolved := bots.ResolveAgentUsernames(resolver, current.LegacyAgentReferences())
	for _, username := range unresolved {
		pluginAPI.Log.Warn("Agent setting still selects an agent by username: no agent account holds it", "username", username)
	}
	if len(resolved) == 0 {
		return current, false, nil
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
		current, err = st.GetConfig()
		return current, false, err
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to save migrated agent references: %w", err)
	}

	pluginAPI.Log.Info("Migrated agent settings from usernames to bot user IDs", "count", len(resolved))
	return &saved, true, nil
}

// migrateAgentReferences runs migrateAgentReferencesToBotUserIDs and loads the
// stored agent settings on this node. When it wrote, peers are told to reload.
// When a peer migrated first, its config update event may have arrived while
// this plugin was still activating, which Mattermost drops, so the settings are
// loaded from the store either way. Failures are logged: an unmigrated setting
// keeps resolving by its legacy username.
func (p *Plugin) migrateAgentReferences(pluginAPI *pluginapi.Client, st agentReferenceConfigStore, resolver bots.UsernameResolver) {
	stored, migrated, err := migrateAgentReferencesToBotUserIDs(pluginAPI, st, resolver)
	if err != nil {
		pluginAPI.Log.Error("failed to migrate agent settings to bot user IDs", "error", err)
		return
	}
	if stored == nil {
		return
	}
	// Stored without notify: only agent references are taken, and they are
	// read live, so the update listeners have nothing to do.
	next := p.configuration.Config().Clone()
	if next.DefaultBotUserID != stored.DefaultBotUserID || next.DefaultBotName != stored.DefaultBotName ||
		next.TranscriptBotUserID != stored.TranscriptBotUserID || next.TranscriptGenerator != stored.TranscriptGenerator {
		next.DefaultBotUserID, next.DefaultBotName = stored.DefaultBotUserID, stored.DefaultBotName
		next.TranscriptBotUserID, next.TranscriptGenerator = stored.TranscriptBotUserID, stored.TranscriptGenerator
		if storeErr := p.configuration.StorePersistedConfigWithoutNotify(next); storeErr != nil {
			pluginAPI.Log.Error("failed to store config after agent reference migration", "error", storeErr)
		}
	}
	if !migrated {
		return
	}
	if pubErr := p.PublishConfigUpdate(); pubErr != nil {
		pluginAPI.Log.Error("Failed to publish config update after agent reference migration", "error", pubErr.Error())
	}
}
