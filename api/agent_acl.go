// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// canManageAgent reports whether userID may update or delete cfg: agent admin,
// canManageOthersAgents, or (agent with empty CreatorID) PermissionManageSystem
// for migrated legacy bots.
func (a *API) canManageAgent(cfg *llm.BotConfig, userID string) bool {
	if cfg == nil {
		return false
	}
	if cfg.IsAdmin(userID) {
		return true
	}
	if a.canManageOthersAgents(userID) {
		return true
	}
	if cfg.CreatorID == "" && a.pluginAPI.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		return true
	}
	return false
}

// canManageOthersAgents reports whether userID may manage every agent: the
// manage_others_agent permission, or the agent managers list a system admin
// keeps in the System Console, which grants the same to individual users.
func (a *API) canManageOthersAgents(userID string) bool {
	if userID == "" {
		return false
	}
	if a.pluginAPI.User.HasPermissionTo(userID, model.PermissionManageOthersAgent) {
		return true
	}
	return a.config.SettingsAccess().ManagesAllAgents(userID)
}

// canManageServiceAccounts reports whether userID may turn on service account
// authentication for an agent, or change its access and MCP grants while it is
// on: system admins and read/write delegates, who already set the MCP servers'
// service account credentials in the Agents settings.
func (a *API) canManageServiceAccounts(userID string) bool {
	return a.settingsAccessLevel(userID) == config.SettingsAccessWrite
}

// serviceAccountPolicyWriteNeedsAdmin reports whether mutating cfg's access
// policy requires canManageServiceAccounts. Policy PUT/DELETE are access grants, which
// serviceAccountChangeNeedsAdmin already treats as admin-only while SA stays
// on; the policy lives outside BotConfig so this is the equivalent.
func serviceAccountPolicyWriteNeedsAdmin(cfg *llm.BotConfig) bool {
	return cfg != nil && cfg.UseServiceAccountAuth
}

// canCreateAgent returns true if the user may create new agents via POST /agents.
func canCreateAgent(client *pluginapi.Client, userID string) bool {
	if client.User.HasPermissionTo(userID, model.PermissionManageOwnAgent) {
		return true
	}
	return client.User.HasPermissionTo(userID, model.PermissionManageSystem)
}

// isSystemAdmin reports whether userID has PermissionManageSystem.
func isSystemAdmin(client *pluginapi.Client, userID string) bool {
	return client.User.HasPermissionTo(userID, model.PermissionManageSystem)
}

// canConfigureAgentServices reports whether userID may list services or fetch models (ManageOwnAgent, canManageOthersAgents, or ManageSystem).
func (a *API) canConfigureAgentServices(userID string) bool {
	if a.pluginAPI.User.HasPermissionTo(userID, model.PermissionManageOwnAgent) {
		return true
	}
	if a.canManageOthersAgents(userID) {
		return true
	}
	return a.pluginAPI.User.HasPermissionTo(userID, model.PermissionManageSystem)
}
