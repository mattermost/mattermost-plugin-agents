// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsVettedHost(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{
			name:    "Atlassian exact host match",
			baseURL: "https://mcp.atlassian.com/v1/mcp",
			want:    true,
		},
		{
			name:    "GitHub exact host match",
			baseURL: "https://api.githubcopilot.com/mcp/",
			want:    true,
		},
		{
			name:    "Figma exact host match",
			baseURL: "https://mcp.figma.com/mcp",
			want:    true,
		},
		{
			name:    "Mattermost embedded match",
			baseURL: EmbeddedClientKey,
			want:    true,
		},
		{
			name:    "subdomain matches vetted pattern",
			baseURL: "https://api.mcp.figma.com/mcp",
			want:    true,
		},
		{
			name:    "path query fragment ignored",
			baseURL: "https://mcp.atlassian.com/v1/mcp?foo=bar#hash",
			want:    true,
		},
		{
			name:    "port ignored",
			baseURL: "https://mcp.atlassian.com:443/v1/mcp",
			want:    true,
		},
		{
			name:    "unknown host is not vetted",
			baseURL: "https://unknown.example.com/mcp",
			want:    false,
		},
		{
			name:    "partial host substring does not match",
			baseURL: "https://evil-githubcopilot.com/mcp",
			want:    false,
		},
		{
			name:    "typosquat host does not match vetted Atlassian pattern",
			baseURL: "https://mcp.atlassian.com.evil.com/mcp",
			want:    false,
		},
		{
			name:    "remote mattermost hostname is not vetted",
			baseURL: "https://mattermost/mcp",
			want:    false,
		},
		{
			name:    "remote mattermost subdomain is not vetted",
			baseURL: "https://evil.mattermost/mcp",
			want:    false,
		},
		{
			name:    "empty URL is not vetted",
			baseURL: "",
			want:    false,
		},
		{
			name:    "invalid URL is not vetted",
			baseURL: "://bad-url",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsVettedHost(tt.baseURL))
		})
	}
}

func TestSeedVettedToolConfigs(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		wantCount int
		wantNil   bool
	}{
		{
			name:      "Atlassian seeds 20 read tools",
			baseURL:   "https://mcp.atlassian.com/v1/mcp",
			wantCount: 20,
		},
		{
			name:      "GitHub seeds 54 read tools",
			baseURL:   "https://api.githubcopilot.com/mcp/",
			wantCount: 54,
		},
		{
			name:      "Figma seeds 8 read tools",
			baseURL:   "https://mcp.figma.com/mcp",
			wantCount: 8,
		},
		{
			name:      "Mattermost seeds 10 read tools",
			baseURL:   EmbeddedClientKey,
			wantCount: 10,
		},
		{
			name:    "unknown host returns nil",
			baseURL: "https://unknown.example.com/mcp",
			wantNil: true,
		},
		{
			name:    "remote mattermost hostname returns nil",
			baseURL: "https://mattermost/mcp",
			wantNil: true,
		},
		{
			name:    "remote mattermost subdomain returns nil",
			baseURL: "https://evil.mattermost/mcp",
			wantNil: true,
		},
		{
			name:    "empty URL returns nil",
			baseURL: "",
			wantNil: true,
		},
		{
			name:    "invalid URL returns nil",
			baseURL: "://bad-url",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SeedVettedToolConfigs(tt.baseURL)

			if tt.wantNil {
				require.Nil(t, got)
				return
			}

			require.Len(t, got, tt.wantCount)
			for _, cfg := range got {
				require.True(t, cfg.Enabled)
				switch {
				case strings.Contains(tt.baseURL, "api.githubcopilot.com"):
					require.True(t, cfg.Policy == ToolPolicyAutoRunInDM || cfg.Policy == ToolPolicyAsk)
				default:
					require.Equal(t, ToolPolicyAutoRunInDM, cfg.Policy)
				}
				require.NotEmpty(t, cfg.Name)
			}
		})
	}
}

