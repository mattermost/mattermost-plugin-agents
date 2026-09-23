// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/require"
)

func TestActiveServiceIDs(t *testing.T) {
	cfg := &Config{
		Services: []llm.ServiceConfig{
			{ID: "first"},
			{ID: "second"},
			{ID: "third"},
		},
	}

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			ids := ActiveServiceIDs(cfg, level)
			if level >= enterprise.LevelEnterprise {
				require.Equal(t, []string{"first", "second", "third"}, ids)
				return
			}
			require.Equal(t, []string{"first"}, ids)
		})
	}

	t.Run("nil config", func(t *testing.T) {
		require.Empty(t, ActiveServiceIDs(nil, enterprise.LevelEnterpriseAdvanced))
	})
}

func TestValidateLicenseTransition(t *testing.T) {
	overLimitServices := Config{
		Services: []llm.ServiceConfig{{ID: "s1"}, {ID: "s2"}},
	}
	overLimitBots := Config{
		Bots: []llm.BotConfig{
			{ID: "b1", Name: "one", DisplayName: "One", ServiceID: "s1"},
			{ID: "b2", Name: "two", DisplayName: "Two", ServiceID: "s1"},
		},
	}

	tests := []struct {
		name     string
		prev     *Config
		next     Config
		minLevel enterprise.Level
		quota    bool
	}{
		{
			name:     "adding a second service",
			next:     Config{Services: []llm.ServiceConfig{{ID: "s1"}, {ID: "s2"}}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name: "unchanged over-limit services are accepted",
			prev: &overLimitServices,
			next: overLimitServices,
		},
		{
			name: "reducing service count is accepted",
			prev: &overLimitServices,
			next: Config{Services: []llm.ServiceConfig{{ID: "s1"}}},
		},
		{
			name:     "newly non-empty fallback",
			prev:     &Config{Services: []llm.ServiceConfig{{ID: "s1"}}},
			next:     Config{Services: []llm.ServiceConfig{{ID: "s1", FallbackServiceID: "s2"}}},
			minLevel: enterprise.LevelEnterpriseAdvanced,
		},
		{
			name:     "retargeting a fallback",
			prev:     &Config{Services: []llm.ServiceConfig{{ID: "s1", FallbackServiceID: "s2"}}},
			next:     Config{Services: []llm.ServiceConfig{{ID: "s1", FallbackServiceID: "s3"}}},
			minLevel: enterprise.LevelEnterpriseAdvanced,
		},
		{
			name: "unchanged fallback is accepted",
			prev: &Config{Services: []llm.ServiceConfig{{ID: "s1", FallbackServiceID: "s2"}}},
			next: Config{Services: []llm.ServiceConfig{{ID: "s1", FallbackServiceID: "s2"}}},
		},
		{
			name: "clearing fallback is accepted",
			prev: &Config{Services: []llm.ServiceConfig{{ID: "s1", FallbackServiceID: "s2"}}},
			next: Config{Services: []llm.ServiceConfig{{ID: "s1"}}},
		},
		{
			name:     "adding a second config bot",
			next:     overLimitBots,
			minLevel: enterprise.LevelProfessional,
			quota:    true,
		},
		{
			name: "unchanged over-limit bots are accepted",
			prev: &overLimitBots,
			next: overLimitBots,
		},
		{
			name: "reducing bot count is accepted",
			prev: &overLimitBots,
			next: Config{Bots: []llm.BotConfig{overLimitBots.Bots[0]}},
		},
		{
			name: "config bot newly carrying access controls",
			next: Config{Bots: []llm.BotConfig{{
				ID: "b1", Name: "one", DisplayName: "One", ServiceID: "s1",
				ChannelAccessLevel: llm.ChannelAccessLevelAllow,
				ChannelIDs:         []string{"ch1"},
			}}},
			minLevel: enterprise.LevelProfessional,
		},
		{
			name: "opening access controls is accepted",
			prev: &Config{Bots: []llm.BotConfig{{
				ID: "b1", Name: "one", DisplayName: "One", ServiceID: "s1",
				ChannelAccessLevel: llm.ChannelAccessLevelAllow,
				ChannelIDs:         []string{"ch1"},
			}}},
			next: Config{Bots: []llm.BotConfig{{
				ID: "b1", Name: "one", DisplayName: "One", ServiceID: "s1",
			}}},
		},
		{
			name: "config bot newly attribute-based",
			next: Config{Bots: []llm.BotConfig{{
				ID: "b1", Name: "one", DisplayName: "One", ServiceID: "s1",
				UserAccessLevel: llm.UserAccessLevelAttributeBased,
			}}},
			minLevel: enterprise.LevelEnterpriseAdvanced,
		},
		{
			name: "config bot newly UseServiceAccountAuth",
			next: Config{Bots: []llm.BotConfig{{
				ID: "b1", Name: "one", DisplayName: "One", ServiceID: "s1",
				UseServiceAccountAuth: true,
			}}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name: "turning off UseServiceAccountAuth is accepted",
			prev: &Config{Bots: []llm.BotConfig{{
				ID: "b1", Name: "one", DisplayName: "One", ServiceID: "s1",
				UseServiceAccountAuth: true,
			}}},
			next: Config{Bots: []llm.BotConfig{{
				ID: "b1", Name: "one", DisplayName: "One", ServiceID: "s1",
			}}},
		},
		{
			name: "config bot newly provider web search",
			next: Config{Bots: []llm.BotConfig{{
				ID: "b1", Name: "one", DisplayName: "One", ServiceID: "s1",
				EnabledNativeTools: []string{llm.NativeToolWebSearch},
			}}},
			minLevel: enterprise.LevelProfessional,
		},
		{
			name:     "EnableTokenUsageLogging newly true",
			next:     Config{EnableTokenUsageLogging: true},
			minLevel: enterprise.LevelProfessional,
		},
		{
			name: "EnableTokenUsageLogging turning off is accepted",
			prev: &Config{EnableTokenUsageLogging: true},
			next: Config{EnableTokenUsageLogging: false},
		},
		{
			name:     "AllowNativeWebSearchInChannels newly true",
			next:     Config{AllowNativeWebSearchInChannels: true},
			minLevel: enterprise.LevelProfessional,
		},
		{
			name:     "WebSearch.Enabled newly true",
			next:     Config{WebSearch: WebSearchConfig{Enabled: true}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name:     "EmbeddingSearchConfig newly enabled",
			next:     Config{EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{Type: embeddings.SearchTypeComposite}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name:     "EnableCallSummary newly true",
			next:     Config{EnableCallSummary: true},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name:     "TranscriptGenerator newly non-empty",
			next:     Config{TranscriptGenerator: "ai"},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name: "MCP server newly present",
			next: Config{MCP: MCPConfig{Servers: []MCPServerConfig{
				{ID: "mcp1", Name: "ext", Enabled: true, BaseURL: "https://mcp.example.com"},
			}}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name: "MCP server newly enabled",
			prev: &Config{MCP: MCPConfig{Servers: []MCPServerConfig{
				{ID: "mcp1", Name: "ext", Enabled: false, BaseURL: "https://mcp.example.com"},
			}}},
			next: Config{MCP: MCPConfig{Servers: []MCPServerConfig{
				{ID: "mcp1", Name: "ext", Enabled: true, BaseURL: "https://mcp.example.com"},
			}}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name: "MCP server turning off is accepted",
			prev: &Config{MCP: MCPConfig{Servers: []MCPServerConfig{
				{ID: "mcp1", Name: "ext", Enabled: true, BaseURL: "https://mcp.example.com"},
			}}},
			next: Config{MCP: MCPConfig{Servers: []MCPServerConfig{
				{ID: "mcp1", Name: "ext", Enabled: false, BaseURL: "https://mcp.example.com"},
			}}},
		},
		{
			name:     "EnablePluginServer newly true",
			next:     Config{MCP: MCPConfig{EnablePluginServer: true}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name: "ServiceAccountHeaders newly non-empty",
			prev: &Config{MCP: MCPConfig{Servers: []MCPServerConfig{
				{ID: "mcp1", Name: "ext", Enabled: true, BaseURL: "https://mcp.example.com"},
			}}},
			next: Config{MCP: MCPConfig{Servers: []MCPServerConfig{
				{ID: "mcp1", Name: "ext", Enabled: true, BaseURL: "https://mcp.example.com",
					ServiceAccountHeaders: map[string]string{"Authorization": "Bearer x"}},
			}}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name: "tool policy newly auto_run_in_dm",
			prev: &Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAsk, Enabled: true}},
			}}},
			next: Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAutoRunInDM, Enabled: true}},
			}}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name: "tool policy widened from DM-only to everywhere",
			prev: &Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAutoRunInDM, Enabled: true}},
			}}},
			next: Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAutoRunEverywhere, Enabled: true}},
			}}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name: "tool policy narrowed from everywhere to DM-only is accepted",
			prev: &Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAutoRunEverywhere, Enabled: true}},
			}}},
			next: Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAutoRunInDM, Enabled: true}},
			}}},
		},
		{
			name: "tool policy back to ask is accepted",
			prev: &Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAutoRunInDM, Enabled: true}},
			}}},
			next: Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAsk, Enabled: true}},
			}}},
		},
		{
			name: "tool Enabled transitions are never gated",
			prev: &Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAsk, Enabled: true}},
			}}},
			next: Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAsk, Enabled: false}},
			}}},
		},
		{
			name: "empty next from empty prev is accepted",
			next: Config{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, level := range enterprisetest.AllLevels {
				t.Run(level.String(), func(t *testing.T) {
					err := ValidateLicenseTransition(tc.prev, tc.next, enterprisetest.CheckerAt(level), nil)
					denied := tc.minLevel != 0 && level < tc.minLevel
					if !denied {
						require.NoError(t, err)
						return
					}
					require.Error(t, err)
					require.True(t, errors.Is(err, enterprise.ErrNotLicensed))
					if tc.quota {
						require.Contains(t, err.Error(), "AI agent")
						return
					}
					var licErr *enterprise.LicenseError
					require.True(t, errors.As(err, &licErr))
					require.Equal(t, tc.minLevel, licErr.RequiredLevel)
				})
			}

			t.Run("nil checker fails closed", func(t *testing.T) {
				err := ValidateLicenseTransition(tc.prev, tc.next, nil, nil)
				denied := tc.minLevel != 0
				if !denied {
					require.NoError(t, err)
					return
				}
				require.Error(t, err)
			})
		})
	}
}

