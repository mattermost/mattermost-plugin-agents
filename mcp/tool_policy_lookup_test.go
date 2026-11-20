// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
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

	t.Run("plugin server without tool configs defaults to ask true for non-readonly", func(t *testing.T) {
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

	t.Run("plugin server without tool configs defaults to auto_run_in_dm true for readOnlyHint", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{{
				PluginID: pluginID,
				Name:     "Demo Plugin",
				Enabled:  true,
			}},
		}

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName, true)

		require.Equal(t, ToolPolicyAutoRunInDM, policy)
		require.True(t, enabled)
	})

	t.Run("plugin explicit ask overrides readOnlyHint", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{
				pluginServerEnabled(ToolPolicyAsk, true),
			},
		}

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName, true)

		require.Equal(t, ToolPolicyAsk, policy)
		require.True(t, enabled)
	})

	t.Run("disabled plugin server returns ask false even with readOnlyHint", func(t *testing.T) {
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

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName, true)

		require.Equal(t, ToolPolicyAsk, policy)
		require.False(t, enabled)
	})

	t.Run("unknown plugin origin returns ask false even with readOnlyHint", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{
				pluginServerEnabled(ToolPolicyAutoRunEverywhere, true),
			},
		}

		policy, enabled := LookupToolPolicy(cfg, "plugin://com.unknown.other", pluginToolName, true)

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
		policy, enabled := LookupToolPolicy(Config{}, "bogus://nowhere", "x", true)

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

			policy, enabled = LookupEffectiveToolPolicy(cfg, pluginOrigin, "unconfigured_read_tool", true, licensed)
			require.True(t, enabled)
			if licensed {
				require.Equal(t, ToolPolicyAutoRunInDM, policy, "plugin readOnlyHint defaults to auto_run_in_dm at Enterprise+")
			} else {
				require.Equal(t, ToolPolicyAsk, policy, "plugin readOnlyHint is capped to ask below Enterprise")
			}
		})
	}

	t.Run("turning off a tool stays disabled when policies are available", func(t *testing.T) {
		policy, enabled := LookupEffectiveToolPolicy(cfg, EmbeddedClientKey, "disabled_tool", false, true)
		require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
		require.False(t, enabled)
	})
}

