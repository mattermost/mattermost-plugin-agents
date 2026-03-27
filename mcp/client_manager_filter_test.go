// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/assert"
)

func TestFilterToolsByConfig(t *testing.T) {
	tests := []struct {
		name          string
		config        Config
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
			name: "embedded server uses vetted tool seed and unconfigured tools default enabled",
			config: Config{
				EmbeddedServer: EmbeddedServerConfig{Enabled: true},
			},
			rawTools: []llm.Tool{
				{Name: "search_users", Description: "Search users", ServerOrigin: EmbeddedClientKey},
				{Name: "create_post", Description: "Create post", ServerOrigin: EmbeddedClientKey},
			},
			wantToolNames: []string{"create_post", "search_users"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var embeddedClient *EmbeddedServerClient
			if tt.config.EmbeddedServer.Enabled {
				embeddedClient = &EmbeddedServerClient{}
			}

			filtered := filterToolsByConfig(tt.rawTools, tt.config, embeddedClient)

			var names []string
			for _, tool := range filtered {
				names = append(names, tool.Name)
			}

			assert.Equal(t, tt.wantToolNames, names)
		})
	}
}
