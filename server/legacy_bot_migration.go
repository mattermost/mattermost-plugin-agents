// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/store"
	"github.com/mattermost/mattermost-plugin-ai/useragents"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

const legacyConfigBotsMigratedKey = "legacy_config_bots_migrated"

func mcpToolsToEnabled(tools []llm.EnabledMCPTool) []useragents.EnabledTool {
	if tools == nil {
		return nil
	}
	out := make([]useragents.EnabledTool, len(tools))
	for i, t := range tools {
		out[i] = useragents.EnabledTool{ServerOrigin: t.ServerOrigin, ToolName: t.ToolName}
	}
	return out
}

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
		byUsername[a.Username] = struct{}{}
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

	for _, bc := range dbCfg.Bots {
		if bc.Name == "" {
			continue
		}
		if _, ok := byUsername[bc.Name]; ok {
			continue
		}
		botUserID, ok := mmByUsername[bc.Name]
		if !ok {
			pluginAPI.Log.Warn("Skipping legacy bot migration: Mattermost bot not found", "username", bc.Name)
			continue
		}

		ua := &useragents.UserAgent{
			BotUserID:               botUserID,
			CreatorID:               "",
			DisplayName:             bc.DisplayName,
			Username:                bc.Name,
			ServiceID:               bc.ServiceID,
			CustomInstructions:      bc.CustomInstructions,
			ChannelAccessLevel:      int(bc.ChannelAccessLevel),
			ChannelIDs:              bc.ChannelIDs,
			UserAccessLevel:         int(bc.UserAccessLevel),
			UserIDs:                 bc.UserIDs,
			TeamIDs:                 bc.TeamIDs,
			AdminUserIDs:            nil,
			EnabledTools:            mcpToolsToEnabled(bc.EnabledMCPTools),
			Model:                   bc.Model,
			EnableVision:            bc.EnableVision,
			DisableTools:            bc.DisableTools,
			EnabledNativeTools:      bc.EnabledNativeTools,
			ReasoningEnabled:        bc.ReasoningEnabled,
			ReasoningEffort:         bc.ReasoningEffort,
			ThinkingBudget:          bc.ThinkingBudget,
			StructuredOutputEnabled: bc.StructuredOutputEnabled,
		}

		if err := st.CreateAgent(ua); err != nil {
			return false, fmt.Errorf("failed to create user agent for legacy bot %q: %w", bc.Name, err)
		}
		byUsername[bc.Name] = struct{}{}
	}

	newCfg := *dbCfg
	newCfg.Bots = nil
	if err := st.SaveConfig(newCfg); err != nil {
		return false, fmt.Errorf("failed to save config after legacy bot migration: %w", err)
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