func TestAccessControlsNewlyRestricted(t *testing.T) {
	open := llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAll, UserAccessLevel: llm.UserAccessLevelAll}
	restricted := llm.BotConfig{
		ChannelAccessLevel: llm.ChannelAccessLevelAllow,
		ChannelIDs:         []string{"ch1"},
	}
	blocked := func(userIDs ...string) llm.BotConfig {
		return llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAll, UserAccessLevel: llm.UserAccessLevelBlock, UserIDs: userIDs}
	}
	twoBlocked := blocked("u1", "u2")
	oneBlocked := blocked("u1")
	twoBlockedReordered := blocked("u2", "u1")

	tests := []struct {
		name string
		prev *llm.BotConfig
		next llm.BotConfig
		want bool
	}{
		{name: "create with open defaults", next: open, want: false},
		{name: "create with restrictions", next: restricted, want: true},
		{name: "unchanged restrictions", prev: &restricted, next: restricted, want: false},
		{name: "opening restrictions", prev: &restricted, next: open, want: false},
		{name: "newly restricted", prev: &open, next: restricted, want: true},
		{name: "removing an entry from a restricted list", prev: &twoBlocked, next: oneBlocked, want: false},
		{name: "reordering a restricted list", prev: &twoBlocked, next: twoBlockedReordered, want: false},
		{name: "retargeting a restricted list", prev: &restricted, next: llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAllow, ChannelIDs: []string{"ch2"}}, want: false},
		{name: "attribute-based is not named access controls", next: llm.BotConfig{UserAccessLevel: llm.UserAccessLevelAttributeBased}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, AccessControlsNewlyRestricted(tc.prev, tc.next))
		})
	}
}

