// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"net/http"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/search"
	"github.com/mattermost/mattermost/server/public/model"
)

// ToolProvider provides built-in tools for the AI assistant
type ToolProvider interface {
	GetTools(isDM bool, bot *bots.Bot) []llm.Tool
}

// MMToolProvider implements ToolProvider with all built-in Mattermost tools
type MMToolProvider struct {
	pluginAPI  mmapi.Client
	search     *search.Search
	httpClient *http.Client
}

// NewMMToolProvider creates a new tool provider
func NewMMToolProvider(pluginAPI mmapi.Client, search *search.Search, httpClient *http.Client) *MMToolProvider {
	return &MMToolProvider{
		pluginAPI:  pluginAPI,
		search:     search,
		httpClient: httpClient,
	}
}

// GetTools returns the available tools based on context
func (p *MMToolProvider) GetTools(isDM bool, bot *bots.Bot) []llm.Tool {
	builtInTools := []llm.Tool{}

	if isDM {
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

			// Add GitHub tool if plugin is available
			status, err := p.pluginAPI.GetPluginStatus("github")
			if err == nil && status != nil && status.State == model.PluginStateRunning {
				builtInTools = append(builtInTools, llm.Tool{
					Name:        "GetGithubIssue",
					Description: "Retrieve a single GitHub issue by owner, repo, and issue number.",
					Schema:      llm.NewJSONSchemaFromStruct[GetGithubIssueArgs](),
					Resolver:    p.toolGetGithubIssue,
				})
			}
		}

		// Add Jira tool if httpClient is available
		if p.httpClient != nil {
			builtInTools = append(builtInTools, llm.Tool{
				Name:        "GetJiraIssue",
				Description: "Retrieve a single Jira issue by issue key.",
				Schema:      llm.NewJSONSchemaFromStruct[GetJiraIssueArgs](),
				Resolver:    p.toolGetJiraIssue,
			})
		}
	}

	return builtInTools
}
