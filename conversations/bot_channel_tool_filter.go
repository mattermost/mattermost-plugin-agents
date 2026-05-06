// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/store"
)

// applyBotChannelAutoEverywhereToolFilter keeps only MCP tools whose policy is
// auto_run_everywhere and enabled. Built-in tools (empty ServerOrigin) are removed
// except for internal MCP meta-tools.
// Removed tools are recorded in DisabledToolsInfo for the model.
// When no policy checker is configured, fail closed for business tools so replayed
// posts cannot expose MCP tools without policy validation.
func (c *Conversations) applyBotChannelAutoEverywhereToolFilter(llmContext *llm.Context) {
	if llmContext == nil || llmContext.Tools == nil {
		return
	}
	if c.toolPolicyChecker == nil {
		removed := make([]llm.ToolInfo, 0)
		for _, tool := range llmContext.Tools.GetTools() {
			if mcp.IsMCPMetaTool(tool.Name) {
				continue
			}
			removed = append(removed, llm.ToolInfo{
				Name:        tool.Name,
				Description: tool.Description,
			})
		}
		llmContext.Tools.KeepToolsIf(func(tool llm.Tool) bool {
			return mcp.IsMCPMetaTool(tool.Name)
		})
		if len(removed) > 0 {
			// Replace any prior DisabledToolsInfo from applyToolAvailability with the tools removed here.
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
	if mcp.IsMCPMetaTool(tool.Name) {
		return true
	}
	if checker == nil {
		return false
	}
	if tool.ServerOrigin == "" {
		return false
	}
	policy, enabled := checker.GetToolPolicy(tool.ServerOrigin, llm.BareMCPToolName(tool.Name))
	return mcp.IsToolPolicyAutoRunEverywhere(policy) && enabled
}

func (c *Conversations) channelFollowUpMCPToolFilterContextOptions(isDM bool, conv *store.Conversation) ([]llm.ContextOption, bool) {
	if c.contextBuilder == nil || !c.shouldConstrainChannelFollowUpToAutoEverywhere(isDM, conv) {
		return nil, false
	}

	return []llm.ContextOption{
		c.contextBuilder.WithLLMContextMCPToolFilter(func(tool llm.Tool) bool {
			return botChannelAutoEverywhereKeepTool(c.toolPolicyChecker, tool)
		}),
	}, true
}

func (c *Conversations) shouldConstrainChannelFollowUpToAutoEverywhere(isDM bool, conv *store.Conversation) bool {
	if isDM || c.configProvider == nil || !c.configProvider.EnableChannelMentionToolCalling() {
		return false
	}

	// Channel follow-ups rebuild strict MCP registries before the follow-up
	// request is sent. If the root post cannot prove this was a normal
	// interactive channel flow, keep only auto-run-everywhere MCP tools.
	if conv == nil || conv.RootPostID == nil || c.mmClient == nil {
		return true
	}

	rootPost, rootErr := c.mmClient.GetPost(*conv.RootPostID)
	if rootErr != nil || rootPost == nil {
		return true
	}
	rootUser, userErr := c.mmClient.GetUser(rootPost.UserId)
	if userErr != nil {
		return true
	}

	return isBotActivateAI(rootPost, rootUser)
}
