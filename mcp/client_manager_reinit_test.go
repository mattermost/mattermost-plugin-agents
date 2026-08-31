// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClientManagerReInitPreservesUnchangedOrigins(t *testing.T) {
	type mutateFn func(t *testing.T, harness *runtimeHarness, manager *ClientManager)

	testCases := []struct {
		name        string
		mutate      mutateFn
		preserved   []string
		invalidated []string
	}{
		{
			name: "tool policy only",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				cfg.Servers[0].ToolConfigs = []ToolConfig{{Name: "keep_0", Policy: ToolPolicyAsk, Enabled: true}}
				cfg.Servers[1].ToolConfigs = []ToolConfig{{Name: "change_0", Policy: ToolPolicyAsk, Enabled: false}}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved: []string{"keep", "change", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
		},
		{
			name: "idle timeout only",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				cfg.IdleTimeoutMinutes = 12
				manager.ReInit(cfg, manager.GetEmbeddedServer())
				require.Equal(t, 12*time.Minute, manager.clientTimeout)
			},
			preserved: []string{"keep", "change", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
		},
		{
			name: "unchanged config",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				manager.ReInit(cloneMCPConfig(manager.GetConfig()), manager.GetEmbeddedServer())
			},
			preserved: []string{"keep", "change", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
		},
		{
			name: "unrelated origin change",
			mutate: func(t *testing.T, harness *runtimeHarness, manager *ClientManager) {
				other := harness.addRemote("other", 1, 0)
				cfg := cloneMCPConfig(manager.GetConfig())
				cfg.Servers = append(cfg.Servers, other)
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved: []string{"keep", "change", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
		},
		{
			name: "remote URL change",
			mutate: func(t *testing.T, harness *runtimeHarness, manager *ClientManager) {
				replacement := harness.addRemote("replacement", 1, 0)
				cfg := cloneMCPConfig(manager.GetConfig())
				for i := range cfg.Servers {
					if cfg.Servers[i].Name == "change" {
						cfg.Servers[i].BaseURL = replacement.BaseURL
					}
				}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
			invalidated: []string{"change"},
		},
		{
			name: "remote name change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				for i := range cfg.Servers {
					if cfg.Servers[i].Name == "change" {
						cfg.Servers[i].Name = "renamed"
					}
				}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
			invalidated: []string{"change"},
		},
		{
			name: "remote header change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				for i := range cfg.Servers {
					if cfg.Servers[i].Name == "change" {
						cfg.Servers[i].Headers = map[string]string{"X-Test": "1"}
					}
				}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
			invalidated: []string{"change"},
		},
		{
			name: "remote cosmetic URL spelling change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				for i := range cfg.Servers {
					if cfg.Servers[i].Name == "change" {
						cfg.Servers[i].BaseURL = strings.TrimRight(cfg.Servers[i].BaseURL, "/") + "/"
					}
				}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
			invalidated: []string{"change"},
		},
		{
			name: "remote client credentials change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				for i := range cfg.Servers {
					if cfg.Servers[i].Name == "change" {
						cfg.Servers[i].ClientID = "new-client"
						cfg.Servers[i].ClientSecret = "new-secret"
					}
				}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
			invalidated: []string{"change"},
		},
		{
			name: "remote enabled change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				for i := range cfg.Servers {
					if cfg.Servers[i].Name == "change" {
						cfg.Servers[i].Enabled = false
					}
				}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
			invalidated: []string{"change"},
		},
		{
			name: "remote duplicate-conflict status change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				var changeURL string
				for _, server := range cfg.Servers {
					if server.Name == "change" {
						changeURL = server.BaseURL
					}
				}
				cfg.Servers = append(cfg.Servers, ServerConfig{Name: "duplicate", BaseURL: changeURL, Enabled: true})
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
			invalidated: []string{"change"},
		},
		{
			name: "plugin path change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				current, ok := manager.GetPluginServer("com.example.change")
				require.True(t, ok)
				current.Path = "/other-mcp"
				manager.RegisterPluginServer(current)
				manager.ReInit(cloneMCPConfig(manager.GetConfig()), manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "change", "plugin://com.example.keep", EmbeddedClientKey},
			invalidated: []string{"plugin://com.example.change"},
		},
		{
			name: "plugin name change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				current, ok := manager.GetPluginServer("com.example.change")
				require.True(t, ok)
				current.Name = "renamed-plugin"
				manager.RegisterPluginServer(current)
				manager.ReInit(cloneMCPConfig(manager.GetConfig()), manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "change", "plugin://com.example.keep", EmbeddedClientKey},
			invalidated: []string{"plugin://com.example.change"},
		},
		{
			name: "plugin enabled change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				for i := range cfg.PluginServers {
					if cfg.PluginServers[i].PluginID == "com.example.change" {
						cfg.PluginServers[i].Enabled = false
					}
				}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "change", "plugin://com.example.keep", EmbeddedClientKey},
			invalidated: []string{"plugin://com.example.change"},
		},
		{
			name: "plugin registration change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				manager.UnregisterPluginServer("com.example.change")
				manager.ReInit(cloneMCPConfig(manager.GetConfig()), manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "change", "plugin://com.example.keep", EmbeddedClientKey},
			invalidated: []string{"plugin://com.example.change"},
		},
		{
			name: "plugin tool configs and expose external only",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				for i := range cfg.PluginServers {
					if cfg.PluginServers[i].PluginID == "com.example.change" {
						cfg.PluginServers[i].ToolConfigs = []ToolConfig{{Name: "plugin-change_0", Policy: ToolPolicyAsk, Enabled: true}}
						cfg.PluginServers[i].ExposeExternal = true
					}
				}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved: []string{"keep", "change", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
		},
		{
			name: "embedded unchanged",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				cfg.EmbeddedServer.ToolConfigs = []ToolConfig{{Name: "embedded_tool", Policy: ToolPolicyAsk, Enabled: true}}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved: []string{"keep", "change", "plugin://com.example.keep", "plugin://com.example.change", EmbeddedClientKey},
		},
		{
			name: "embedded disabled",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				cfg := cloneMCPConfig(manager.GetConfig())
				cfg.EmbeddedServer.Enabled = false
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			},
			preserved:   []string{"keep", "change", "plugin://com.example.keep", "plugin://com.example.change"},
			invalidated: []string{EmbeddedClientKey},
		},
		{
			name: "embedded server replaced",
			mutate: func(t *testing.T, harness *runtimeHarness, manager *ClientManager) {
				replacement := &delayedEmbeddedServer{
					ctx:    harness.embedded.ctx,
					server: newTestMCPServer(0, "embedded_tool"),
				}
				manager.ReInit(cloneMCPConfig(manager.GetConfig()), replacement)
			},
			preserved:   []string{"keep", "change", "plugin://com.example.keep", "plugin://com.example.change"},
			invalidated: []string{EmbeddedClientKey},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			harness := newRuntimeHarness(t, "alice")
			harness.addRemote("keep", 1, 0)
			harness.addRemote("change", 1, 0)
			harness.addPlugin("com.example.keep", "plugin-keep", 1, 0)
			harness.addPlugin("com.example.change", "plugin-change", 1, 0)
			harness.withEmbedded("embedded_tool", 0)
			manager := harness.newManager()

			tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
			require.Nil(t, mcpErrors)
			require.Len(t, tools, 5)

			before := map[string]*Client{
				"keep":                        cachedUserClient(manager, "alice", "keep"),
				"change":                      cachedUserClient(manager, "alice", "change"),
				"plugin://com.example.keep":   cachedUserClient(manager, "alice", "plugin://com.example.keep"),
				"plugin://com.example.change": cachedUserClient(manager, "alice", "plugin://com.example.change"),
				EmbeddedClientKey:             cachedUserClient(manager, "alice", EmbeddedClientKey),
			}
			for serverID, client := range before {
				require.NotNil(t, client, "precondition: %s must be connected", serverID)
			}

			keepDials := harness.remoteSrv["keep"].dialCount()
			changeDials := harness.remoteSrv["change"].dialCount()
			pluginKeepDials := harness.pluginSrv["com.example.keep"].dialCount()
			pluginChangeDials := harness.pluginSrv["com.example.change"].dialCount()
			embeddedDials := harness.embedded.transports.Load()

			tc.mutate(t, harness, manager)

			for _, serverID := range tc.preserved {
				require.Same(t, before[serverID], cachedUserClient(manager, "alice", serverID),
					"unaffected origin %s must keep the same session pointer", serverID)
			}
			for _, serverID := range tc.invalidated {
				require.Nil(t, cachedUserClient(manager, "alice", serverID),
					"affected origin %s must be closed and removed", serverID)
			}

			_, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
			require.Nil(t, mcpErrors)

			require.Equal(t, keepDials, harness.remoteSrv["keep"].dialCount(), "unaffected remote must not be redialed")
			require.Equal(t, pluginKeepDials, harness.pluginSrv["com.example.keep"].dialCount(), "unaffected plugin must not be redialed")
			if tc.name != "embedded server replaced" && tc.name != "embedded disabled" {
				require.Equal(t, embeddedDials, harness.embedded.transports.Load(), "unaffected embedded session must not be redialed")
			}

			switch tc.name {
			case "remote URL change":
				require.NotNil(t, cachedUserClient(manager, "alice", "change"))
				require.NotSame(t, before["change"], cachedUserClient(manager, "alice", "change"))
				require.Equal(t, 1, harness.remoteSrv["replacement"].dialCount())
			case "remote name change":
				require.NotNil(t, cachedUserClient(manager, "alice", "renamed"))
				require.Greater(t, harness.remoteSrv["change"].dialCount(), changeDials)
			case "remote header change", "remote client credentials change", "remote cosmetic URL spelling change":
				require.NotNil(t, cachedUserClient(manager, "alice", "change"))
				require.NotSame(t, before["change"], cachedUserClient(manager, "alice", "change"))
				require.Greater(t, harness.remoteSrv["change"].dialCount(), changeDials)
			case "plugin path change", "plugin name change":
				require.NotNil(t, cachedUserClient(manager, "alice", "plugin://com.example.change"))
				require.NotSame(t, before["plugin://com.example.change"], cachedUserClient(manager, "alice", "plugin://com.example.change"))
				require.Greater(t, harness.pluginSrv["com.example.change"].dialCount(), pluginChangeDials)
			case "plugin registration change":
				require.Nil(t, cachedUserClient(manager, "alice", "plugin://com.example.change"),
					"an unregistered plugin must not reconnect")
				require.Equal(t, pluginChangeDials, harness.pluginSrv["com.example.change"].dialCount(),
					"an unregistered plugin must not be redialed")
			case "embedded server replaced":
				require.NotNil(t, cachedUserClient(manager, "alice", EmbeddedClientKey))
				require.NotSame(t, before[EmbeddedClientKey], cachedUserClient(manager, "alice", EmbeddedClientKey))
			}
		})
	}
}

