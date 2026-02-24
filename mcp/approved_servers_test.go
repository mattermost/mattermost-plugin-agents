// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
			serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
			toolName:      "search",
			expected:      false,
		},
		{
			name:          "empty server URL returns false",
			config:        &ApprovedMCPServersConfig{Servers: BuiltinApprovedServers()},
			serverBaseURL: "",
			toolName:      "search",
			expected:      false,
		},
		{
			name:          "empty tool name returns false",
			config:        &ApprovedMCPServersConfig{Servers: BuiltinApprovedServers()},
			serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
			toolName:      "",
			expected:      false,
		},
		{
			name: "approved tool on enabled Atlassian server",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: true, AutoApproveTools: []string{"search", "getJiraIssue"}},
				},
			},
			serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
			toolName:      "search",
			expected:      true,
		},
		{
			name: "non-approved tool on enabled server",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: true, AutoApproveTools: []string{"search", "getJiraIssue"}},
				},
			},
			serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
			toolName:      "createJiraIssue",
			expected:      false,
		},
		{
			name: "approved tool on disabled server",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: false, AutoApproveTools: []string{"search"}},
				},
			},
			serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
			toolName:      "search",
			expected:      false,
		},
		{
			name: "tool from non-matching server",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: true, AutoApproveTools: []string{"search"}},
				},
			},
			serverBaseURL: "https://evil.example.com/mcp",
			toolName:      "search",
			expected:      false,
		},
		{
			name: "multiple URL patterns - second matches",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{Name: "GitHub", URLPatterns: []string{"api.github.com", "api.githubcopilot.com"}, Enabled: true, AutoApproveTools: []string{"get_me"}},
				},
			},
			serverBaseURL: "https://api.githubcopilot.com/mcp/",
			toolName:      "get_me",
			expected:      true,
		},
		{
			name: "empty URL patterns returns false",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{Name: "Test", URLPatterns: []string{}, Enabled: true, AutoApproveTools: []string{"test_tool"}},
				},
			},
			serverBaseURL: "https://example.com/mcp",
			toolName:      "test_tool",
			expected:      false,
		},
		{
			name: "empty auto-approve list returns false",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: true, AutoApproveTools: []string{}},
				},
			},
			serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
			toolName:      "search",
			expected:      false,
		},
		{
			name: "multiple servers - matches second server",
			config: &ApprovedMCPServersConfig{
				Servers: []ApprovedMCPServer{
					{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: true, AutoApproveTools: []string{"search"}},
					{Name: "Figma", URLPatterns: []string{"mcp.figma.com"}, Enabled: true, AutoApproveTools: []string{"get_screenshot"}},
				},
			},
			serverBaseURL: "https://mcp.figma.com/mcp",
			toolName:      "get_screenshot",
			expected:      true,
		},
		{
			name:          "no servers configured returns false",
			config:        &ApprovedMCPServersConfig{Servers: []ApprovedMCPServer{}},
			serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
			toolName:      "search",
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.IsToolAutoApproved(tt.serverBaseURL, tt.toolName)
			assert.Equal(t, tt.expected, result)
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
			name:     "exact substring match",
			baseURL:  "https://mcp.atlassian.com/v1/mcp",
			patterns: []string{"mcp.atlassian.com"},
			expected: true,
		},
		{
			name:     "no match",
			baseURL:  "https://evil.example.com/mcp",
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
			name:     "empty base URL never matches",
			baseURL:  "",
			patterns: []string{"mcp.atlassian.com"},
			expected: false,
		},
		{
			name:     "empty pattern string is skipped",
			baseURL:  "https://mcp.atlassian.com/v1/mcp",
			patterns: []string{"", "mcp.atlassian.com"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesURLPattern(tt.baseURL, tt.patterns)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResolveApprovedServers(t *testing.T) {
	tests := []struct {
		name              string
		config            Config
		checkAtlassian    bool
		checkGitHub       bool
		checkFigma        bool
		expectServerCount int
	}{
		{
			name: "all providers enabled",
			config: Config{
				ApprovedProviders: ApprovedProviders{
					Atlassian: true,
					GitHub:    true,
					Figma:     true,
				},
			},
			checkAtlassian:    true,
			checkGitHub:       true,
			checkFigma:        true,
			expectServerCount: 3,
		},
		{
			name: "only GitHub enabled",
			config: Config{
				ApprovedProviders: ApprovedProviders{
					Atlassian: false,
					GitHub:    true,
					Figma:     false,
				},
			},
			checkAtlassian:    false,
			checkGitHub:       true,
			checkFigma:        false,
			expectServerCount: 3,
		},
		{
			name: "all providers disabled (default)",
			config: Config{
				ApprovedProviders: ApprovedProviders{},
			},
			checkAtlassian:    false,
			checkGitHub:       false,
			checkFigma:        false,
			expectServerCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.ResolveApprovedServers()
			require.NotNil(t, result)
			assert.Len(t, result.Servers, tt.expectServerCount)

			for _, server := range result.Servers {
				switch server.Name {
				case "Atlassian":
					assert.Equal(t, tt.checkAtlassian, server.Enabled, "Atlassian Enabled mismatch")
					assert.Greater(t, len(server.AutoApproveTools), 0, "Atlassian should have auto-approve tools")
				case "GitHub":
					assert.Equal(t, tt.checkGitHub, server.Enabled, "GitHub Enabled mismatch")
					assert.Greater(t, len(server.AutoApproveTools), 0, "GitHub should have auto-approve tools")
				case "Figma":
					assert.Equal(t, tt.checkFigma, server.Enabled, "Figma Enabled mismatch")
					assert.Greater(t, len(server.AutoApproveTools), 0, "Figma should have auto-approve tools")
				}
			}
		})
	}
}

func TestResolveApprovedServers_EndToEnd(t *testing.T) {
	// Test the full flow: config -> resolve -> check tool approval
	config := Config{
		ApprovedProviders: ApprovedProviders{
			Atlassian: true,
			GitHub:    false,
			Figma:     true,
		},
	}

	approvedServers := config.ResolveApprovedServers()

	// Atlassian READ tool should be approved
	assert.True(t, approvedServers.IsToolAutoApproved("https://mcp.atlassian.com/v1/mcp", "search"))
	assert.True(t, approvedServers.IsToolAutoApproved("https://mcp.atlassian.com/v1/mcp", "getJiraIssue"))

	// Atlassian WRITE tool should not be approved
	assert.False(t, approvedServers.IsToolAutoApproved("https://mcp.atlassian.com/v1/mcp", "createJiraIssue"))

	// GitHub is disabled, so even READ tools should not be approved
	assert.False(t, approvedServers.IsToolAutoApproved("https://api.githubcopilot.com/mcp/", "get_me"))
	assert.False(t, approvedServers.IsToolAutoApproved("https://api.githubcopilot.com/mcp/", "issue_read"))

	// Figma READ tool should be approved
	assert.True(t, approvedServers.IsToolAutoApproved("https://mcp.figma.com/mcp", "get_screenshot"))
	assert.True(t, approvedServers.IsToolAutoApproved("https://mcp.figma.com/mcp", "whoami"))

	// Figma WRITE tool should not be approved
	assert.False(t, approvedServers.IsToolAutoApproved("https://mcp.figma.com/mcp", "generate_diagram"))

	// Unknown server should never be approved
	assert.False(t, approvedServers.IsToolAutoApproved("https://evil.example.com/mcp", "search"))
}

func TestBuiltinApprovedServers(t *testing.T) {
	servers := BuiltinApprovedServers()
	require.Len(t, servers, 3)

	names := make(map[string]bool)
	for _, server := range servers {
		names[server.Name] = true
		// All built-in servers should default to disabled
		assert.False(t, server.Enabled, "built-in server %s should default to disabled", server.Name)
		// All should have URL patterns
		assert.NotEmpty(t, server.URLPatterns, "built-in server %s should have URL patterns", server.Name)
		// All should have auto-approve tools
		assert.NotEmpty(t, server.AutoApproveTools, "built-in server %s should have auto-approve tools", server.Name)
	}

	assert.True(t, names["Atlassian"])
	assert.True(t, names["GitHub"])
	assert.True(t, names["Figma"])
}
