// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"cmp"
	"reflect"
	"slices"

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

// clearManagerEditableFields zeroes the fields any agent manager may change while
// service account auth stays on. Every other field is sensitive by default: the
// proposed config starts as a copy of the stored one, so a field added to
// applyAgentUpdateRequest later is admin-only until it is listed here.
func clearManagerEditableFields(cfg *llm.BotConfig) {
	cfg.Name = ""
	cfg.DisplayName = ""
	cfg.CustomInstructions = ""
	cfg.Model = ""
	cfg.ServiceID = ""
	cfg.EnableVision = false
	cfg.DisableTools = false
	cfg.EnabledNativeTools = nil
	cfg.MCPDynamicToolLoading = false
	cfg.ReasoningEnabled = false
	cfg.ReasoningEffort = ""
	cfg.ThinkingBudget = 0
	cfg.StructuredOutputEnabled = false //nolint:staticcheck // deprecated field is still persisted, so it must be normalized too
	cfg.MaxToolTurns = 0
	cfg.UseServiceAccountAuth = false
	cfg.Skills = llm.SkillsConfig{}

	// Access/MCP-grant ID collections are sets on the wire; order and nil-vs-empty are not changes.
	cfg.ChannelIDs = sortedOrNil(cfg.ChannelIDs)
	cfg.UserIDs = sortedOrNil(cfg.UserIDs)
	cfg.TeamIDs = sortedOrNil(cfg.TeamIDs)
	cfg.AdminUserIDs = sortedOrNil(cfg.AdminUserIDs)
	cfg.EnabledMCPTools = sortedToolsOrNil(cfg.EnabledMCPTools)
}

func sortedOrNil(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

func sortedToolsOrNil(s []llm.EnabledMCPTool) []llm.EnabledMCPTool {
	if len(s) == 0 {
		return nil
	}
	out := slices.Clone(s)
	slices.SortFunc(out, func(a, b llm.EnabledMCPTool) int {
		if c := cmp.Compare(a.ServerOrigin, b.ServerOrigin); c != 0 {
			return c
		}
		return cmp.Compare(a.ToolName, b.ToolName)
	})
	return out
}

// serviceAccountChangeNeedsAdmin reports whether moving stored to proposed requires
// canManageServiceAccounts: enabling service account auth, or changing Access / MCP grants
// while it stays on. Turning it off is always allowed. Agent managers may still
// change the fields cleared by clearManagerEditableFields while SA stays on.
func serviceAccountChangeNeedsAdmin(stored, proposed llm.BotConfig) bool {
	if !proposed.UseServiceAccountAuth {
		return false
	}
	if !stored.UseServiceAccountAuth {
		return true
	}
	clearManagerEditableFields(&stored)
	clearManagerEditableFields(&proposed)
	return !reflect.DeepEqual(stored, proposed)
}