func TestClientManagerReInitReconnectsInvalidatedOriginOnce(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	harness.addRemote("keep", 1, 0)
	change := harness.addRemote("change", 1, 0)
	manager := harness.newManager()

	_, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	original := cachedUserClient(manager, "alice", "change")
	require.NotNil(t, original)

	cfg := cloneMCPConfig(manager.GetConfig())
	for i := range cfg.Servers {
		if cfg.Servers[i].Name == "change" {
			cfg.Servers[i].Headers = map[string]string{"X-Reinit": "1"}
		}
	}
	manager.ReInit(cfg, manager.GetEmbeddedServer())
	require.Nil(t, cachedUserClient(manager, "alice", "change"))

	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	reconnected := cachedUserClient(manager, "alice", "change")
	require.NotNil(t, reconnected)
	require.NotSame(t, original, reconnected)
	require.Equal(t, 2, harness.remoteSrv["change"].dialCount())
	require.Equal(t, 1, harness.remoteSrv["keep"].dialCount())
	require.Contains(t, toolOrigins(tools), change.BaseURL)

	_, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Same(t, reconnected, cachedUserClient(manager, "alice", "change"))
	require.Equal(t, 2, harness.remoteSrv["change"].dialCount())
}

func TestReInitDiscardsInFlightInvalidatedOrigin(t *testing.T) {
	for i := range 20 {
		t.Run(fmt.Sprintf("attempt-%d", i), func(t *testing.T) {
			harness := newRuntimeHarness(t, "alice")
			first := harness.addRemote("first", 1, 250*time.Millisecond)
			manager := harness.newManager()

			done := make(chan struct{})
			go func() {
				defer close(done)
				manager.GetToolsForUser(context.Background(), "alice", ToolSelection{
					AllowedOrigins: []string{first.BaseURL},
				})
			}()

			require.Eventually(t, func() bool {
				return harness.remoteSrv["first"].requestCount() > 0
			}, 2*time.Second, 5*time.Millisecond)

			cfg := cloneMCPConfig(manager.GetConfig())
			for i := range cfg.Servers {
				if cfg.Servers[i].Name == "first" {
					cfg.Servers[i].ClientSecret = "rotated"
				}
			}
			manager.ReInit(cfg, manager.GetEmbeddedServer())

			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("in-flight connect did not finish after ReInit")
			}
			require.Nil(t, cachedUserClient(manager, "alice", "first"),
				"an origin invalidated during dial must not commit a stale session")

			tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{
				AllowedOrigins: []string{first.BaseURL},
			})
			require.Nil(t, mcpErrors)
			require.Equal(t, map[string]int{first.BaseURL: 1}, toolOrigins(tools))
			require.NotNil(t, cachedUserClient(manager, "alice", "first"))
			require.GreaterOrEqual(t, harness.remoteSrv["first"].dialCount(), 1)
		})
	}
}