func TestSeedVettedToolConfigsSpotChecks(t *testing.T) {
	t.Run("Atlassian", func(t *testing.T) {
		configs := SeedVettedToolConfigs("https://mcp.atlassian.com/v1/mcp")
		requireToolConfig(t, configs, "getJiraIssue", ToolPolicyAutoRunInDM, true)
		requireToolConfig(t, configs, "search", ToolPolicyAutoRunInDM, true)
		requireNoToolConfig(t, configs, "createJiraIssue")
	})

	t.Run("GitHub", func(t *testing.T) {
		configs := SeedVettedToolConfigs("https://api.githubcopilot.com/mcp/")
		requireToolConfig(t, configs, "get_me", ToolPolicyAutoRunInDM, true)
		requireToolConfig(t, configs, "pull_request_read", ToolPolicyAutoRunInDM, true)
		requireToolConfig(t, configs, "get_code_scanning_alert", ToolPolicyAsk, true)
		requireToolConfig(t, configs, "list_repository_security_advisories", ToolPolicyAsk, true)
		requireToolConfig(t, configs, "get_global_security_advisory", ToolPolicyAutoRunInDM, true)
		requireNoToolConfig(t, configs, "create_repository")
	})

	t.Run("Figma", func(t *testing.T) {
		configs := SeedVettedToolConfigs("https://mcp.figma.com/mcp")
		requireToolConfig(t, configs, "get_design_context", ToolPolicyAutoRunInDM, true)
		requireToolConfig(t, configs, "whoami", ToolPolicyAutoRunInDM, true)
		requireNoToolConfig(t, configs, "generate_diagram")
	})

	t.Run("Mattermost", func(t *testing.T) {
		configs := SeedVettedToolConfigs(EmbeddedClientKey)
		requireToolConfig(t, configs, "search_posts", ToolPolicyAutoRunInDM, true)
		requireToolConfig(t, configs, "search_users", ToolPolicyAutoRunInDM, true)
		requireToolConfig(t, configs, "read_file", ToolPolicyAutoRunInDM, true)
		requireNoToolConfig(t, configs, "create_post")
	})
}

func TestMergeSeedConfigs(t *testing.T) {
	tests := []struct {
		name   string
		stored []ToolConfig
		seed   []ToolConfig
		want   []ToolConfig
	}{
		{
			name:   "empty stored returns the seed",
			stored: nil,
			seed:   []ToolConfig{{Name: "read_file", Policy: ToolPolicyAutoRunInDM, Enabled: true}},
			want:   []ToolConfig{{Name: "read_file", Policy: ToolPolicyAutoRunInDM, Enabled: true}},
		},
		{
			name:   "tool missing from stored is appended from the seed",
			stored: []ToolConfig{{Name: "search_posts", Policy: ToolPolicyAutoRunInDM, Enabled: true}},
			seed:   []ToolConfig{{Name: "read_file", Policy: ToolPolicyAutoRunInDM, Enabled: true}},
			want: []ToolConfig{
				{Name: "search_posts", Policy: ToolPolicyAutoRunInDM, Enabled: true},
				{Name: "read_file", Policy: ToolPolicyAutoRunInDM, Enabled: true},
			},
		},
		{
			// Conflicting non-default values on both sides prove the stored
			// entry wins (rather than coinciding with the unconfigured default)
			// and that no duplicate read_file reaches last-match-wins resolution.
			name:   "stored entry wins over a conflicting seed entry",
			stored: []ToolConfig{{Name: "read_file", Policy: ToolPolicyAutoRunEverywhere, Enabled: false}},
			seed:   []ToolConfig{{Name: "read_file", Policy: ToolPolicyAutoRunInDM, Enabled: true}},
			want:   []ToolConfig{{Name: "read_file", Policy: ToolPolicyAutoRunEverywhere, Enabled: false}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Callers pass the live config slice as stored, so the merge must
			// not mutate its inputs.
			storedBefore := append([]ToolConfig(nil), tt.stored...)
			seedBefore := append([]ToolConfig(nil), tt.seed...)

			got := mergeSeedConfigs(tt.stored, tt.seed)

			require.Equal(t, tt.want, got)
			require.Equal(t, storedBefore, tt.stored)
			require.Equal(t, seedBefore, tt.seed)
		})
	}
}

func requireToolConfig(t *testing.T, configs []ToolConfig, name, policy string, enabled bool) {
	t.Helper()

	for _, cfg := range configs {
		if cfg.Name == name {
			require.Equal(t, policy, cfg.Policy)
			require.Equal(t, enabled, cfg.Enabled)
			return
		}
	}

	t.Fatalf("expected tool config for %q", name)
}

func requireNoToolConfig(t *testing.T, configs []ToolConfig, name string) {
	t.Helper()

	for _, cfg := range configs {
		if cfg.Name == name {
			t.Fatalf("did not expect tool config for %q", name)
		}
	}
}
