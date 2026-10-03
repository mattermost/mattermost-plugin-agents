// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

func TestClientManagerRemoteMCPLicenseGate(t *testing.T) {
	pluginTestAPI := &plugintest.API{}
	setupClientManagerTestAPI(t, pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)

	const remoteURL = "https://remote.example.com/mcp"
	const pluginID = "com.example.demo"

	cfg := Config{
		IdleTimeoutMinutes: 30,
		EmbeddedServer:     EmbeddedServerConfig{Enabled: true},
		Servers: []ServerConfig{{
			Name:    "Remote",
			Enabled: true,
			BaseURL: remoteURL,
		}},
		PluginServers: []PluginServerConfig{{
			PluginID: pluginID,
			Name:     "Demo",
			Enabled:  true,
		}},
	}
	plugins := []PluginServerConfig{{
		PluginID: pluginID,
		Name:     "Demo",
		Enabled:  true,
	}}

	m := NewClientManager(cfg, client.Log, client, nil, nil, nil, nil, RemoteMCPAlwaysAllowed)
	t.Cleanup(m.Close)
	m.RegisterPluginServer(plugins[0])

	t.Run("default allows remotes so existing tests keep connecting", func(t *testing.T) {
		resolved := m.resolveEligibleServers(cfg, nil, plugins, ToolSelection{}, nil, false)
		require.Len(t, resolved.remote, 1)
		require.Len(t, resolved.plugins, 1)
	})

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			checker := enterprisetest.CheckerAt(level)
			m.remoteAllowed = func() bool {
				return checker.Allows(enterprise.CapRemoteMCP)
			}
			m.ReInit(cfg, nil)

			resolved := m.resolveEligibleServers(cfg, nil, plugins, ToolSelection{}, nil, false)
			identities := m.liveOriginIdentities(cfg, nil, false)
			require.Contains(t, identities, EmbeddedClientKey, "embedded server stays connected at every level")

			if level >= enterprise.LevelEnterprise {
				require.Len(t, resolved.remote, 1)
				require.Len(t, resolved.plugins, 1)
				require.Contains(t, identities, remoteURL)
				require.Contains(t, identities, pluginServerOriginKey(pluginID))
				return
			}

			require.Empty(t, resolved.remote)
			require.Empty(t, resolved.plugins)
			require.NotContains(t, identities, remoteURL)
			require.NotContains(t, identities, pluginServerOriginKey(pluginID))
		})
	}

	t.Run("nil remoteAllowed fails closed", func(t *testing.T) {
		m.remoteAllowed = nil
		resolved := m.resolveEligibleServers(cfg, nil, plugins, ToolSelection{}, nil, false)
		require.Empty(t, resolved.remote)
		require.Empty(t, resolved.plugins)
		identities := m.liveOriginIdentities(cfg, nil, false)
		require.Contains(t, identities, EmbeddedClientKey)
		require.NotContains(t, identities, remoteURL)
	})

	t.Run("turning remotes off skips previously eligible servers", func(t *testing.T) {
		m.remoteAllowed = func() bool { return true }
		require.NotEmpty(t, m.resolveEligibleServers(cfg, nil, plugins, ToolSelection{}, nil, false).remote)
		m.remoteAllowed = func() bool { return false }
		require.Empty(t, m.resolveEligibleServers(cfg, nil, plugins, ToolSelection{}, nil, false).remote)
		require.Empty(t, m.resolveEligibleServers(cfg, nil, plugins, ToolSelection{}, nil, false).plugins)
	})
}