func TestUnregisterPluginServerStopsReconnectUntilReregister(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	harness.addRemote("remotea", 1, 0)
	plugin := harness.addPlugin("com.example.plug", "plugin", 1, 0)
	manager := harness.newManager()

	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{harness.remote[0].BaseURL: 1, pluginServerOriginKey(plugin.PluginID): 1}, toolOrigins(tools))
	require.Equal(t, 1, harness.pluginSrv[plugin.PluginID].dialCount())
	original := cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID))
	require.NotNil(t, original)

	dialsBeforeUnregister := harness.pluginSrv[plugin.PluginID].dialCount()
	requestsBeforeUnregister := harness.pluginSrv[plugin.PluginID].requestCount()

	manager.UnregisterPluginServer(plugin.PluginID)
	require.Nil(t, cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID)))
	require.False(t, manager.IsPluginRegistered(plugin.PluginID))

	tools, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{harness.remote[0].BaseURL: 1}, toolOrigins(tools))
	require.Nil(t, cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID)))
	require.Equal(t, dialsBeforeUnregister, harness.pluginSrv[plugin.PluginID].dialCount(),
		"unregister must not start a new plugin connection")
	require.Equal(t, requestsBeforeUnregister, harness.pluginSrv[plugin.PluginID].requestCount(),
		"unregister must not contact the plugin server")

	manager.RegisterPluginServer(plugin)
	require.True(t, manager.IsPluginRegistered(plugin.PluginID))
	require.Nil(t, cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID)),
		"re-register must not resurrect the previous session")

	tools, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{harness.remote[0].BaseURL: 1, pluginServerOriginKey(plugin.PluginID): 1}, toolOrigins(tools))
	fresh := cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID))
	require.NotNil(t, fresh)
	require.NotSame(t, original, fresh)
	require.Equal(t, 2, harness.pluginSrv[plugin.PluginID].dialCount(),
		"re-register must open exactly one fresh connection")

	_, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Same(t, fresh, cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID)))
	require.Equal(t, 2, harness.pluginSrv[plugin.PluginID].dialCount())
}

