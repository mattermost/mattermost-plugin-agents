// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

const legacyConfigBotsMigratedKey = "legacy_config_bots_migrated"

// migrateLegacyConfigBotsToUserAgents copies config-defined bots into Agents_UserAgents once,
// then removes them from stored plugin config to avoid duplicate bot registration in EnsureBots.
// Returns true if migration was actually performed (agents were created), false if already done or no-op.
func migrateLegacyConfigBotsToUserAgents(api plugin.API, pluginAPI *pluginapi.Client, st *store.Store, cfg *config.Container) (bool, error) {
	mtx, err := cluster.NewMutex(api, "ai_legacy_bots_migration")
	if err != nil {
		return false, fmt.Errorf("failed to create legacy bot migration mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	done, err := st.GetSystemValue(legacyConfigBotsMigratedKey)
	if err != nil {
		return false, fmt.Errorf("failed to read migration flag: %w", err)
	}
	if done == "true" {
		return false, nil
	}

	dbCfg, err := st.GetConfig()
	if err != nil {
		return false, fmt.Errorf("failed to load config: %w", err)
	}
	if dbCfg == nil || len(dbCfg.Bots) == 0 {
		// Do not mark migration complete when there are no config bots yet.
		// Plugin enable can run before initial admin config is applied (e.g. e2e installs),
		// and bots may arrive on a later configuration update.
		return false, nil
	}

	existingAgents, err := st.ListAgents()
	if err != nil {
		return false, fmt.Errorf("failed to list agents: %w", err)
	}
	byUsername := make(map[string]struct{}, len(existingAgents))
	for _, a := range existingAgents {
		byUsername[a.Name] = struct{}{}
	}

	previousMMBots, err := pluginAPI.Bot.List(0, 1000, pluginapi.BotOwner("mattermost-ai"), pluginapi.BotIncludeDeleted())
	if err != nil {
		return false, fmt.Errorf("failed to list mattermost bots: %w", err)
	}
	mmByUsername := make(map[string]string)
	for _, b := range previousMMBots {
		if b.DeleteAt == 0 {
			mmByUsername[b.Username] = b.UserId
		}
	}

	// If any legacy config bot still needs migration but has no Mattermost bot row,
	// defer the entire migration: do not create partial agents, wipe config, or set the flag.
	for _, bc := range dbCfg.Bots {
		if bc.Name == "" {
			continue
		}
		if _, ok := byUsername[bc.Name]; ok {
			continue
		}
		if _, ok := mmByUsername[bc.Name]; !ok {
			// Soft defer (not an error): activation can run before EnsureBots creates MM bot rows,
			// or the bot list may not yet include this owner. We retry on config updates.
			pluginAPI.Log.Warn("Deferring legacy bot migration: Mattermost bot not found", "username", bc.Name)
			return false, nil
		}
	}

	for _, bc := range dbCfg.Bots {
		if bc.Name == "" {
			continue
		}
		if _, ok := byUsername[bc.Name]; ok {
			continue
		}
		botUserID := mmByUsername[bc.Name]

		ua := bc

		// Reset identity/lifecycle so store.CreateAgent assigns a new ID & timestamps.
		ua.ID = ""
		ua.BotUserID = botUserID
		ua.CreatorID = "" // migrated legacy bot has no owner
		ua.AdminUserIDs = nil
		ua.CreateAt = 0
		ua.UpdateAt = 0
		ua.DeleteAt = 0

		// Config bots predate per-agent MCP tool gating — they had access to every
		// MCP tool. Preserve that by auto-enabling new MCP tools for migrated agents.
		ua.AutoEnableNewMCPTools = true
		ua.EnabledMCPTools = nil

		if createErr := st.CreateAgent(&ua); createErr != nil {
			return false, fmt.Errorf("failed to create user agent for legacy bot %q: %w", bc.Name, createErr)
		}
		byUsername[bc.Name] = struct{}{}
	}

	newCfg := *dbCfg
	newCfg.Bots = nil
	if saveErr := st.SaveConfig(newCfg); saveErr != nil {
		return false, fmt.Errorf("failed to save config after legacy bot migration: %w", saveErr)
	}
	reloaded, err := st.GetConfig()
	if err != nil {
		return false, fmt.Errorf("failed to reload config: %w", err)
	}
	if reloaded != nil {
		if err := cfg.StorePersistedConfigWithoutNotify(reloaded); err != nil {
			return false, fmt.Errorf("failed to store config after legacy bot migration: %w", err)
		}
	}

	if err := st.SetSystemValue(legacyConfigBotsMigratedKey, "true"); err != nil {
		return false, fmt.Errorf("failed to set migration flag: %w", err)
	}

	pluginAPI.Log.Info("Migrated legacy config bots to self-service agents table")
	return true, nil
}
