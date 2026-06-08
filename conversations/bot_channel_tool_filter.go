// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
)

// applyBotChannelAutoEverywhereToolFilter keeps only MCP tools whose policy is
// auto_run_everywhere and enabled. Built-in tools (empty ServerOrigin) are removed.
// Removed tools are recorded in DisabledToolsInfo for the model.
// When no policy checker is configured, fail closed: remove all tools so replayed
// posts cannot expose MCP tools without policy validation.
func (c *Conversations) applyBotChannelAutoEverywhereToolFilter(llmContext *llm.Context) {
	if llmContext == nil || llmContext.Tools == nil {
		return
	}
	if c.toolPolicyChecker == nil {
		removed := llmContext.Tools.GetToolsInfo()
		llmContext.Tools.KeepToolsIf(func(tool llm.Tool) bool { return false })
		if len(removed) > 0 {
			// Replace any prior DisabledToolsInfo from applyToolAvailability: all tools are removed.
			llmContext.DisabledToolsInfo = removed
		}
		return
	}

	removed := make([]llm.ToolInfo, 0)
	for _, t := range llmContext.Tools.GetTools() {
		if botChannelAutoEverywhereKeepTool(c.toolPolicyChecker, t) {
			continue
		}
		removed = append(removed, llm.ToolInfo{
			Name:        t.Name,
			Description: t.Description,
		})
	}

	llmContext.Tools.KeepToolsIf(func(tool llm.Tool) bool {
		return botChannelAutoEverywhereKeepTool(c.toolPolicyChecker, tool)
	})

	if len(removed) > 0 {
		llmContext.DisabledToolsInfo = append(llmContext.DisabledToolsInfo, removed...)
	}
}

// applyToolAvailability decides whether tools should be disabled for a context based on
// whether the conversation is a DM or a channel mention with tools allowed. It returns
// true when tools are disabled. When disabled, it populates DisabledToolsInfo so the
// model can still reference the tool descriptions.
func applyToolAvailability(context *llm.Context, isDM bool, allowToolsInChannel bool) bool {
	toolsDisabled := !isDM && !allowToolsInChannel
	if context != nil {
		if toolsDisabled && context.Tools != nil {
			context.DisabledToolsInfo = context.Tools.GetToolsInfo()
		} else {
			context.DisabledToolsInfo = nil
		}
	}
	return toolsDisabled
}

func botChannelAutoEverywhereKeepTool(checker mcp.ToolPolicyChecker, tool llm.Tool) bool {
	if checker == nil {
		return false
	}
	if tool.ServerOrigin == "" {
		return false
	}
	policy, enabled := checker.GetToolPolicy(tool.ServerOrigin, tool.Name)
	return mcp.IsToolPolicyAutoRunEverywhere(policy) && enabled
}