func TestRegisterPluginServerIdentityChangeInvalidatesWithoutReInit(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	plugin := harness.addPlugin("com.example.plug", "plugin", 1, 0)
	manager := harness.newManager()

	_, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	original := cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID))
	require.NotNil(t, original)

	updated := plugin
	updated.Path = "/other-mcp"
	manager.RegisterPluginServer(updated)
	require.Nil(t, cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID)))

	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{pluginServerOriginKey(plugin.PluginID): 1}, toolOrigins(tools))
	require.Equal(t, 2, harness.pluginSrv[plugin.PluginID].dialCount())
	require.NotSame(t, original, cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID)))
}

func TestRegisterPluginServerToolConfigsDoNotInvalidate(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	plugin := harness.addPlugin("com.example.plug", "plugin", 1, 0)
	manager := harness.newManager()

	_, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	original := cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID))
	require.NotNil(t, original)

	updated := plugin
	updated.ToolConfigs = []ToolConfig{{Name: "plugin_0", Policy: ToolPolicyAsk, Enabled: true}}
	updated.ExposeExternal = true
	manager.RegisterPluginServer(updated)
	require.Same(t, original, cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID)))

	_, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Same(t, original, cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID)))
	require.Equal(t, 1, harness.pluginSrv[plugin.PluginID].dialCount())
}

