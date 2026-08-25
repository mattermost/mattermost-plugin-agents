// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalMCPEndpointURL(t *testing.T) {
	testCases := []struct {
		name     string
		raw      string
		expected string
	}{
		{name: "empty", raw: "   ", expected: ""},
		{name: "lowercases scheme and host", raw: "HTTPS://MCP.Example.COM/Path", expected: "https://mcp.example.com/Path"},
		{name: "drops default https port", raw: "https://mcp.example.com:443/mcp", expected: "https://mcp.example.com/mcp"},
		{name: "drops default http port", raw: "http://mcp.example.com:80/mcp", expected: "http://mcp.example.com/mcp"},
		{name: "keeps non-default port", raw: "https://mcp.example.com:8443/mcp", expected: "https://mcp.example.com:8443/mcp"},
		{name: "drops trailing slash", raw: "https://mcp.example.com/mcp/", expected: "https://mcp.example.com/mcp"},
		{name: "drops root slash", raw: "https://mcp.example.com/", expected: "https://mcp.example.com"},
		{name: "trims surrounding whitespace", raw: "  https://mcp.example.com/mcp  ", expected: "https://mcp.example.com/mcp"},
		{name: "sorts query parameters", raw: "https://mcp.example.com/mcp?b=2&a=1", expected: "https://mcp.example.com/mcp?a=1&b=2"},
		{name: "drops fragment", raw: "https://mcp.example.com/mcp#frag", expected: "https://mcp.example.com/mcp"},
		{name: "preserves userinfo", raw: "https://user@mcp.example.com/mcp", expected: "https://user@mcp.example.com/mcp"},
		{name: "brackets bare ipv6 host", raw: "https://[::1]/mcp", expected: "https://[::1]/mcp"},
		{name: "keeps ipv6 host with port", raw: "https://[::1]:8443/mcp", expected: "https://[::1]:8443/mcp"},
		{name: "non-url input compared case-insensitively", raw: "Not A URL", expected: "not a url"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, CanonicalMCPEndpointURL(tc.raw))
		})
	}
}

func TestMCPConfigValidateDuplicates(t *testing.T) {
	testCases := []struct {
		name             string
		servers          []MCPServerConfig
		expectValid      bool
		conflictIndexes  []int
		conflictReasons  []MCPServerConflictReason
		errorMustContain []string
	}{
		{
			name: "distinct names and urls are valid",
			servers: []MCPServerConfig{
				{Name: "Jira", BaseURL: "https://mcp.atlassian.com/v1/sse"},
				{Name: "GitHub", BaseURL: "https://api.githubcopilot.com/mcp"},
			},
			expectValid: true,
		},
		{
			name: "distinct paths on the same host stay valid",
			servers: []MCPServerConfig{
				{Name: "Alpha", BaseURL: "https://mcp.example.com/alpha"},
				{Name: "Beta", BaseURL: "https://mcp.example.com/beta"},
			},
			expectValid: true,
		},
		{
			name: "meaningful query differences stay valid",
			servers: []MCPServerConfig{
				{Name: "Alpha", BaseURL: "https://mcp.example.com/mcp?tenant=a"},
				{Name: "Beta", BaseURL: "https://mcp.example.com/mcp?tenant=b"},
			},
			expectValid: true,
		},
		{
			name: "duplicate names are rejected",
			servers: []MCPServerConfig{
				{Name: "Jira", BaseURL: "https://a.example.com/mcp"},
				{Name: "Jira", BaseURL: "https://b.example.com/mcp"},
			},
			conflictIndexes:  []int{0, 1},
			conflictReasons:  []MCPServerConflictReason{MCPServerConflictDuplicateName, MCPServerConflictDuplicateName},
			errorMustContain: []string{`name "Jira" is used by more than one server`},
		},
		{
			name: "canonically equivalent urls are rejected",
			servers: []MCPServerConfig{
				{Name: "Alpha", BaseURL: "https://MCP.Example.com:443/mcp/"},
				{Name: "Beta", BaseURL: "https://mcp.example.com/mcp"},
			},
			conflictIndexes:  []int{0, 1},
			conflictReasons:  []MCPServerConflictReason{MCPServerConflictDuplicateURL, MCPServerConflictDuplicateURL},
			errorMustContain: []string{"is used by more than one server"},
		},
		{
			name: "reordered query parameters are rejected as duplicates",
			servers: []MCPServerConfig{
				{Name: "Alpha", BaseURL: "https://mcp.example.com/mcp?a=1&b=2"},
				{Name: "Beta", BaseURL: "https://mcp.example.com/mcp?b=2&a=1"},
			},
			conflictIndexes: []int{0, 1},
			conflictReasons: []MCPServerConflictReason{MCPServerConflictDuplicateURL, MCPServerConflictDuplicateURL},
		},
		{
			name: "only the colliding entries are reported",
			servers: []MCPServerConfig{
				{Name: "Alpha", BaseURL: "https://a.example.com/mcp"},
				{Name: "Dup", BaseURL: "https://b.example.com/mcp"},
				{Name: "Dup", BaseURL: "https://c.example.com/mcp"},
				{Name: "Omega", BaseURL: "https://d.example.com/mcp"},
			},
			conflictIndexes: []int{1, 2},
			conflictReasons: []MCPServerConflictReason{MCPServerConflictDuplicateName, MCPServerConflictDuplicateName},
		},
		{
			name: "blank name and url entries are ignored",
			servers: []MCPServerConfig{
				{Name: "", BaseURL: ""},
				{Name: "", BaseURL: ""},
			},
			expectValid: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := MCPConfig{Servers: tc.servers}

			conflicts := cfg.ServerConflicts()
			err := cfg.Validate()

			if tc.expectValid {
				require.Empty(t, conflicts)
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			require.ErrorIs(t, err, ErrDuplicateMCPServer)

			gotIndexes := make([]int, 0, len(conflicts))
			gotReasons := make([]MCPServerConflictReason, 0, len(conflicts))
			for _, conflict := range conflicts {
				gotIndexes = append(gotIndexes, conflict.Index)
				gotReasons = append(gotReasons, conflict.Reason)
			}
			require.Equal(t, tc.conflictIndexes, gotIndexes)
			require.Equal(t, tc.conflictReasons, gotReasons)

			for _, substring := range tc.errorMustContain {
				require.Contains(t, err.Error(), substring)
			}
		})
	}
}
