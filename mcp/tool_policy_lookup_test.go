// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/stretchr/testify/require"
)

func TestLookupToolPolicy(t *testing.T) {
	const remoteURL = "https://remote.example.com/mcp"
	const pluginID = "com.example.demo"
	const pluginOrigin = "plugin://" + pluginID
	const pluginToolName = "com_example_demo__add"
	const remoteToolName = "remote_tool"

	pluginServerEnabled := func(toolPolicy string, toolEnabled bool) PluginServerConfig {
		return PluginServerConfig{
			PluginID: pluginID,
			Name:     "Demo Plugin",
			Enabled:  true,
			ToolConfigs: []ToolConfig{{
				Name:    pluginToolName,
				Policy:  toolPolicy,
				Enabled: toolEnabled,
			}},
		}
	}

	t.Run("plugin auto_run_everywhere enabled propagates", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{
				pluginServerEnabled(ToolPolicyAutoRunEverywhere, true),
			},
		}

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName, false)

		require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
		require.True(t, enabled)
	})

	t.Run("plugin auto_run_in_dm enabled propagates", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{
				pluginServerEnabled(ToolPolicyAutoRunInDM, true),
			},
		}

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName, false)

		require.Equal(t, ToolPolicyAutoRunInDM, policy)
		require.True(t, enabled)
	})

	t.Run("plugin tool disabled preserves configured policy", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{
				pluginServerEnabled(ToolPolicyAutoRunEverywhere, false),
			},
		}

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName, false)

		require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
		require.False(t, enabled)
	})

	t.Run("plugin server without tool configs defaults to ask true", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{{
				PluginID: pluginID,
				Name:     "Demo Plugin",
				Enabled:  true,
			}},
		}

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName, false)

		require.Equal(t, ToolPolicyAsk, policy)
		require.True(t, enabled)
	})

	t.Run("disabled plugin server returns ask false", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{{
				PluginID: pluginID,
				Name:     "Demo Plugin",
				Enabled:  false,
				ToolConfigs: []ToolConfig{{
					Name:    pluginToolName,
					Policy:  ToolPolicyAutoRunEverywhere,
					Enabled: true,
				}},
			}},
		}

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName, false)

		require.Equal(t, ToolPolicyAsk, policy)
		require.False(t, enabled)
	})

	t.Run("unknown plugin origin returns ask false", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{
				pluginServerEnabled(ToolPolicyAutoRunEverywhere, true),
			},
		}

		policy, enabled := LookupToolPolicy(cfg, "plugin://com.unknown.other", pluginToolName, false)

		require.Equal(t, ToolPolicyAsk, policy)
		require.False(t, enabled)
	})

	t.Run("remote server propagates configured policy", func(t *testing.T) {
		cfg := Config{
			Servers: []ServerConfig{{
				Name:    "Remote",
				Enabled: true,
				BaseURL: remoteURL,
				ToolConfigs: []ToolConfig{{
					Name:    remoteToolName,
					Policy:  ToolPolicyAutoRunEverywhere,
					Enabled: true,
				}},
			}},
		}

		policy, enabled := LookupToolPolicy(cfg, remoteURL, remoteToolName, false)

		require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
		require.True(t, enabled)
	})

	t.Run("remote namespaced tool matches bare configured policy", func(t *testing.T) {
		cfg := Config{
			Servers: []ServerConfig{{
				Name:    "Remote",
				Enabled: true,
				BaseURL: remoteURL,
				ToolConfigs: []ToolConfig{{
					Name:    remoteToolName,
					Policy:  ToolPolicyAutoRunEverywhere,
					Enabled: true,
				}},
			}},
		}

		policy, enabled := LookupToolPolicy(cfg, remoteURL, "remote__"+remoteToolName, false)

		require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
		require.True(t, enabled)
	})

	t.Run("embedded server with empty tool configs falls back to vetted seed", func(t *testing.T) {
		cfg := Config{
			EmbeddedServer: EmbeddedServerConfig{
				Enabled:     true,
				ToolConfigs: nil,
			},
		}

		seeds := SeedVettedToolConfigs(EmbeddedClientKey)
		if len(seeds) == 0 {
			t.Skip("no vetted seed tools available")
		}

		seedTool := seeds[0]
		policy, enabled := LookupToolPolicy(cfg, EmbeddedClientKey, seedTool.Name, false)

		require.NotEmpty(t, policy)
		require.Equal(t, seedTool.Enabled, enabled)
	})

	t.Run("embedded backfills seed policy for a tool missing from stored configs", func(t *testing.T) {
		// Non-empty stored configs without read_file (an install that saved
		// configs before read_file existed) must still get the vetted seed.
		cfg := Config{
			EmbeddedServer: EmbeddedServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{{
					Name:    "search_posts",
					Policy:  ToolPolicyAutoRunInDM,
					Enabled: true,
				}},
			},
		}

		policy, enabled := LookupToolPolicy(cfg, EmbeddedClientKey, "read_file", false)

		require.Equal(t, ToolPolicyAutoRunInDM, policy)
		require.True(t, enabled)
	})

	t.Run("embedded explicit config overrides the vetted seed", func(t *testing.T) {
		// An explicitly disabled tool must not be silently re-enabled by the seed.
		cfg := Config{
			EmbeddedServer: EmbeddedServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{{
					Name:    "read_file",
					Policy:  ToolPolicyAsk,
					Enabled: false,
				}},
			},
		}

		policy, enabled := LookupToolPolicy(cfg, EmbeddedClientKey, "read_file", false)

		require.Equal(t, ToolPolicyAsk, policy)
		require.False(t, enabled)
	})

	t.Run("unknown origin returns ask false", func(t *testing.T) {
		policy, enabled := LookupToolPolicy(Config{}, "bogus://nowhere", "x", false)

		require.Equal(t, ToolPolicyAsk, policy)
		require.False(t, enabled)
	})
}

