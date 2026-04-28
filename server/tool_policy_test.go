// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/stretchr/testify/assert"
)

func TestLookupToolPolicy_PluginServer_AutoRunEverywhere(t *testing.T) {
	const pluginID = "com.mattermost.plugin-demo"
	const toolName = "com_mattermost_plugin-demo__add_two_numbers"

	cfg := config.MCPConfig{
		PluginServers: []config.PluginServerConfig{{
			PluginID: pluginID,
			Name:     "Demo Plugin",
			Enabled:  true,
			ToolConfigs: []config.MCPToolConfig{{
				Name:    toolName,
				Policy:  config.MCPToolPolicyAutoRunEverywhere,
				Enabled: true,
			}},
		}},
	}

	policy, enabled := lookupToolPolicy(cfg, "plugin://"+pluginID, toolName)

	assert.Equal(t, config.MCPToolPolicyAutoRunEverywhere, policy,
		"plugin tool with admin-set auto_run_everywhere must report that policy")
	assert.True(t, enabled, "plugin tool with admin Enabled=true must report enabled=true")
}

// TestLookupToolPolicy covers embedded, remote, plugin, and unknown origins.
func TestLookupToolPolicy(t *testing.T) {
	const remoteURL = "https://remote.example.com/mcp"
	const pluginID = "com.example.demo"
	const pluginOrigin = "plugin://" + pluginID
	const pluginToolName = "com_example_demo__add"
	const remoteToolName = "remote_tool"

	pluginServerEnabled := func(toolPolicy string, toolEnabled bool) config.PluginServerConfig {
		return config.PluginServerConfig{
			PluginID: pluginID,
			Name:     "Demo Plugin",
			Enabled:  true,
			ToolConfigs: []config.MCPToolConfig{{
				Name:    pluginToolName,
				Policy:  toolPolicy,
				Enabled: toolEnabled,
			}},
		}
	}

	t.Run("plugin auto_run_everywhere enabled -> propagates", func(t *testing.T) {
		cfg := config.MCPConfig{
			PluginServers: []config.PluginServerConfig{
				pluginServerEnabled(config.MCPToolPolicyAutoRunEverywhere, true),
			},
		}
		policy, enabled := lookupToolPolicy(cfg, pluginOrigin, pluginToolName)
		assert.Equal(t, config.MCPToolPolicyAutoRunEverywhere, policy)
		assert.True(t, enabled)
	})

	t.Run("plugin auto_run_in_dm enabled -> propagates", func(t *testing.T) {
		cfg := config.MCPConfig{
			PluginServers: []config.PluginServerConfig{
				pluginServerEnabled(config.MCPToolPolicyAutoRunInDM, true),
			},
		}
		policy, enabled := lookupToolPolicy(cfg, pluginOrigin, pluginToolName)
		assert.Equal(t, config.MCPToolPolicyAutoRunInDM, policy)
		assert.True(t, enabled)
	})

	t.Run("plugin tool Enabled=false -> reports configured policy with enabled=false", func(t *testing.T) {
		cfg := config.MCPConfig{
			PluginServers: []config.PluginServerConfig{
				pluginServerEnabled(config.MCPToolPolicyAutoRunEverywhere, false),
			},
		}
		policy, enabled := lookupToolPolicy(cfg, pluginOrigin, pluginToolName)
		assert.Equal(t, config.MCPToolPolicyAutoRunEverywhere, policy,
			"configured policy is preserved even when the tool entry is disabled")
		assert.False(t, enabled, "tool Enabled=false must surface as enabled=false (this is the gate)")
	})

	t.Run("plugin server matched but no ToolConfigs -> default-allow ask", func(t *testing.T) {
		cfg := config.MCPConfig{
			PluginServers: []config.PluginServerConfig{{
				PluginID: pluginID,
				Name:     "Demo Plugin",
				Enabled:  true,
			}},
		}
		policy, enabled := lookupToolPolicy(cfg, pluginOrigin, pluginToolName)
		assert.Equal(t, config.MCPToolPolicyAsk, policy)
		assert.True(t, enabled, "unconfigured plugin tools must default to enabled with ask policy")
	})

	t.Run("plugin server Enabled=false -> ask, false", func(t *testing.T) {
		cfg := config.MCPConfig{
			PluginServers: []config.PluginServerConfig{{
				PluginID: pluginID,
				Name:     "Demo Plugin",
				Enabled:  false,
				ToolConfigs: []config.MCPToolConfig{{
					Name:    pluginToolName,
					Policy:  config.MCPToolPolicyAutoRunEverywhere,
					Enabled: true,
				}},
			}},
		}
		policy, enabled := lookupToolPolicy(cfg, pluginOrigin, pluginToolName)
		assert.Equal(t, config.MCPToolPolicyAsk, policy)
		assert.False(t, enabled,
			"a disabled plugin server must never auto-execute even if a tool's policy says auto_run_everywhere")
	})

	t.Run("plugin origin with no matching PluginID -> ask, false", func(t *testing.T) {
		cfg := config.MCPConfig{
			PluginServers: []config.PluginServerConfig{
				pluginServerEnabled(config.MCPToolPolicyAutoRunEverywhere, true),
			},
		}
		policy, enabled := lookupToolPolicy(cfg, "plugin://com.unknown.other", pluginToolName)
		assert.Equal(t, config.MCPToolPolicyAsk, policy)
		assert.False(t, enabled)
	})

	t.Run("remote server auto_run_everywhere -> propagates", func(t *testing.T) {
		cfg := config.MCPConfig{
			Servers: []config.MCPServerConfig{{
				Name:    "Remote",
				Enabled: true,
				BaseURL: remoteURL,
				ToolConfigs: []config.MCPToolConfig{{
					Name:    remoteToolName,
					Policy:  config.MCPToolPolicyAutoRunEverywhere,
					Enabled: true,
				}},
			}},
		}
		policy, enabled := lookupToolPolicy(cfg, remoteURL, remoteToolName)
		assert.Equal(t, config.MCPToolPolicyAutoRunEverywhere, policy)
		assert.True(t, enabled)
	})

	t.Run("embedded server with empty ToolConfigs falls back to vetted seed", func(t *testing.T) {
		cfg := config.MCPConfig{
			EmbeddedServer: config.MCPEmbeddedServerConfig{
				Enabled:     true,
				ToolConfigs: nil,
			},
		}
		seeds := mcp.SeedVettedToolConfigs(mcp.EmbeddedClientKey)
		if len(seeds) == 0 {
			t.Skip("no vetted seed tools available; skip regression pin")
		}
		seedTool := seeds[0]
		policy, enabled := lookupToolPolicy(cfg, mcp.EmbeddedClientKey, seedTool.Name)
		assert.NotEmpty(t, policy)
		assert.Equal(t, seedTool.Enabled, enabled,
			"embedded seed fallback must reflect the seed entry's Enabled flag")
	})

	t.Run("unknown origin -> ask, false", func(t *testing.T) {
		cfg := config.MCPConfig{}
		policy, enabled := lookupToolPolicy(cfg, "bogus://nowhere", "x")
		assert.Equal(t, config.MCPToolPolicyAsk, policy)
		assert.False(t, enabled)
	})
}
