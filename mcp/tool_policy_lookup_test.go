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

	remoteServerEnabled := Config{
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

	tests := []struct {
		name         string
		cfg          Config
		origin       string
		toolName     string
		wantPolicy   string
		wantEnabled  bool
		seedFallback bool
	}{
		{
			name:        "plugin auto_run_everywhere enabled propagates",
			cfg:         Config{PluginServers: []PluginServerConfig{pluginServerEnabled(ToolPolicyAutoRunEverywhere, true)}},
			origin:      pluginOrigin,
			toolName:    pluginToolName,
			wantPolicy:  ToolPolicyAutoRunEverywhere,
			wantEnabled: true,
		},
		{
			name:        "plugin auto_run_in_dm enabled propagates",
			cfg:         Config{PluginServers: []PluginServerConfig{pluginServerEnabled(ToolPolicyAutoRunInDM, true)}},
			origin:      pluginOrigin,
			toolName:    pluginToolName,
			wantPolicy:  ToolPolicyAutoRunInDM,
			wantEnabled: true,
		},
		{
			name:        "plugin tool disabled preserves configured policy",
			cfg:         Config{PluginServers: []PluginServerConfig{pluginServerEnabled(ToolPolicyAutoRunEverywhere, false)}},
			origin:      pluginOrigin,
			toolName:    pluginToolName,
			wantPolicy:  ToolPolicyAutoRunEverywhere,
			wantEnabled: false,
		},
		{
			name: "plugin server without tool configs defaults to ask true",
			cfg: Config{PluginServers: []PluginServerConfig{{
				PluginID: pluginID,
				Name:     "Demo Plugin",
				Enabled:  true,
			}}},
			origin:      pluginOrigin,
			toolName:    pluginToolName,
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: true,
		},
		{
			name: "disabled plugin server returns ask false",
			cfg: Config{PluginServers: []PluginServerConfig{{
				PluginID: pluginID,
				Name:     "Demo Plugin",
				Enabled:  false,
				ToolConfigs: []ToolConfig{{
					Name:    pluginToolName,
					Policy:  ToolPolicyAutoRunEverywhere,
					Enabled: true,
				}},
			}}},
			origin:      pluginOrigin,
			toolName:    pluginToolName,
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: false,
		},
		{
			name:        "unknown plugin origin returns ask false",
			cfg:         Config{PluginServers: []PluginServerConfig{pluginServerEnabled(ToolPolicyAutoRunEverywhere, true)}},
			origin:      "plugin://com.unknown.other",
			toolName:    pluginToolName,
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: false,
		},
		{
			name:        "remote server propagates configured policy",
			cfg:         remoteServerEnabled,
			origin:      remoteURL,
			toolName:    remoteToolName,
			wantPolicy:  ToolPolicyAutoRunEverywhere,
			wantEnabled: true,
		},
		{
			name:        "remote namespaced tool matches bare configured policy",
			cfg:         remoteServerEnabled,
			origin:      remoteURL,
			toolName:    "remote__" + remoteToolName,
			wantPolicy:  ToolPolicyAutoRunEverywhere,
			wantEnabled: true,
		},
		{
			name:        "plugin runtime name with server slug prefix matches advertised config name",
			cfg:         Config{PluginServers: []PluginServerConfig{pluginServerEnabled(ToolPolicyAutoRunEverywhere, true)}},
			origin:      pluginOrigin,
			toolName:    "cursor_cloud_agents__" + pluginToolName,
			wantPolicy:  ToolPolicyAutoRunEverywhere,
			wantEnabled: true,
		},
		{
			name:        "over-stripped native name does not match plugin-prefixed config",
			cfg:         Config{PluginServers: []PluginServerConfig{pluginServerEnabled(ToolPolicyAutoRunEverywhere, true)}},
			origin:      pluginOrigin,
			toolName:    "add",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: true,
		},
		{
			name: "embedded server with empty tool configs falls back to vetted seed",
			cfg: Config{
				EmbeddedServer: EmbeddedServerConfig{
					Enabled:     true,
					ToolConfigs: nil,
				},
			},
			origin:       EmbeddedClientKey,
			seedFallback: true,
		},
		{
			// Non-empty stored configs without read_file (an install that saved
			// configs before read_file existed) must still get the vetted seed.
			name: "embedded backfills seed policy for a tool missing from stored configs",
			cfg: Config{
				EmbeddedServer: EmbeddedServerConfig{
					Enabled: true,
					ToolConfigs: []ToolConfig{{
						Name:    "search_posts",
						Policy:  ToolPolicyAutoRunInDM,
						Enabled: true,
					}},
				},
			},
			origin:      EmbeddedClientKey,
			toolName:    "read_file",
			wantPolicy:  ToolPolicyAutoRunInDM,
			wantEnabled: true,
		},
		{
			// An explicitly disabled tool must not be silently re-enabled by the seed.
			name: "embedded explicit config overrides the vetted seed",
			cfg: Config{
				EmbeddedServer: EmbeddedServerConfig{
					Enabled: true,
					ToolConfigs: []ToolConfig{{
						Name:    "read_file",
						Policy:  ToolPolicyAsk,
						Enabled: false,
					}},
				},
			},
			origin:      EmbeddedClientKey,
			toolName:    "read_file",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: false,
		},
		{
			name:        "unknown origin returns ask false",
			cfg:         Config{},
			origin:      "bogus://nowhere",
			toolName:    "x",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolName := tt.toolName
			wantPolicy := tt.wantPolicy
			wantEnabled := tt.wantEnabled
			if tt.seedFallback {
				seeds := SeedVettedToolConfigs(EmbeddedClientKey)
				if len(seeds) == 0 {
					t.Skip("no vetted seed tools available")
				}
				toolName = seeds[0].Name
				wantEnabled = seeds[0].Enabled
			}

			policy, enabled := LookupToolPolicy(tt.cfg, tt.origin, toolName)

			if tt.seedFallback {
				require.NotEmpty(t, policy)
			} else {
				require.Equal(t, wantPolicy, policy)
			}
			require.Equal(t, wantEnabled, enabled)
		})
	}
}