func TestGetToolsForUserSkipsUnregisteredPluginServers(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	remote := harness.addRemote("remotea", 1, 0)
	plugin := harness.addPlugin("com.example.plug", "plugin", 1, 0)
	manager := harness.newManagerWithoutPluginRegister()

	require.False(t, manager.IsPluginRegistered(plugin.PluginID))
	_, ok := manager.GetPluginServer(plugin.PluginID)
	require.True(t, ok, "config still hydrates the plugin row")

	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{remote.BaseURL: 1}, toolOrigins(tools))
	require.Zero(t, harness.pluginSrv[plugin.PluginID].requestCount(),
		"a config-only plugin row must not be contacted")
	require.Nil(t, cachedUserClient(manager, "alice", pluginServerOriginKey(plugin.PluginID)))
}

func TestClientManagerConcurrentReInit(t *testing.T) {
	harness := newRuntimeHarness(t, "alice", "bob")
	remote := harness.addRemote("remotea", 1, 0)
	harness.addPlugin("com.example.one", "pluginone", 1, 0)
	harness.withEmbedded("embedded_tool", 0)
	manager := harness.newManager()

	_, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	keep := cachedUserClient(manager, "alice", "remotea")
	require.NotNil(t, keep)

	const workers = 6
	const iterations = 20
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < iterations; n++ {
				cfg := cloneMCPConfig(manager.GetConfig())
				cfg.IdleTimeoutMinutes = 10 + (i+n)%7
				if len(cfg.Servers) > 0 {
					cfg.Servers[0].ToolConfigs = []ToolConfig{{
						Name:    "remotea_0",
						Policy:  ToolPolicyAsk,
						Enabled: true,
					}}
				}
				if len(cfg.PluginServers) > 0 {
					cfg.PluginServers[0].ExposeExternal = n%2 == 0
				}
				manager.ReInit(cfg, manager.GetEmbeddedServer())
			}
		}()
	}
	wg.Wait()

	require.Same(t, keep, cachedUserClient(manager, "alice", "remotea"),
		"concurrent tool-policy ReInit must keep the unaffected remote session")

	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Contains(t, toolOrigins(tools), remote.BaseURL)
	require.Equal(t, 1, harness.remoteSrv["remotea"].dialCount())
}

func TestClientManagerReInitRaceSafe(t *testing.T) {
	harness := newRuntimeHarness(t, "alice", "bob")
	harness.addRemote("remotea", 1, 0)
	harness.addRemote("remoteb", 1, 0)
	harness.addPlugin("com.example.one", "pluginone", 1, 0)
	harness.withEmbedded("embedded_tool", 0)
	manager := harness.newManager()

	_, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)

	var stop atomic.Bool
	var wg sync.WaitGroup
	const iterations = 40

	run := func(body func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations && !stop.Load(); i++ {
				body()
			}
		}()
	}

	run(func() {
		manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	})
	run(func() {
		manager.GetToolsForUser(context.Background(), "bob", ToolSelection{})
	})
	run(func() {
		cfg := cloneMCPConfig(manager.GetConfig())
		cfg.IdleTimeoutMinutes = 20 + (cfg.IdleTimeoutMinutes % 5)
		if len(cfg.Servers) > 0 {
			cfg.Servers[0].ToolConfigs = []ToolConfig{{Name: "remotea_0", Policy: ToolPolicyAsk, Enabled: true}}
		}
		manager.ReInit(cfg, manager.GetEmbeddedServer())
	})
	run(func() {
		cfg := cloneMCPConfig(manager.GetConfig())
		cfg.IdleTimeoutMinutes = 15 + (cfg.IdleTimeoutMinutes % 3)
		manager.ReInit(cfg, manager.GetEmbeddedServer())
	})
	run(func() {
		manager.ReInit(cloneMCPConfig(manager.GetConfig()), manager.GetEmbeddedServer())
	})
	run(func() {
		manager.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.temp", Name: "Temp", Path: "/mcp", Enabled: true})
		manager.UnregisterPluginServer("com.example.temp")
	})
	run(func() {
		manager.RefreshToolsForUser(context.Background(), "alice", ToolSelection{})
	})
	run(func() {
		manager.InvalidateUserClients("bob")
	})

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		stop.Store(true)
		t.Fatal("timed out running concurrent ReInit against runtime operations")
	}

	manager.Close()
	require.Nil(t, manager.getOrCreateUserClients("alice"))
}