// TestValidateLicenseTransitionSeededToolDefaults pins that vetted default
// policies seeded by the System Console are baseline, not admin-authored
// approval policies, while a policy above the seed stays gated.
func TestValidateLicenseTransitionSeededToolDefaults(t *testing.T) {
	seed := func(serverBaseURL, toolName string) string {
		if serverBaseURL == MCPEmbeddedServerOrigin && toolName == "read_post" {
			return MCPToolPolicyAutoRunInDM
		}
		return ""
	}
	tests := []struct {
		name     string
		next     Config
		minLevel enterprise.Level
	}{
		{
			name: "seeded default policy is accepted",
			next: Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAutoRunInDM, Enabled: true}},
			}}},
		},
		{
			name: "policy above the seeded default requires Enterprise",
			next: Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "read_post", Policy: MCPToolPolicyAutoRunEverywhere, Enabled: true}},
			}}},
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name: "unseeded tool with auto-run policy requires Enterprise",
			next: Config{MCP: MCPConfig{EmbeddedServer: MCPEmbeddedServerConfig{
				ToolConfigs: []MCPToolConfig{{Name: "create_post", Policy: MCPToolPolicyAutoRunInDM, Enabled: true}},
			}}},
			minLevel: enterprise.LevelEnterprise,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, level := range enterprisetest.AllLevels {
				err := ValidateLicenseTransition(nil, tc.next, enterprisetest.CheckerAt(level), seed)
				if tc.minLevel == 0 || level >= tc.minLevel {
					require.NoError(t, err, "level %s", level)
					continue
				}
				require.True(t, errors.Is(err, enterprise.ErrNotLicensed), "level %s", level)
			}
		})
	}
}
