// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

// applyBotChannelAutoEverywhereToolFilter keeps only the tools
// botChannelAutoEverywhereKeepTool allows.
func (c *Conversations) applyBotChannelAutoEverywhereToolFilter(llmContext *llm.Context) {
	if llmContext == nil || llmContext.Tools == nil {
		return
	}
	llmContext.Tools.KeepToolsIf(func(tool llm.Tool) bool {
		return botChannelAutoEverywhereKeepTool(c.toolPolicyChecker, tool)
	})
}

// toolsDisabledLLMOptions returns the language-model options for a run whose
// tools are disabled: tools off, plus native web search when both the config
// and the bot allow it in channels. Nil when tools are enabled.
func (c *Conversations) toolsDisabledLLMOptions(bot *bots.Bot, toolsDisabled bool) []llm.LanguageModelOption {
	if !toolsDisabled {
		return nil
	}
	opts := []llm.LanguageModelOption{llm.WithToolsDisabled()}
	if c.configProvider != nil && c.configProvider.AllowNativeWebSearchInChannels() && bot.HasNativeWebSearchEnabled() && c.licenseChecker.Allows(enterprise.CapProviderWebSearch) {
		opts = append(opts, llm.WithNativeWebSearchAllowed())
	}
	return opts
}

// botChannelAutoEverywhereKeepTool reports whether a run nobody is present
// to approve may use tool: MCP meta-tools and auto-execute built-ins, which
// never need approval, and MCP tools whose enabled policy is
// auto_run_everywhere.
func botChannelAutoEverywhereKeepTool(checker mcp.ToolPolicyChecker, tool llm.Tool) bool {
	if mcp.IsMCPMetaTool(tool.Name) || isAutoExecuteBuiltIn(tool) {
		return true
	}
	if tool.ServerOrigin == "" {
		return false
	}
	policy, enabled := checker.GetToolPolicy(tool.ServerOrigin, llm.BareMCPToolName(tool.Name))
	return mcp.IsToolPolicyAutoRunEverywhere(policy) && enabled
}