func TestLookupEffectiveToolPolicy(t *testing.T) {
	const remoteURL = "https://remote.example.com/mcp"
	const pluginOrigin = "plugin://com.example.demo"

	cfg := Config{
		EmbeddedServer: EmbeddedServerConfig{
			Enabled: true,
			ToolConfigs: []ToolConfig{
				{Name: "read_file", Policy: ToolPolicyAutoRunEverywhere, Enabled: true},
				{Name: "read_post", Policy: ToolPolicyAsk, Enabled: true},
				{Name: "custom_unseeded", Policy: ToolPolicyAutoRunEverywhere, Enabled: true},
				{Name: "disabled_tool", Policy: ToolPolicyAutoRunEverywhere, Enabled: false},
			},
		},
		Servers: []ServerConfig{{
			Name:    "remote",
			Enabled: true,
			BaseURL: remoteURL,
			ToolConfigs: []ToolConfig{
				{Name: "remote_tool", Policy: ToolPolicyAutoRunEverywhere, Enabled: true},
				{Name: "remote_disabled", Policy: ToolPolicyAutoRunEverywhere, Enabled: false},
			},
		}},
		PluginServers: []PluginServerConfig{{
			PluginID: "com.example.demo",
			Name:     "Demo Plugin",
			Enabled:  true,
			ToolConfigs: []ToolConfig{
				{Name: "plugin_tool", Policy: ToolPolicyAutoRunEverywhere, Enabled: true},
			},
		}},
	}

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			licensed := level >= enterprise.LevelEnterprise

			policy, enabled := LookupEffectiveToolPolicy(cfg, EmbeddedClientKey, "read_file", false, licensed)
			require.True(t, enabled)
			if licensed {
				require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
			} else {
				require.Equal(t, ToolPolicyAutoRunInDM, policy, "embedded seed policy is used below Enterprise")
			}

			policy, enabled = LookupEffectiveToolPolicy(cfg, EmbeddedClientKey, "read_post", false, licensed)
			require.True(t, enabled)
			require.Equal(t, ToolPolicyAsk, policy, "an admin's narrower policy than the seed holds at every level")

			policy, enabled = LookupEffectiveToolPolicy(cfg, EmbeddedClientKey, "custom_unseeded", false, licensed)
			require.True(t, enabled)
			if licensed {
				require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
			} else {
				require.Equal(t, ToolPolicyAsk, policy, "unseeded embedded tools use ask below Enterprise")
			}

			_, enabled = LookupEffectiveToolPolicy(cfg, EmbeddedClientKey, "disabled_tool", false, licensed)
			require.False(t, enabled, "Enabled=false is honored at every level")

			policy, enabled = LookupEffectiveToolPolicy(cfg, remoteURL, "remote_tool", false, licensed)
			require.True(t, enabled)
			if licensed {
				require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
			} else {
				require.Equal(t, ToolPolicyAsk, policy)
			}

			_, enabled = LookupEffectiveToolPolicy(cfg, remoteURL, "remote_disabled", false, licensed)
			require.False(t, enabled, "remote Enabled=false is honored at every level")

			policy, enabled = LookupEffectiveToolPolicy(cfg, pluginOrigin, "plugin_tool", false, licensed)
			require.True(t, enabled)
			if licensed {
				require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
			} else {
				require.Equal(t, ToolPolicyAsk, policy)
			}
		})
	}

	t.Run("turning off a tool stays disabled when policies are available", func(t *testing.T) {
		policy, enabled := LookupEffectiveToolPolicy(cfg, EmbeddedClientKey, "disabled_tool", false, true)
		require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
		require.False(t, enabled)
	})
}

