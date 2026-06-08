// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
)

// ToolProvider provides built-in tools for the AI assistant
type ToolProvider interface {
	GetTools(bot *bots.Bot) []llm.Tool
}

// MMToolProvider implements ToolProvider with all built-in Mattermost tools
type MMToolProvider struct {
	pluginAPI mmapi.Client
	webSearch WebSearchService
}

// NewMMToolProvider creates a new tool provider
func NewMMToolProvider(pluginAPI mmapi.Client, webSearch WebSearchService) *MMToolProvider {
	return &MMToolProvider{
		pluginAPI: pluginAPI,
		webSearch: webSearch,
	}
}

// GetTools returns all available tools. Tool execution is restricted at runtime via
// WithToolsDisabled() based on context (e.g., DM vs channel). This allows LLMs to be
// aware of tool capabilities even when they can't be executed in the current context.
func (p *MMToolProvider) GetTools(bot *bots.Bot) []llm.Tool {
	builtInTools := []llm.Tool{}

	if p.pluginAPI != nil && p.webSearch != nil && !hasNativeWebSearch(bot) {
		tool := p.webSearch.Tool()
		if tool != nil {
			builtInTools = append(builtInTools, *tool)
		}

		if sourceTool := p.webSearch.SourceTool(bot); sourceTool != nil {
			builtInTools = append(builtInTools, *sourceTool)
		}
	}

	return builtInTools
}

func hasNativeWebSearch(bot *bots.Bot) bool {
	if bot == nil {
		return false
	}

	return bot.HasNativeWebSearchEnabled()
}