func TestRemoteOriginIdentityIgnoresToolConfigs(t *testing.T) {
	left := ServerConfig{
		Name:    "Jira",
		Enabled: true,
		BaseURL: "https://jira.example.com/mcp",
		Headers: map[string]string{"X-A": "1"},
		ToolConfigs: []ToolConfig{
			{Name: "old", Enabled: true},
		},
	}
	right := left
	right.ToolConfigs = []ToolConfig{{Name: "new", Enabled: false}}
	require.Equal(t, remoteOriginIdentity(left, false), remoteOriginIdentity(right, false))

	right.Headers = map[string]string{"X-A": "2"}
	require.NotEqual(t, remoteOriginIdentity(left, false), remoteOriginIdentity(right, false))
}

func TestPluginOriginIdentityIgnoresToolConfigsAndExposure(t *testing.T) {
	left := PluginServerConfig{PluginID: "com.example.mcp", Name: "Example", Path: "/mcp", Enabled: true, ExposeExternal: false}
	right := left
	right.ToolConfigs = []ToolConfig{{Name: "echo", Enabled: true}}
	right.ExposeExternal = true
	require.Equal(t, pluginOriginIdentity(left, true), pluginOriginIdentity(right, true))

	right.Path = "/other"
	require.NotEqual(t, pluginOriginIdentity(left, true), pluginOriginIdentity(right, true))
}

func TestEncodeIdentityHeadersDoesNotCollideOnEqualsOrNewlines(t *testing.T) {
	left := map[string]string{"a=b\nc": ""}
	right := map[string]string{"a": "b\nc="}
	require.Equal(t, encodeIdentityHeadersLegacy(left), encodeIdentityHeadersLegacy(right),
		"precondition: the old k=v encoding must collide for this pair")
	require.NotEqual(t, remoteOriginIdentity(ServerConfig{Name: "s", BaseURL: "https://example.com", Headers: left}, false),
		remoteOriginIdentity(ServerConfig{Name: "s", BaseURL: "https://example.com", Headers: right}, false))
}

func TestPlanConnectionsDetachesMismatchedIdentity(t *testing.T) {
	server := newInstrumentedMCPServer("stale", 1, 0)
	t.Cleanup(server.Close)

	uc := NewUserClients("alice", newTestLogService(), nil, &http.Client{}, nil)
	cfg := ServerConfig{Name: "stale", BaseURL: server.URL, Enabled: true}
	require.Nil(t, uc.ensureConnections(context.Background(), []connectTask{uc.remoteConnectTask(context.Background(), cfg, false)}))
	original := uc.snapshotClients()[0].client
	require.NotNil(t, original)

	replacement := uc.remoteConnectTask(context.Background(), cfg, false)
	replacement.identity = `{"k":"remote","n":"stale","u":"` + server.URL + `","rotated":true}`
	replacement.dial = func() (*Client, error) {
		return nil, fmt.Errorf("replacement dial failed")
	}

	plans, discarded := uc.planConnections([]connectTask{replacement})
	require.Len(t, discarded, 1)
	require.Same(t, original, discarded[0])
	require.Len(t, plans, 1)
	require.True(t, plans[0].dialing)
	require.Empty(t, uc.snapshotClients(), "the old identity's client must be detached before the replacement dial")

	uc.closeDiscardedClients(discarded)
	mcpErrors := uc.executeConnections(context.Background(), plans)
	require.NotNil(t, mcpErrors)
	require.Empty(t, uc.snapshotClients(), "a failed replacement must not restore the old client")
}

func TestRemoteOriginIdentityTreatsCosmeticURLAsDistinct(t *testing.T) {
	left := remoteOriginIdentity(ServerConfig{Name: "s", Enabled: true, BaseURL: "https://example.com/mcp"}, false)
	right := remoteOriginIdentity(ServerConfig{Name: "s", Enabled: true, BaseURL: "https://example.com/mcp/"}, false)
	require.NotEqual(t, left, right)
}

func encodeIdentityHeadersLegacy(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(headers[key])
		b.WriteByte('\n')
	}
	return b.String()
}