// Unconfigured tools take their default from the server's readOnlyHint, but an
// admin's stored config, the vetted seed, and the license cap all win over it.
func TestConfigToolPolicyCheckerReadOnlyHintDefault(t *testing.T) {
	const remoteURL = "https://mcp.example.com/mcp"
	const githubURL = "https://api.githubcopilot.com/mcp/"
	const figmaURL = "https://mcp.figma.com/mcp"
	pluginOrigin := pluginServerOriginKey("com.example.plugin")

	cfg := Config{
		Servers: []ServerConfig{
			{Name: "Remote", BaseURL: remoteURL, Enabled: true, ToolConfigs: []ToolConfig{
				{Name: "configured_ask", Policy: ToolPolicyAsk, Enabled: true},
				{Name: "configured_disabled", Policy: ToolPolicyAsk, Enabled: false},
				{Name: "toggled_only", Enabled: false},
				{Name: "toggled_write", Enabled: true},
			}},
			{Name: "GitHub", BaseURL: githubURL, Enabled: true},
			{Name: "Figma", BaseURL: figmaURL, Enabled: true},
			{Name: "Off", BaseURL: "https://off.example.com", Enabled: false},
		},
		PluginServers: []PluginServerConfig{
			{PluginID: "com.example.plugin", Name: "Plugin", Enabled: true},
		},
		EmbeddedServer: EmbeddedServerConfig{ToolConfigs: []ToolConfig{{Name: "read_post", Enabled: true}}},
	}

	tests := []struct {
		name         string
		origin       string
		toolName     string
		readOnlyHint bool
		licensed     bool
		wantPolicy   string
		wantEnabled  bool
	}{
		{"read-only remote tool auto-runs in DMs", remoteURL, "list_things", true, true, ToolPolicyAutoRunInDM, true},
		{"namespaced read-only remote tool auto-runs in DMs", remoteURL, "remote__list_things", true, true, ToolPolicyAutoRunInDM, true},
		{"remote tool without the hint asks", remoteURL, "delete_things", false, true, ToolPolicyAsk, true},
		{"stored ask wins over the hint", remoteURL, "configured_ask", true, true, ToolPolicyAsk, true},
		{"stored disabled stays disabled", remoteURL, "configured_disabled", true, true, ToolPolicyAsk, false},
		{"vetted seed applies without the hint", figmaURL, "whoami", false, true, ToolPolicyAutoRunInDM, true},
		{"GitHub read tool follows the hint", githubURL, "get_me", true, true, ToolPolicyAutoRunInDM, true},
		{"GitHub tool without the hint asks", githubURL, "create_issue", false, true, ToolPolicyAsk, true},
		{"empty stored policy keeps the hint default", remoteURL, "toggled_only", true, true, ToolPolicyAutoRunInDM, false},
		{"empty stored policy keeps the ask default", remoteURL, "toggled_write", false, true, ToolPolicyAsk, true},
		{"empty stored embedded policy keeps the seed", EmbeddedClientKey, "read_post", false, true, ToolPolicyAutoRunInDM, true},
		{"disabled server never auto-runs", "https://off.example.com", "list_things", true, true, ToolPolicyAsk, false},
		{"read-only plugin tool auto-runs in DMs", pluginOrigin, "list_things", true, true, ToolPolicyAutoRunInDM, true},
		{"embedded server ignores the hint", EmbeddedClientKey, "get_file_link", true, true, ToolPolicyAsk, true},
		{"unknown origin never auto-runs", "https://unknown.example.com", "list_things", true, true, ToolPolicyAsk, false},
		{"unlicensed policies ignore the hint", remoteURL, "list_things", true, false, ToolPolicyAsk, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewConfigToolPolicyChecker(func() Config { return cfg }, func() bool { return tt.licensed })

			policy, enabled := checker.GetToolPolicy(tt.origin, tt.toolName, tt.readOnlyHint)

			require.Equal(t, tt.wantPolicy, policy)
			require.Equal(t, tt.wantEnabled, enabled)
		})
	}
}
