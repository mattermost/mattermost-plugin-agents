// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
)

func TestFilterToolsByConfig(t *testing.T) {
	tests := []struct {
		name          string
		config        Config
		pluginServers []PluginServerConfig
		rawTools      []llm.Tool
		wantToolNames []string
	}{
		{
			name: "enabled configured tool is returned",
			config: Config{
				Servers: []ServerConfig{
					{
						Name:    "Jira",
						Enabled: true,
						BaseURL: "https://mcp.atlassian.com",
						ToolConfigs: []ToolConfig{
							{Name: "getJiraIssue", Policy: ToolPolicyAsk, Enabled: true},
						},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "getJiraIssue", Description: "Get issue", ServerOrigin: "https://mcp.atlassian.com"},
			},
			wantToolNames: []string{"getJiraIssue"},
		},
		{
			name: "disabled configured tool is filtered out",
			config: Config{
				Servers: []ServerConfig{
					{
						Name:    "Jira",
						Enabled: true,
						BaseURL: "https://mcp.atlassian.com",
						ToolConfigs: []ToolConfig{
							{Name: "getJiraIssue", Policy: ToolPolicyAsk, Enabled: false},
						},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "getJiraIssue", Description: "Get issue", ServerOrigin: "https://mcp.atlassian.com"},
			},
		},
		{
			name: "unconfigured tool defaults to enabled",
			config: Config{
				Servers: []ServerConfig{
					{
						Name:    "Jira",
						Enabled: true,
						BaseURL: "https://mcp.atlassian.com",
						ToolConfigs: []ToolConfig{
							{Name: "getJiraIssue", Policy: ToolPolicyAsk, Enabled: true},
						},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "createJiraIssue", Description: "Create issue", ServerOrigin: "https://mcp.atlassian.com"},
			},
			wantToolNames: []string{"createJiraIssue"},
		},
		{
			name: "tool from unknown server is filtered out",
			config: Config{
				Servers: []ServerConfig{
					{
						Name:    "Jira",
						Enabled: true,
						BaseURL: "https://mcp.atlassian.com",
						ToolConfigs: []ToolConfig{
							{Name: "getJiraIssue", Policy: ToolPolicyAsk, Enabled: true},
						},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "unknown_tool", Description: "Unknown", ServerOrigin: "https://unknown-mcp-server.com"},
			},
		},
		{
			name: "mixed configured and unconfigured returns enabled configured plus unconfigured",
			config: Config{
				Servers: []ServerConfig{
					{
						Name:    "Jira",
						Enabled: true,
						BaseURL: "https://mcp.atlassian.com",
						ToolConfigs: []ToolConfig{
							{Name: "getJiraIssue", Policy: ToolPolicyAsk, Enabled: true},
							{Name: "createJiraIssue", Policy: ToolPolicyAsk, Enabled: false},
						},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "getJiraIssue", Description: "Get", ServerOrigin: "https://mcp.atlassian.com"},
				{Name: "createJiraIssue", Description: "Create", ServerOrigin: "https://mcp.atlassian.com"},
				{Name: "unconfigured_tool", Description: "Unconfigured", ServerOrigin: "https://mcp.atlassian.com"},
			},
			wantToolNames: []string{"getJiraIssue", "unconfigured_tool"},
		},
		{
			name: "deterministic ordering by configured server order then tool name",
			config: Config{
				Servers: []ServerConfig{
					{
						Name:    "GitHub",
						Enabled: true,
						BaseURL: "https://api.githubcopilot.com",
						ToolConfigs: []ToolConfig{
							{Name: "z_tool", Policy: ToolPolicyAsk, Enabled: true},
							{Name: "a_tool", Policy: ToolPolicyAsk, Enabled: true},
						},
					},
					{
						Name:    "Atlassian",
						Enabled: true,
						BaseURL: "https://mcp.atlassian.com",
						ToolConfigs: []ToolConfig{
							{Name: "b_tool", Policy: ToolPolicyAsk, Enabled: true},
						},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "b_tool", Description: "B", ServerOrigin: "https://mcp.atlassian.com"},
				{Name: "z_tool", Description: "Z", ServerOrigin: "https://api.githubcopilot.com"},
				{Name: "a_tool", Description: "A", ServerOrigin: "https://api.githubcopilot.com"},
			},
			wantToolNames: []string{"a_tool", "z_tool", "b_tool"},
		},
		{
			name:   "embedded server uses vetted tool seed and unconfigured tools default enabled",
			config: Config{},
			rawTools: []llm.Tool{
				{Name: "search_users", Description: "Search users", ServerOrigin: EmbeddedClientKey},
				{Name: "create_post", Description: "Create post", ServerOrigin: EmbeddedClientKey},
			},
			wantToolNames: []string{"create_post", "search_users"},
		},
		{
			name: "namespaced tool is denormalized before disabled admin policy lookup",
			config: Config{
				Servers: []ServerConfig{
					{
						Name:    "Jira",
						Enabled: true,
						BaseURL: "https://mcp.atlassian.com",
						ToolConfigs: []ToolConfig{
							{Name: "get_issue", Policy: ToolPolicyAsk, Enabled: false},
						},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "jira__get_issue", Description: "Get issue", ServerOrigin: "https://mcp.atlassian.com"},
			},
		},
		{
			name: "unconfigured namespaced tool defaults enabled by bare name",
			config: Config{
				Servers: []ServerConfig{
					{
						Name:    "Jira",
						Enabled: true,
						BaseURL: "https://mcp.atlassian.com",
						ToolConfigs: []ToolConfig{
							{Name: "get_issue", Policy: ToolPolicyAsk, Enabled: true},
						},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "jira__new_tool", Description: "New tool", ServerOrigin: "https://mcp.atlassian.com"},
			},
			wantToolNames: []string{"jira__new_tool"},
		},
		{
			name: "embedded namespaced tool is denormalized before admin policy lookup",
			config: Config{
				EmbeddedServer: EmbeddedServerConfig{
					ToolConfigs: []ToolConfig{
						{Name: "search_users", Policy: ToolPolicyAsk, Enabled: false},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "mattermost__search_users", Description: "Search users", ServerOrigin: EmbeddedClientKey},
			},
		},
		{
			name:   "plugin server enabled, tools flow through default-allow",
			config: Config{},
			pluginServers: []PluginServerConfig{
				{PluginID: "com.example.mcp", Name: "Example", Path: "/mcp", Enabled: true},
			},
			rawTools: []llm.Tool{
				{Name: "tool_a", ServerOrigin: "plugin://com.example.mcp"},
				{Name: "tool_b", ServerOrigin: "plugin://com.example.mcp"},
			},
			wantToolNames: []string{"tool_a", "tool_b"},
		},
		{
			name:   "plugin server with per-tool policy filters disabled tool",
			config: Config{},
			pluginServers: []PluginServerConfig{
				{
					PluginID: "com.example.mcp",
					Name:     "Example",
					Path:     "/mcp",
					Enabled:  true,
					ToolConfigs: []ToolConfig{
						{Name: "tool_a", Policy: ToolPolicyAsk, Enabled: false},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "tool_a", ServerOrigin: "plugin://com.example.mcp"},
				{Name: "tool_b", ServerOrigin: "plugin://com.example.mcp"},
			},
			wantToolNames: []string{"tool_b"},
		},
		{
			name:   "plugin server with per-tool policy returns explicitly enabled tool",
			config: Config{},
			pluginServers: []PluginServerConfig{
				{
					PluginID: "com.example.mcp",
					Name:     "Example",
					Path:     "/mcp",
					Enabled:  true,
					ToolConfigs: []ToolConfig{
						{Name: "tool_a", Policy: ToolPolicyAsk, Enabled: true},
					},
				},
			},
			rawTools: []llm.Tool{
				{Name: "tool_a", ServerOrigin: "plugin://com.example.mcp"},
			},
			wantToolNames: []string{"tool_a"},
		},
		{
			name:   "plugin server disabled, tools filtered out",
			config: Config{},
			pluginServers: []PluginServerConfig{
				{PluginID: "com.example.mcp", Name: "Example", Path: "/mcp", Enabled: false},
			},
			rawTools: []llm.Tool{
				{Name: "tool_a", ServerOrigin: "plugin://com.example.mcp"},
			},
			wantToolNames: nil,
		},
		{
			name: "embedded + remote + plugin mix",
			config: Config{
				Servers: []ServerConfig{{
					Name:        "Atlassian",
					Enabled:     true,
					BaseURL:     "https://mcp.atlassian.com",
					ToolConfigs: []ToolConfig{{Name: "getJiraIssue", Policy: ToolPolicyAsk, Enabled: true}},
				}},
			},
			pluginServers: []PluginServerConfig{
				{PluginID: "com.example.mcp", Name: "Example", Path: "/mcp", Enabled: true},
			},
			rawTools: []llm.Tool{
				{Name: "getJiraIssue", ServerOrigin: "https://mcp.atlassian.com"},
				{Name: "search_users", ServerOrigin: EmbeddedClientKey},
				{Name: "plugin_tool", ServerOrigin: "plugin://com.example.mcp"},
			},
			// serverOrder: remote, embedded, plugin.
			wantToolNames: []string{"getJiraIssue", "search_users", "plugin_tool"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Tests without embedded tools are unaffected by the non-nil client.
			embeddedClient := &EmbeddedServerClient{}

			filtered := filterToolsByConfig(tt.rawTools, tt.config, embeddedClient, tt.pluginServers)

			var names []string
			for _, tool := range filtered {
				names = append(names, tool.Name)
			}

			assert.Equal(t, tt.wantToolNames, names)
		})
	}
}
