// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"strings"
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
		{name: "preserves malformed query", raw: "https://mcp.example.com/mcp?x=1;y=2", expected: "https://mcp.example.com/mcp?x=1;y=2"},
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

func TestMCPConfigValidateDescriptionLength(t *testing.T) {
	testCases := []struct {
		name        string
		description string
		wantErr     bool
	}{
		{name: "empty description", description: ""},
		{name: "description at the limit", description: strings.Repeat("a", MaxMCPServerDescriptionRunes)},
		{name: "limit counts characters, not bytes", description: strings.Repeat("é", MaxMCPServerDescriptionRunes)},
		{name: "description over the limit", description: strings.Repeat("a", MaxMCPServerDescriptionRunes+1), wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := MCPConfig{Servers: []MCPServerConfig{
				{Name: "Jira", BaseURL: "https://jira.example.com/mcp", Description: tc.description},
				{Name: "GitHub", BaseURL: "https://github.example.com/mcp"},
			}}

			err := cfg.Validate()
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), `MCP server "Jira" description`)
		})
	}
}

func TestMCPConfigValidateAuthorizationParams(t *testing.T) {
	testCases := []struct {
		name    string
		params  map[string]string
		wantErr string
	}{
		{name: "no parameters"},
		{name: "provider parameters", params: map[string]string{"access_type": "offline", "prompt": "consent"}},
		{name: "empty name", params: map[string]string{"": "offline"}, wantErr: "empty name"},
		{name: "blank name", params: map[string]string{"  ": "offline"}, wantErr: "empty name"},
		{name: "response_type", params: map[string]string{"response_type": "token"}, wantErr: `"response_type"`},
		{name: "client_id", params: map[string]string{"client_id": "other"}, wantErr: `"client_id"`},
		{name: "redirect_uri", params: map[string]string{"redirect_uri": "https://evil.example.com"}, wantErr: `"redirect_uri"`},
		{name: "state", params: map[string]string{"state": "fixed"}, wantErr: `"state"`},
		{name: "scope", params: map[string]string{"scope": "admin"}, wantErr: `"scope"`},
		{name: "code_challenge", params: map[string]string{"code_challenge": "fixed"}, wantErr: `"code_challenge"`},
		{name: "code_challenge_method", params: map[string]string{"code_challenge_method": "plain"}, wantErr: `"code_challenge_method"`},
		{name: "resource", params: map[string]string{"resource": "https://other.example.com"}, wantErr: `"resource"`},
		{name: "reserved name in another case", params: map[string]string{"State": "fixed"}, wantErr: `"State"`},
		{name: "reserved name with spaces", params: map[string]string{" state ": "fixed"}, wantErr: `" state "`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := MCPConfig{Servers: []MCPServerConfig{
				{Name: "YouTrack", BaseURL: "https://youtrack.example.com/mcp", AuthorizationParams: tc.params},
			}}

			err := cfg.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, `MCP server "YouTrack"`)
			require.ErrorContains(t, err, tc.wantErr)
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
		// errorLineCount, when set, asserts how many collisions were reported.
		errorLineCount int
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
			name: "malformed query does not collide with missing query",
			servers: []MCPServerConfig{
				{Name: "Alpha", BaseURL: "https://mcp.example.com/mcp?x=1;y=2"},
				{Name: "Beta", BaseURL: "https://mcp.example.com/mcp"},
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
			conflictIndexes: []int{0, 1},
			conflictReasons: []MCPServerConflictReason{MCPServerConflictDuplicateURL, MCPServerConflictDuplicateURL},
			// Reported once, against the canonical endpoint both entries share.
			errorMustContain: []string{`endpoint "https://mcp.example.com/mcp" is configured on more than one server`},
			errorLineCount:   1,
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
			if tc.errorLineCount > 0 {
				require.Len(t, strings.Split(err.Error(), "\n"), tc.errorLineCount)
			}
		})
	}
}
