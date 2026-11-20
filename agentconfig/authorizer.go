// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentconfig

import (
	"cmp"
	"context"
	"reflect"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// Authorizer decides who may change an agent. The Service consults it on
// every write, so an entry point that acts for a user (an HTTP route, a tool
// call) is re-checked at the moment of the write.
type Authorizer interface {
	// CanManage reports whether actorID may change agent at all.
	CanManage(ctx context.Context, actorID string, agent *llm.BotConfig) bool
	// CanManageServiceAccounts reports whether actorID may turn service
	// account authentication on for agent, or change its access and MCP
	// grants while it is on.
	CanManageServiceAccounts(ctx context.Context, actorID string, agent *llm.BotConfig) bool
}

// ClearManagerEditableFields zeroes the fields any agent manager may change
// while service account auth stays on. Every other field is sensitive by
// default: a field added to BotConfig later is admin-only until it is listed
// here.
func ClearManagerEditableFields(cfg *llm.BotConfig) {
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
	cfg.ScopedExtraChannelIDs = sortedOrNil(cfg.ScopedExtraChannelIDs)
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

// ServiceAccountChangeNeedsAdmin reports whether moving stored to proposed
// requires Authorizer.CanManageServiceAccounts: enabling service account
// auth, or changing access or MCP grants while it stays on. Turning it off is
// always allowed. Agent managers may still change the fields cleared by
// ClearManagerEditableFields while it stays on.
func ServiceAccountChangeNeedsAdmin(stored, proposed llm.BotConfig) bool {
	if !proposed.UseServiceAccountAuth {
		return false
	}
	if !stored.UseServiceAccountAuth {
		return true
	}
	ClearManagerEditableFields(&stored)
	ClearManagerEditableFields(&proposed)
	return !reflect.DeepEqual(stored, proposed)
}
