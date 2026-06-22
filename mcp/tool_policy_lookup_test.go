// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

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

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName)

		require.Equal(t, ToolPolicyAutoRunEverywhere, policy)
		require.True(t, enabled)
	})

	t.Run("plugin auto_run_in_dm enabled propagates", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{
				pluginServerEnabled(ToolPolicyAutoRunInDM, true),
			},
		}

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName)

		require.Equal(t, ToolPolicyAutoRunInDM, policy)
		require.True(t, enabled)
	})

	t.Run("plugin tool disabled preserves configured policy", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{
				pluginServerEnabled(ToolPolicyAutoRunEverywhere, false),
			},
		}

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName)

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

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName)

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

		policy, enabled := LookupToolPolicy(cfg, pluginOrigin, pluginToolName)

		require.Equal(t, ToolPolicyAsk, policy)
		require.False(t, enabled)
	})

	t.Run("unknown plugin origin returns ask false", func(t *testing.T) {
		cfg := Config{
			PluginServers: []PluginServerConfig{
				pluginServerEnabled(ToolPolicyAutoRunEverywhere, true),
			},
		}

		policy, enabled := LookupToolPolicy(cfg, "plugin://com.unknown.other", pluginToolName)

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

		policy, enabled := LookupToolPolicy(cfg, remoteURL, remoteToolName)

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

		policy, enabled := LookupToolPolicy(cfg, remoteURL, "remote__"+remoteToolName)

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
		policy, enabled := LookupToolPolicy(cfg, EmbeddedClientKey, seedTool.Name)

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

		policy, enabled := LookupToolPolicy(cfg, EmbeddedClientKey, "read_file")

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

		policy, enabled := LookupToolPolicy(cfg, EmbeddedClientKey, "read_file")

		require.Equal(t, ToolPolicyAsk, policy)
		require.False(t, enabled)
	})

	t.Run("unknown origin returns ask false", func(t *testing.T) {
		policy, enabled := LookupToolPolicy(Config{}, "bogus://nowhere", "x")

		require.Equal(t, ToolPolicyAsk, policy)
		require.False(t, enabled)
	})
}
