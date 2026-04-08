// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/search"
)

// ToolProvider provides built-in tools for the AI assistant
type ToolProvider interface {
	GetTools(bot *bots.Bot) []llm.Tool
}

// MMToolProvider implements ToolProvider with all built-in Mattermost tools
type MMToolProvider struct {
	pluginAPI mmapi.Client
	search    *search.Search
	webSearch WebSearchService
}

// NewMMToolProvider creates a new tool provider
func NewMMToolProvider(pluginAPI mmapi.Client, search *search.Search, webSearch WebSearchService) *MMToolProvider {
	return &MMToolProvider{
		pluginAPI: pluginAPI,
		search:    search,
		webSearch: webSearch,
	}
}

// GetTools returns all available tools. Tool execution is restricted at runtime via
// WithToolsDisabled() based on context (e.g., DM vs channel). This allows LLMs to be
// aware of tool capabilities even when they can't be executed in the current context.
func (p *MMToolProvider) GetTools(bot *bots.Bot) []llm.Tool {
	builtInTools := []llm.Tool{}

	// Add search tool if search service is available and enabled
	if p.search.Enabled() {
		builtInTools = append(builtInTools, llm.Tool{
			Name:        "SearchServer",
			Description: "Search the Mattermost chat server the user is on for messages using semantic search. Use this tool whenever the user asks a question and you don't have the context to answer or you think your response would be more accurate with knowledge from the Mattermost server",
			Schema:      llm.NewJSONSchemaFromStruct[SearchServerArgs](),
			Resolver:    p.toolSearchServer,
		})
	}

	// Add user lookup tool if pluginAPI is available
	if p.pluginAPI != nil {
		builtInTools = append(builtInTools, llm.Tool{
			Name:        "LookupMattermostUser",
			Description: "Lookup a Mattermost user by their username. Available information includes: username, full name, email, nickname, position, locale, timezone, last activity, and status.",
			Schema:      llm.NewJSONSchemaFromStruct[LookupMattermostUserArgs](),
			Resolver:    p.toolResolveLookupMattermostUser,
		})

		if p.webSearch != nil && !hasNativeWebSearch(bot) {
			tool := p.webSearch.Tool()
			if tool != nil {
				builtInTools = append(builtInTools, *tool)
			}

			if sourceTool := p.webSearch.SourceTool(bot); sourceTool != nil {
				builtInTools = append(builtInTools, *sourceTool)
			}
		}
	}

	return builtInTools
}

func hasNativeWebSearch(bot *bots.Bot) bool {
	if bot == nil {
		return false
	}

	for _, tool := range bot.GetConfig().EnabledNativeTools {
		if strings.EqualFold(tool, "web_search") {
			return true
		}
	}

	return false
}