// Unconfigured remote and embedded tools ignore readOnlyHint at runtime;
// plugin-registered tools trust readOnlyHint as auto_run_in_dm when licensed,
// while stored admin policies and disabled flags always win.
func TestConfigToolPolicyCheckerDefaults(t *testing.T) {
	const remoteURL = "https://mcp.example.com/mcp"
	const figmaURL = "https://mcp.figma.com/mcp"
	pluginOrigin := pluginServerOriginKey("com.example.plugin")

	cfg := Config{
		Servers: []ServerConfig{
			{Name: "Remote", BaseURL: remoteURL, Enabled: true, ToolConfigs: []ToolConfig{
				{Name: "configured_dm", Policy: ToolPolicyAutoRunInDM, Enabled: true},
				{Name: "configured_disabled", Policy: ToolPolicyAsk, Enabled: false},
				{Name: "toggled_only", Enabled: false},
			}},
			{Name: "Figma", BaseURL: figmaURL, Enabled: true, ToolConfigs: []ToolConfig{
				{Name: "whoami", Enabled: true},
			}},
			{Name: "Off", BaseURL: "https://off.example.com", Enabled: false},
		},
		PluginServers: []PluginServerConfig{
			{PluginID: "com.example.plugin", Name: "Plugin", Enabled: true, ToolConfigs: []ToolConfig{
				{Name: "explicit_ask", Policy: ToolPolicyAsk, Enabled: true},
				{Name: "explicit_everywhere", Policy: ToolPolicyAutoRunEverywhere, Enabled: true},
				{Name: "toggled_off_readonly", Enabled: false},
				{Name: "override_only_readonly", Enabled: true, RetrievalDescriptionOverride: "Custom summary"},
			}},
			{PluginID: "com.example.disabled", Name: "Disabled Plugin", Enabled: false},
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
		{"unconfigured remote tool asks", remoteURL, "list_things", false, true, ToolPolicyAsk, true},
		{"unconfigured remote read-only tool still asks", remoteURL, "list_things", true, true, ToolPolicyAsk, true},
		{"stored policy applies", remoteURL, "configured_dm", false, true, ToolPolicyAutoRunInDM, true},
		{"namespaced name finds the stored policy", remoteURL, "remote__configured_dm", false, true, ToolPolicyAutoRunInDM, true},
		{"stored disabled stays disabled", remoteURL, "configured_disabled", false, true, ToolPolicyAsk, false},
		{"empty stored policy keeps the ask default", remoteURL, "toggled_only", false, true, ToolPolicyAsk, false},
		{"empty stored policy keeps the vetted seed", figmaURL, "whoami", false, true, ToolPolicyAutoRunInDM, true},
		{"empty stored embedded policy keeps the seed", EmbeddedClientKey, "read_post", false, true, ToolPolicyAutoRunInDM, true},
		{"unseeded embedded read-only tool still asks", EmbeddedClientKey, "get_file_link", true, true, ToolPolicyAsk, true},
		{"unconfigured plugin write tool asks", pluginOrigin, "mutate_things", false, true, ToolPolicyAsk, true},
		{"unconfigured plugin read-only tool defaults to auto_run_in_dm", pluginOrigin, "list_things", true, true, ToolPolicyAutoRunInDM, true},
		{"unconfigured plugin read-only tool capped to ask when unlicensed", pluginOrigin, "list_things", true, false, ToolPolicyAsk, true},
		{"explicit plugin ask overrides readOnlyHint", pluginOrigin, "explicit_ask", true, true, ToolPolicyAsk, true},
		{"explicit plugin auto_run_everywhere overrides non-readonly", pluginOrigin, "explicit_everywhere", false, true, ToolPolicyAutoRunEverywhere, true},
		{"plugin empty policy toggled off keeps readOnlyHint default and stays disabled", pluginOrigin, "toggled_off_readonly", true, true, ToolPolicyAutoRunInDM, false},
		{"plugin empty policy with retrieval override keeps readOnlyHint default", pluginOrigin, "override_only_readonly", true, true, ToolPolicyAutoRunInDM, true},
		{"disabled plugin server never auto-runs even with readOnlyHint", pluginServerOriginKey("com.example.disabled"), "list_things", true, true, ToolPolicyAsk, false},
		{"disabled server never auto-runs", "https://off.example.com", "list_things", true, true, ToolPolicyAsk, false},
		{"unknown origin never auto-runs", "https://unknown.example.com", "list_things", true, true, ToolPolicyAsk, false},
		{"unlicensed policies cap stored auto-run", remoteURL, "configured_dm", false, false, ToolPolicyAsk, true},
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

func TestEffectiveDefaultToolPolicy(t *testing.T) {
	const remoteURL = "https://mcp.example.com/mcp"
	const figmaURL = "https://mcp.figma.com/mcp"
	pluginOrigin := pluginServerOriginKey("com.example.plugin")

	tests := []struct {
		name         string
		origin       string
		toolName     string
		readOnlyHint bool
		licensed     bool
		want         string
	}{
		{"remote read-only stays ask", remoteURL, "list_things", true, true, ToolPolicyAsk},
		{"vetted remote seed defaults to auto_run_in_dm when licensed", figmaURL, "whoami", false, true, ToolPolicyAutoRunInDM},
		{"vetted remote seed capped to ask when unlicensed", figmaURL, "whoami", false, false, ToolPolicyAsk},
		{"plugin read-only defaults to auto_run_in_dm when licensed", pluginOrigin, "list_things", true, true, ToolPolicyAutoRunInDM},
		{"plugin read-only capped to ask when unlicensed", pluginOrigin, "list_things", true, false, ToolPolicyAsk},
		{"plugin write tool defaults to ask", pluginOrigin, "delete_things", false, true, ToolPolicyAsk},
		{"empty plugin ID origin fails closed to ask", "plugin://", "list_things", true, true, ToolPolicyAsk},
		{"embedded unseeded read-only stays ask", EmbeddedClientKey, "get_file_link", true, true, ToolPolicyAsk},
		{"embedded seeded read tool defaults to auto_run_in_dm even unlicensed", EmbeddedClientKey, "read_post", false, false, ToolPolicyAutoRunInDM},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, EffectiveDefaultToolPolicy(tt.origin, tt.toolName, tt.readOnlyHint, tt.licensed))
		})
	}
}

