// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsToolAutoApproved(t *testing.T) {
	tests := []struct {
		name          string
		config        *ApprovedMCPServersConfig
		serverBaseURL string
		toolName      string
		expected      bool
	}{
		{
			name:          "nil config returns false",
			config:        nil,
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      false,
		},
		{
			name:          "empty servers returns false",
			config:        &ApprovedMCPServersConfig{Servers: []ApprovedMCPServer{}},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      false,
		},
		{
			name: "approved tool from matching server returns true",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{"api.githubcopilot.com"},
						AutoApproveTools: []string{"get_me", "list_branches"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      true,
		},
		{
			name: "non-approved tool from matching server returns false",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{"api.githubcopilot.com"},
						AutoApproveTools: []string{"get_me", "list_branches"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "create_repository",
			expected:      false,
		},
		{
			name: "tool from non-matching server returns false",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{"api.githubcopilot.com"},
						AutoApproveTools: []string{"get_me"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
			toolName:      "get_me",
			expected:      false,
		},
		{
			name: "disabled server returns false even with matching tool",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{"api.githubcopilot.com"},
						AutoApproveTools: []string{"get_me"},
						Enabled:          false,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      false,
		},
		{
			name: "empty URL patterns returns false",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{},
						AutoApproveTools: []string{"get_me"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      false,
		},
		{
			name: "empty auto-approve tools returns false",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{"api.githubcopilot.com"},
						AutoApproveTools: []string{},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      false,
		},
		{
			name: "multiple URL patterns - second matches",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{"github.example.com", "api.githubcopilot.com"},
						AutoApproveTools: []string{"get_me"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      true,
		},
		{
			name: "multiple servers - second matches",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "Atlassian",
						URLPatterns:      []string{"mcp.atlassian.com"},
						AutoApproveTools: []string{"search"},
						Enabled:          true,
					},
					{
						Name:             "GitHub",
						URLPatterns:      []string{"api.githubcopilot.com"},
						AutoApproveTools: []string{"get_me"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      true,
		},
		{
			name: "empty server base URL returns false",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{"api.githubcopilot.com"},
						AutoApproveTools: []string{"get_me"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "",
			toolName:      "get_me",
			expected:      false,
		},
		{
			name: "empty tool name returns false",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{"api.githubcopilot.com"},
						AutoApproveTools: []string{"get_me"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "",
			expected:      false,
		},
		{
			name: "host-only match - path query fragment do not affect matching",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{"api.githubcopilot.com"},
						AutoApproveTools: []string{"get_me"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/path?query=1#fragment",
			toolName:      "get_me",
			expected:      true,
		},
		{
			name: "host-only match - partial host substring does not match",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "GitHub",
						URLPatterns:      []string{"githubcopilot"},
						AutoApproveTools: []string{"get_me"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      false,
		},
		{
			name: "empty string URL pattern is skipped",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{
						Name:             "Bad",
						URLPatterns:      []string{""},
						AutoApproveTools: []string{"get_me"},
						Enabled:          true,
					},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.IsToolAutoApproved(tt.serverBaseURL, tt.toolName)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestMatchesURLPattern(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		patterns []string
		expected bool
	}{
		{
			name:     "exact domain match",
			baseURL:  "https://mcp.atlassian.com/v1/mcp",
			patterns: []string{"mcp.atlassian.com"},
			expected: true,
		},
		{
			name:     "no match",
			baseURL:  "https://example.com/mcp",
			patterns: []string{"mcp.atlassian.com"},
			expected: false,
		},
		{
			name:     "empty patterns",
			baseURL:  "https://mcp.atlassian.com/v1/mcp",
			patterns: []string{},
			expected: false,
		},
		{
			name:     "nil patterns",
			baseURL:  "https://mcp.atlassian.com/v1/mcp",
			patterns: nil,
			expected: false,
		},
		{
			name:     "empty base URL",
			baseURL:  "",
			patterns: []string{"mcp.atlassian.com"},
			expected: false,
		},
		{
			name:     "empty pattern string is skipped",
			baseURL:  "https://anything.com",
			patterns: []string{""},
			expected: false,
		},
		{
			name:     "multiple patterns first matches",
			baseURL:  "https://mcp.atlassian.com/v1/mcp",
			patterns: []string{"mcp.atlassian.com", "api.githubcopilot.com"},
			expected: true,
		},
		{
			name:     "multiple patterns second matches",
			baseURL:  "https://api.githubcopilot.com/mcp/",
			patterns: []string{"mcp.atlassian.com", "api.githubcopilot.com"},
			expected: true,
		},
		{
			name:     "path query fragment bypass - host still matches",
			baseURL:  "https://mcp.atlassian.com/v1/mcp?foo=bar#hash",
			patterns: []string{"mcp.atlassian.com"},
			expected: true,
		},
		{
			name:     "port does not affect matching",
			baseURL:  "https://mcp.atlassian.com:443/v1/mcp",
			patterns: []string{"mcp.atlassian.com"},
			expected: true,
		},
		{
			name:     "subdomain matches pattern",
			baseURL:  "https://api.mcp.figma.com/mcp",
			patterns: []string{"mcp.figma.com"},
			expected: true,
		},
		{
			name:     "exact host matches pattern",
			baseURL:  "https://mcp.figma.com/mcp",
			patterns: []string{"mcp.figma.com"},
			expected: true,
		},
		{
			name:     "partial host substring does not match",
			baseURL:  "https://api.githubcopilot.com/mcp/",
			patterns: []string{"githubcopilot"},
			expected: false,
		},
		{
			name:     "URL with no host returns false",
			baseURL:  "https://",
			patterns: []string{"mcp.atlassian.com"},
			expected: false,
		},
		{
			name:     "case sensitive no match",
			baseURL:  "https://api.githubcopilot.com/mcp/",
			patterns: []string{"API.GITHUBCOPILOT.COM"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesURLPattern(tt.baseURL, tt.patterns)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestMergeApprovedServers(t *testing.T) {
	tests := []struct {
		name        string
		builtin     []ApprovedMCPServer
		userDefined []ApprovedMCPServer
		validate    func(t *testing.T, result *ApprovedMCPServersConfig)
	}{
		{
			name:        "both empty returns empty config",
			builtin:     nil,
			userDefined: nil,
			validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
				require.NotNil(t, result)
				require.Empty(t, result.Servers)
			},
		},
		{
			name: "builtin only returns builtin servers",
			builtin: []ApprovedMCPServer{
				{Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me"}, Enabled: true},
				{Name: "Atlassian", URLPatterns: []string{"atlassian.com"}, AutoApproveTools: []string{"search"}, Enabled: true},
			},
			userDefined: nil,
			validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
				require.Len(t, result.Servers, 2)
				require.Equal(t, "GitHub", result.Servers[0].Name)
				require.Equal(t, "Atlassian", result.Servers[1].Name)
			},
		},
		{
			name:    "user defined only returns user servers",
			builtin: nil,
			userDefined: []ApprovedMCPServer{
				{Name: "Custom", URLPatterns: []string{"custom.com"}, AutoApproveTools: []string{"my_tool"}, Enabled: true},
			},
			validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
				require.Len(t, result.Servers, 1)
				require.Equal(t, "Custom", result.Servers[0].Name)
			},
		},
		{
			name: "user override replaces builtin with same name",
			builtin: []ApprovedMCPServer{
				{Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me", "list_branches"}, Enabled: true},
			},
			userDefined: []ApprovedMCPServer{
				{Name: "GitHub", URLPatterns: []string{"github-enterprise.myco.com"}, AutoApproveTools: []string{"get_me"}, Enabled: true},
			},
			validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
				require.Len(t, result.Servers, 1)
				require.Equal(t, "GitHub", result.Servers[0].Name)
				// User version should win: different URL pattern and fewer tools
				require.Equal(t, []string{"github-enterprise.myco.com"}, result.Servers[0].URLPatterns)
				require.Equal(t, []string{"get_me"}, result.Servers[0].AutoApproveTools)
			},
		},
		{
			name: "user can disable a builtin server",
			builtin: []ApprovedMCPServer{
				{Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me"}, Enabled: true},
			},
			userDefined: []ApprovedMCPServer{
				{Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me"}, Enabled: false},
			},
			validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
				require.Len(t, result.Servers, 1)
				require.Equal(t, "GitHub", result.Servers[0].Name)
				require.False(t, result.Servers[0].Enabled)
			},
		},
		{
			name: "user adds new server alongside builtin",
			builtin: []ApprovedMCPServer{
				{Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me"}, Enabled: true},
			},
			userDefined: []ApprovedMCPServer{
				{Name: "Internal API", URLPatterns: []string{"internal.myco.com"}, AutoApproveTools: []string{"get_status"}, Enabled: true},
			},
			validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
				require.Len(t, result.Servers, 2)
				// Builtin first, then new user-defined
				require.Equal(t, "GitHub", result.Servers[0].Name)
				require.Equal(t, "Internal API", result.Servers[1].Name)
			},
		},
		{
			name: "user overrides one builtin and adds a new one",
			builtin: []ApprovedMCPServer{
				{Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me"}, Enabled: true},
				{Name: "Atlassian", URLPatterns: []string{"atlassian.com"}, AutoApproveTools: []string{"search"}, Enabled: true},
			},
			userDefined: []ApprovedMCPServer{
				{Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me", "extra_tool"}, Enabled: true},
				{Name: "Custom", URLPatterns: []string{"custom.com"}, AutoApproveTools: []string{"do_thing"}, Enabled: true},
			},
			validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
				require.Len(t, result.Servers, 3)
				// Order: GitHub (overridden), Atlassian (unchanged), Custom (new)
				require.Equal(t, "GitHub", result.Servers[0].Name)
				require.Equal(t, []string{"get_me", "extra_tool"}, result.Servers[0].AutoApproveTools)
				require.Equal(t, "Atlassian", result.Servers[1].Name)
				require.Equal(t, "Custom", result.Servers[2].Name)
			},
		},
		{
			name:        "empty builtin empty user returns empty",
			builtin:     []ApprovedMCPServer{},
			userDefined: []ApprovedMCPServer{},
			validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
				require.NotNil(t, result)
				require.Empty(t, result.Servers)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeApprovedServers(tt.builtin, tt.userDefined)
			require.NotNil(t, result)
			tt.validate(t, result)
		})
	}
}

func TestBuiltinApprovedServers(t *testing.T) {
	servers := BuiltinApprovedServers()

	require.Len(t, servers, 3, "should have exactly 3 built-in approved servers")

	t.Run("Atlassian server", func(t *testing.T) {
		s := servers[0]
		require.Equal(t, "Atlassian", s.Name)
		require.Equal(t, []string{"mcp.atlassian.com"}, s.URLPatterns)
		require.True(t, s.Enabled)
		require.Len(t, s.AutoApproveTools, 20, "Atlassian should have 20 READ tools")

		// Verify a few key tools are present
		expectedTools := []string{"search", "fetch", "getJiraIssue", "getConfluencePage", "searchJiraIssuesUsingJql"}
		for _, tool := range expectedTools {
			require.Contains(t, s.AutoApproveTools, tool, "Atlassian should contain tool: %s", tool)
		}

		// Verify WRITE tools are NOT present
		writeTools := []string{"createConfluencePage", "updateConfluencePage", "createJiraIssue", "editJiraIssue", "transitionJiraIssue"}
		for _, tool := range writeTools {
			require.NotContains(t, s.AutoApproveTools, tool, "Atlassian should not contain WRITE tool: %s", tool)
		}
	})

	t.Run("GitHub server", func(t *testing.T) {
		s := servers[1]
		require.Equal(t, "GitHub", s.Name)
		require.Equal(t, []string{"api.githubcopilot.com"}, s.URLPatterns)
		require.True(t, s.Enabled)
		require.Len(t, s.AutoApproveTools, 54, "GitHub should have 54 READ tools")

		// Verify a few key tools are present
		expectedTools := []string{"get_me", "list_branches", "issue_read", "pull_request_read", "search_code"}
		for _, tool := range expectedTools {
			require.Contains(t, s.AutoApproveTools, tool, "GitHub should contain tool: %s", tool)
		}

		// Verify WRITE/MIXED/DELETE tools are NOT present
		excludedTools := []string{"create_branch", "create_repository", "delete_file", "push_files", "merge_pull_request", "pull_request_review_write", "actions_run_trigger"}
		for _, tool := range excludedTools {
			require.NotContains(t, s.AutoApproveTools, tool, "GitHub should not contain non-READ tool: %s", tool)
		}
	})

	t.Run("Figma server", func(t *testing.T) {
		s := servers[2]
		require.Equal(t, "Figma", s.Name)
		require.Equal(t, []string{"mcp.figma.com"}, s.URLPatterns)
		require.True(t, s.Enabled)
		require.Len(t, s.AutoApproveTools, 8, "Figma should have 8 READ tools")

		// Verify a few key tools are present
		expectedTools := []string{"get_design_context", "get_screenshot", "whoami", "get_figjam"}
		for _, tool := range expectedTools {
			require.Contains(t, s.AutoApproveTools, tool, "Figma should contain tool: %s", tool)
		}

		// Verify WRITE tools are NOT present
		writeTools := []string{"generate_diagram", "generate_figma_design", "add_code_connect_map", "send_code_connect_mappings"}
		for _, tool := range writeTools {
			require.NotContains(t, s.AutoApproveTools, tool, "Figma should not contain WRITE tool: %s", tool)
		}
	})
}

func TestBuiltinApprovedServersIntegration(t *testing.T) {
	config := MergeApprovedServers(BuiltinApprovedServers(), nil)

	tests := []struct {
		name          string
		serverBaseURL string
		toolName      string
		expected      bool
	}{
		{
			name:          "GitHub READ tool auto-approved",
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      true,
		},
		{
			name:          "GitHub WRITE tool not auto-approved",
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "create_repository",
			expected:      false,
		},
		{
			name:          "Atlassian READ tool auto-approved",
			serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
			toolName:      "getJiraIssue",
			expected:      true,
		},
		{
			name:          "Atlassian WRITE tool not auto-approved",
			serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
			toolName:      "createJiraIssue",
			expected:      false,
		},
		{
			name:          "Figma READ tool auto-approved",
			serverBaseURL: "https://mcp.figma.com/mcp",
			toolName:      "get_design_context",
			expected:      true,
		},
		{
			name:          "Figma WRITE tool not auto-approved",
			serverBaseURL: "https://mcp.figma.com/mcp",
			toolName:      "generate_diagram",
			expected:      false,
		},
		{
			name:          "unknown server not auto-approved",
			serverBaseURL: "https://unknown-mcp-server.com/mcp",
			toolName:      "get_me",
			expected:      false,
		},
		{
			name:          "unknown tool on known server not auto-approved",
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "nonexistent_tool",
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := config.IsToolAutoApproved(tt.serverBaseURL, tt.toolName)
			require.Equal(t, tt.expected, result)
		})
	}
}
