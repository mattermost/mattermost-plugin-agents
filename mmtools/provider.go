// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"database/sql"
	"net/http"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/mmtools/types"
	"github.com/mattermost/mattermost-plugin-ai/search"
	"github.com/mattermost/mattermost-plugin-ai/semanticcache"
	"github.com/mattermost/mattermost/server/public/model"
)

// ToolProvider provides built-in tools for the AI assistant
type ToolProvider interface {
	GetTools(isDM bool, bot *bots.Bot) []llm.Tool
}

// RoleToolProvider defines the interface for role-specific tool providers
type RoleToolProvider interface {
	// GetToolDefinitions returns all tools for this role
	GetToolDefinitions() []types.ToolDefinition
	// MatchesBot returns true if this provider should be used for the given bot
	MatchesBot(bot *bots.Bot) bool
	// GetToolMetadata returns metadata for a specific tool (empty metadata + false if not found)
	GetToolMetadata(toolName string) (types.ToolMetadata, bool)
	// GetSupportedDataSources returns data sources supported by a tool (empty slice if not found)
	GetSupportedDataSources(toolName string) []string
}

// MMToolProvider implements ToolProvider with all built-in Mattermost tools
type MMToolProvider struct {
	pluginAPI       mmapi.Client
	search          *search.Search
	httpClient      *http.Client
	vectorCache     *semanticcache.SimpleCache
	configContainer *config.Container // Kept for Jira credentials lookup only
	roleProviders   []RoleToolProvider
}

// NewMMToolProvider creates a new tool provider
// Role-specific providers should be registered via RegisterRole after creation
func NewMMToolProvider(pluginAPI mmapi.Client, search *search.Search, httpClient *http.Client, configContainer *config.Container, db *sql.DB) *MMToolProvider {
	return &MMToolProvider{
		pluginAPI:       pluginAPI,
		search:          search,
		httpClient:      httpClient,
		configContainer: configContainer,
		vectorCache:     semanticcache.NewSimpleCache(db),
		roleProviders:   make([]RoleToolProvider, 0),
	}
}

// GetVectorCache returns the vector cache for use by role providers
func (p *MMToolProvider) GetVectorCache() *semanticcache.SimpleCache {
	return p.vectorCache
}

// RegisterRole registers a role-specific tool provider
func (p *MMToolProvider) RegisterRole(provider RoleToolProvider) {
	p.roleProviders = append(p.roleProviders, provider)
}

// getCoreToolDefinitions returns core Mattermost tools (non-role-specific)
func (p *MMToolProvider) getCoreToolDefinitions() []types.ToolDefinition {
	var toolDefs []types.ToolDefinition

	// Safe tools (available everywhere)
	if p.search != nil && p.search.Enabled() {
		toolDefs = append(toolDefs, types.ToolDefinition{
			Tool: llm.Tool{
				Name:        "SearchServer",
				Description: "Search the Mattermost chat server the user is on for messages using semantic search. Use this tool whenever the user asks a question and you don't have the context to answer or you think your response would be more accurate with knowledge from the Mattermost server",
				Schema:      llm.NewJSONSchemaFromStruct[SearchServerArgs](),
				Resolver:    p.toolSearchServer,
			},
			SafeOnly: true,
		})
	}

	// GitHub tools
	if p.pluginAPI != nil {
		status, err := p.pluginAPI.GetPluginStatus("github")
		if err == nil && status != nil && status.State == model.PluginStateRunning {
			toolDefs = append(toolDefs, types.ToolDefinition{
				Tool: llm.Tool{
					Name:        "GetGithubIssue",
					Description: "Retrieve a single GitHub issue by owner, repo, and issue number.",
					Schema:      llm.NewJSONSchemaFromStruct[GetGithubIssueArgs](),
					Resolver:    p.toolGetGithubIssue,
				},
				SafeOnly: true,
			})
		}

		// User lookup tool (private data, DM only)
		toolDefs = append(toolDefs, types.ToolDefinition{
			Tool: llm.Tool{
				Name:        "LookupMattermostUser",
				Description: "Lookup a Mattermost user by their username. Available information includes: username, full name, email, nickname, position, locale, timezone, last activity, and status.",
				Schema:      llm.NewJSONSchemaFromStruct[LookupMattermostUserArgs](),
				Resolver:    p.toolResolveLookupMattermostUser,
			},
			SafeOnly: false,
		})
	}

	return toolDefs
}

// getAllToolDefinitions returns all available tools with their safety flags
func (p *MMToolProvider) getAllToolDefinitions(bot *bots.Bot) []types.ToolDefinition {
	// Start with core tools that are available to all bots
	toolDefs := p.getCoreToolDefinitions()

	// Add role-specific tools from registered providers
	if bot != nil {
		for _, roleProvider := range p.roleProviders {
			if roleProvider.MatchesBot(bot) {
				// Add role-specific tool definitions
				toolDefs = append(toolDefs, roleProvider.GetToolDefinitions()...)
			}
		}
	}

	return toolDefs
}

// GetTools returns the available tools based on context
func (p *MMToolProvider) GetTools(isDM bool, bot *bots.Bot) []llm.Tool {
	allToolDefs := p.getAllToolDefinitions(bot)
	var builtInTools []llm.Tool

	for _, toolDef := range allToolDefs {
		// Include safe tools everywhere, unsafe tools only in DMs
		if toolDef.SafeOnly || isDM {
			builtInTools = append(builtInTools, toolDef.Tool)
		}
	}

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

	return builtInTools
}

// GetToolMetadata returns metadata for a specific tool by querying registered role providers
func (p *MMToolProvider) GetToolMetadata(toolName string) (types.ToolMetadata, bool) {
	// Query each registered role provider
	for _, roleProvider := range p.roleProviders {
		if metadata, ok := roleProvider.GetToolMetadata(toolName); ok {
			return metadata, true
		}
	}
	// Not found in any provider
	return types.ToolMetadata{}, false
}

// GetSupportedDataSources returns all data sources supported by a tool
func (p *MMToolProvider) GetSupportedDataSources(toolName string) []string {
	// Query each registered role provider
	for _, roleProvider := range p.roleProviders {
		if sources := roleProvider.GetSupportedDataSources(toolName); len(sources) > 0 {
			return sources
		}
	}
	// Not found in any provider
	return nil
}
