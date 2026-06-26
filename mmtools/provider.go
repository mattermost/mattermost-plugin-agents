// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
)

// ToolProvider provides built-in tools for the AI assistant. The context is
// consulted for catalog inputs such as whether the requesting user is
// interactively present (llm.ToolCatalogContext).
type ToolProvider interface {
	GetTools(bot *bots.Bot, llmContext *llm.Context) []llm.Tool
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
//
// The exception is user-interaction tools: advertising them where nobody can
// answer would strand the conversation, so they are only cataloged when the
// context says an interactive user is present.
func (p *MMToolProvider) GetTools(bot *bots.Bot, llmContext *llm.Context) []llm.Tool {
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

	if llmContext != nil && llmContext.ToolCatalog.InteractiveUserPresent {
		builtInTools = append(builtInTools, NewAskUserQuestionTool())
	}

	return builtInTools
}

func hasNativeWebSearch(bot *bots.Bot) bool {
	if bot == nil {
		return false
	}

	return bot.HasNativeWebSearchEnabled()
}
