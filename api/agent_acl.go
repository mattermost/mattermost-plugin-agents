// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"cmp"
	"reflect"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// canManageAgent reports whether userID may update or delete cfg: agent admin, PermissionManageOthersAgent,
// or (agent with empty CreatorID) PermissionManageSystem for migrated legacy bots.
func canManageAgent(client *pluginapi.Client, cfg *llm.BotConfig, userID string) bool {
	if cfg == nil {
		return false
	}
	if cfg.IsAdmin(userID) {
		return true
	}
	if client.User.HasPermissionTo(userID, model.PermissionManageOthersAgent) {
		return true
	}
	if cfg.CreatorID == "" && client.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		return true
	}
	return false
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

// canConfigureAgentServices reports whether userID may list services or fetch models (ManageOwnAgent, ManageOthersAgent, or ManageSystem).
func canConfigureAgentServices(client *pluginapi.Client, userID string) bool {
	if client.User.HasPermissionTo(userID, model.PermissionManageOwnAgent) {
		return true
	}
	if client.User.HasPermissionTo(userID, model.PermissionManageOthersAgent) {
		return true
	}
	return client.User.HasPermissionTo(userID, model.PermissionManageSystem)
}

// clearManagerEditableFields zeroes the fields any agent manager may change while
// service account auth stays on. Every other field is sensitive by default: the
// proposed config starts as a copy of the stored one, so a field added to
// applyAgentUpdateRequest later is admin-only until it is listed here.
func clearManagerEditableFields(cfg *llm.BotConfig) {
	cfg.DisplayName = ""
	cfg.CustomInstructions = ""
	cfg.Model = ""
	cfg.EnableVision = false
	cfg.ReasoningEnabled = false
	cfg.ReasoningEffort = ""
	cfg.ThinkingBudget = 0
	cfg.StructuredOutputEnabled = false
	cfg.MaxToolTurns = 0
	cfg.UseServiceAccountAuth = false

	// ID collections are sets on the wire; order and nil-vs-empty are not changes.
	cfg.ChannelIDs = sortedOrNil(cfg.ChannelIDs)
	cfg.UserIDs = sortedOrNil(cfg.UserIDs)
	cfg.TeamIDs = sortedOrNil(cfg.TeamIDs)
	cfg.AdminUserIDs = sortedOrNil(cfg.AdminUserIDs)
	cfg.EnabledNativeTools = sortedOrNil(cfg.EnabledNativeTools)
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
// manage_system: enabling service account auth, or changing an access/tool-reach/
// provider field while it stays on. Turning it off is always allowed. Agent managers
// may still change the fields cleared by clearManagerEditableFields while SA stays on.
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