func TestCollectToolsFromSnapshotsCapturesReadOnlyHint(t *testing.T) {
	pluginOrigin := pluginServerOriginKey("com.example.plugin")
	client := &Client{
		config: ServerConfig{
			Name:    "Plugin",
			BaseURL: pluginOrigin,
		},
		tools: map[string]*gomcp.Tool{
			"read_item": {
				Name:        "read_item",
				Description: "Read item",
				Annotations: &gomcp.ToolAnnotations{ReadOnlyHint: true},
			},
			"write_item": {
				Name:        "write_item",
				Description: "Write item",
				Annotations: &gomcp.ToolAnnotations{ReadOnlyHint: false},
			},
			"unannotated_item": {
				Name:        "unannotated_item",
				Description: "Unannotated item",
			},
		},
	}
	owner := &UserClients{userID: "user-1"}

	tools := collectToolsFromSnapshots("user-1", pluginapi.LogService{}, []userClientSnapshot{{
		serverID: pluginOrigin,
		client:   client,
		owner:    owner,
	}})

	hintsByBare := make(map[string]bool, len(tools))
	for _, tool := range tools {
		hintsByBare[llm.BareMCPToolName(tool.Name)] = tool.ReadOnlyHint
	}

	require.Equal(t, map[string]bool{
		"read_item":        true,
		"write_item":       false,
		"unannotated_item": false,
	}, hintsByBare)
}

// SuggestedToolPolicy pre-selects auto_run_in_dm for readOnlyHint tools on
// remote and plugin servers when licensed; the vetted seed, the embedded
// server's curated list and the license still win.
func TestSuggestedToolPolicy(t *testing.T) {
	const remoteURL = "https://mcp.example.com/mcp"
	const figmaURL = "https://mcp.figma.com/mcp"

	tests := []struct {
		name         string
		origin       string
		toolName     string
		readOnlyHint bool
		licensed     bool
		want         string
	}{
		{"read-only remote tool suggests auto-run in DMs", remoteURL, "list_things", true, true, ToolPolicyAutoRunInDM},
		{"remote tool without the hint suggests ask", remoteURL, "delete_things", false, true, ToolPolicyAsk},
		{"vetted seed is suggested without the hint", figmaURL, "whoami", false, true, ToolPolicyAutoRunInDM},
		{"read-only plugin tool suggests auto-run in DMs", pluginServerOriginKey("com.example.plugin"), "list_things", true, true, ToolPolicyAutoRunInDM},
		{"unlicensed plugin read-only tool suggests ask", pluginServerOriginKey("com.example.plugin"), "list_things", true, false, ToolPolicyAsk},
		{"embedded server ignores the hint", EmbeddedClientKey, "get_file_link", true, true, ToolPolicyAsk},
		{"embedded server suggests its seed", EmbeddedClientKey, "read_post", false, true, ToolPolicyAutoRunInDM},
		{"unlicensed policies suggest ask", remoteURL, "list_things", true, false, ToolPolicyAsk},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, SuggestedToolPolicy(tt.origin, tt.toolName, tt.readOnlyHint, tt.licensed))
		})
	}
}
