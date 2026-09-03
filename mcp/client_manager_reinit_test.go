// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The ReInit fixture connects one of every origin kind twice over: a "keep"
// pair no test case touches and a "change" pair each case mutates.
const (
	keepPluginID   = "com.example.keep"
	changePluginID = "com.example.change"
)

func reinitConfig(manager *ClientManager, apply func(cfg *Config)) {
	cfg := cloneMCPConfig(manager.GetConfig())
	apply(&cfg)
	manager.ReInit(cfg, manager.GetEmbeddedServer())
}

func reinitRemote(manager *ClientManager, name string, apply func(server *ServerConfig)) {
	reinitConfig(manager, func(cfg *Config) {
		for i := range cfg.Servers {
			if cfg.Servers[i].Name == name {
				apply(&cfg.Servers[i])
			}
		}
	})
}

func reinitPlugin(manager *ClientManager, pluginID string, apply func(cfg *PluginServerConfig)) {
	reinitConfig(manager, func(cfg *Config) {
		for i := range cfg.PluginServers {
			if cfg.PluginServers[i].PluginID == pluginID {
				apply(&cfg.PluginServers[i])
			}
		}
	})
}

// A configuration update must only disturb the origins it actually changed:
// every other session keeps its exact *Client, and nothing is re-dialed.
func TestClientManagerReInitPreservesUnchangedOrigins(t *testing.T) {
	connected := []string{"keep", "change", pluginServerOriginKey(keepPluginID), pluginServerOriginKey(changePluginID), EmbeddedClientKey}

	testCases := []struct {
		name   string
		mutate func(t *testing.T, harness *runtimeHarness, manager *ClientManager)
		// invalidated lists the cached serverIDs the change must drop.
		invalidated []string
		// reconnected lists the serverIDs holding a fresh session after the
		// next request. Every other origin must not be dialed again.
		reconnected []string
	}{
		{
			name: "unchanged config",
			mutate: func(_ *testing.T, _ *runtimeHarness, manager *ClientManager) {
				reinitConfig(manager, func(*Config) {})
			},
		},
		{
			name: "tool policy and external exposure only",
			mutate: func(_ *testing.T, _ *runtimeHarness, manager *ClientManager) {
				reinitConfig(manager, func(cfg *Config) {
					cfg.Servers[0].ToolConfigs = []ToolConfig{{Name: "keep_0", Policy: ToolPolicyAsk, Enabled: false}}
					cfg.PluginServers[0].ToolConfigs = []ToolConfig{{Name: "plugin-keep_0", Policy: ToolPolicyAsk, Enabled: true}}
					cfg.PluginServers[0].ExposeExternal = true
					cfg.EmbeddedServer.ToolConfigs = []ToolConfig{{Name: "embedded_tool", Policy: ToolPolicyAsk, Enabled: true}}
				})
			},
		},
		{
			name: "idle timeout only",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				reinitConfig(manager, func(cfg *Config) { cfg.IdleTimeoutMinutes = 12 })
				require.Equal(t, 12*time.Minute, manager.clientTimeout)
			},
		},
		{
			name: "unrelated origin added",
			mutate: func(_ *testing.T, harness *runtimeHarness, manager *ClientManager) {
				other := harness.addRemote("other", 1, 0)
				reinitConfig(manager, func(cfg *Config) { cfg.Servers = append(cfg.Servers, other) })
			},
			reconnected: []string{"other"},
		},
		{
			name: "remote URL change",
			mutate: func(_ *testing.T, harness *runtimeHarness, manager *ClientManager) {
				replacement := harness.addRemote("replacement", 1, 0)
				reinitRemote(manager, "change", func(server *ServerConfig) { server.BaseURL = replacement.BaseURL })
			},
			invalidated: []string{"change"},
			reconnected: []string{"change"},
		},
		{
			name: "remote name change",
			mutate: func(_ *testing.T, _ *runtimeHarness, manager *ClientManager) {
				reinitRemote(manager, "change", func(server *ServerConfig) { server.Name = "renamed" })
			},
			invalidated: []string{"change"},
			reconnected: []string{"renamed"},
		},
		{
			name: "remote credentials change",
			mutate: func(_ *testing.T, _ *runtimeHarness, manager *ClientManager) {
				reinitRemote(manager, "change", func(server *ServerConfig) {
					server.Headers = map[string]string{"X-Test": "1"}
					server.ClientSecret = "new-secret"
				})
			},
			invalidated: []string{"change"},
			reconnected: []string{"change"},
		},
		{
			name: "remote disabled",
			mutate: func(_ *testing.T, _ *runtimeHarness, manager *ClientManager) {
				reinitRemote(manager, "change", func(server *ServerConfig) { server.Enabled = false })
			},
			invalidated: []string{"change"},
		},
		{
			name: "remote becomes a duplicate conflict",
			mutate: func(_ *testing.T, _ *runtimeHarness, manager *ClientManager) {
				reinitConfig(manager, func(cfg *Config) {
					for _, server := range cfg.Servers {
						if server.Name == "change" {
							cfg.Servers = append(cfg.Servers, ServerConfig{Name: "duplicate", BaseURL: server.BaseURL, Enabled: true})
							return
						}
					}
				})
			},
			invalidated: []string{"change"},
		},
		{
			name: "plugin path change",
			mutate: func(t *testing.T, _ *runtimeHarness, manager *ClientManager) {
				current, ok := manager.GetPluginServer(changePluginID)
				require.True(t, ok)
				current.Path = "/other-mcp"
				manager.RegisterPluginServer(current)
				reinitConfig(manager, func(*Config) {})
			},
			invalidated: []string{pluginServerOriginKey(changePluginID)},
			reconnected: []string{pluginServerOriginKey(changePluginID)},
		},
		{
			name: "plugin disabled",
			mutate: func(_ *testing.T, _ *runtimeHarness, manager *ClientManager) {
				reinitPlugin(manager, changePluginID, func(cfg *PluginServerConfig) { cfg.Enabled = false })
			},
			invalidated: []string{pluginServerOriginKey(changePluginID)},
		},
		{
			name: "plugin unregistered",
			mutate: func(_ *testing.T, _ *runtimeHarness, manager *ClientManager) {
				manager.UnregisterPluginServer(changePluginID)
				reinitConfig(manager, func(*Config) {})
			},
			invalidated: []string{pluginServerOriginKey(changePluginID)},
		},
		{
			name: "embedded disabled",
			mutate: func(_ *testing.T, _ *runtimeHarness, manager *ClientManager) {
				reinitConfig(manager, func(cfg *Config) { cfg.EmbeddedServer.Enabled = false })
			},
			invalidated: []string{EmbeddedClientKey},
		},
		{
			name: "embedded server replaced",
			mutate: func(_ *testing.T, harness *runtimeHarness, manager *ClientManager) {
				manager.ReInit(cloneMCPConfig(manager.GetConfig()), harness.newEmbeddedServer("embedded_tool", 0))
			},
			invalidated: []string{EmbeddedClientKey},
			reconnected: []string{EmbeddedClientKey},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			harness := newRuntimeHarness(t, "alice")
			harness.addRemote("keep", 1, 0)
			harness.addRemote("change", 1, 0)
			harness.addPlugin(keepPluginID, "plugin-keep", 1, 0)
			harness.addPlugin(changePluginID, "plugin-change", 1, 0)
			harness.withEmbedded("embedded_tool", 0)
			manager := harness.newManager()

			tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
			require.Nil(t, mcpErrors)
			require.Len(t, tools, len(connected))

			before := map[string]*Client{}
			for _, serverID := range connected {
				before[serverID] = cachedUserClient(manager, "alice", serverID)
				require.NotNil(t, before[serverID], "precondition: %s must be connected", serverID)
			}
			dialsBefore := harness.totalDials()

			tc.mutate(t, harness, manager)

			for _, serverID := range connected {
				if slices.Contains(tc.invalidated, serverID) {
					require.Nil(t, cachedUserClient(manager, "alice", serverID),
						"affected origin %s must be closed and removed", serverID)
					continue
				}
				require.Same(t, before[serverID], cachedUserClient(manager, "alice", serverID),
					"unaffected origin %s must keep the same session pointer", serverID)
			}

			_, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
			require.Nil(t, mcpErrors)

			for _, serverID := range connected {
				switch {
				case slices.Contains(tc.reconnected, serverID):
					require.NotSame(t, before[serverID], cachedUserClient(manager, "alice", serverID))
				case slices.Contains(tc.invalidated, serverID):
					require.Nil(t, cachedUserClient(manager, "alice", serverID),
						"an origin the change ruled out must stay disconnected")
				default:
					require.Same(t, before[serverID], cachedUserClient(manager, "alice", serverID),
						"unaffected origin %s must not be re-dialed", serverID)
				}
			}
			for _, serverID := range tc.reconnected {
				require.NotNil(t, cachedUserClient(manager, "alice", serverID),
					"invalidated origin %s must reconnect", serverID)
			}
			require.Equal(t, dialsBefore+len(tc.reconnected), harness.totalDials(),
				"exactly the affected origins may be dialed again")

			// Identity is stable once applied: a further request reuses everything.
			_, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
			require.Nil(t, mcpErrors)
			require.Equal(t, dialsBefore+len(tc.reconnected), harness.totalDials())
		})
	}
}

// An origin invalidated while its dial is in flight must not commit the stale
// session, and must reconnect cleanly on the next request.
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

			reinitRemote(manager, "first", func(server *ServerConfig) { server.ClientSecret = "rotated" })

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
		})
	}
}

// Registration changes take effect immediately, without waiting for a config
// reload, and only when they change how the plugin server is reached.
func TestRegisterPluginServerAppliesIdentityChangesImmediately(t *testing.T) {
	testCases := []struct {
		name        string
		mutate      func(cfg *PluginServerConfig)
		invalidated bool
	}{
		{
			name:        "path change reconnects",
			mutate:      func(cfg *PluginServerConfig) { cfg.Path = "/other-mcp" },
			invalidated: true,
		},
		{
			name: "tool configs and exposure do not reconnect",
			mutate: func(cfg *PluginServerConfig) {
				cfg.ToolConfigs = []ToolConfig{{Name: "plugin_0", Policy: ToolPolicyAsk, Enabled: true}}
				cfg.ExposeExternal = true
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			harness := newRuntimeHarness(t, "alice")
			plugin := harness.addPlugin("com.example.plug", "plugin", 1, 0)
			manager := harness.newManager()
			origin := pluginServerOriginKey(plugin.PluginID)

			_, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
			require.Nil(t, mcpErrors)
			original := cachedUserClient(manager, "alice", origin)
			require.NotNil(t, original)

			updated := plugin
			tc.mutate(&updated)
			manager.RegisterPluginServer(updated)

			if !tc.invalidated {
				require.Same(t, original, cachedUserClient(manager, "alice", origin))
				_, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
				require.Nil(t, mcpErrors)
				require.Same(t, original, cachedUserClient(manager, "alice", origin))
				require.Equal(t, 1, harness.pluginSrv[plugin.PluginID].dialCount())
				return
			}

			require.Nil(t, cachedUserClient(manager, "alice", origin))
			tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
			require.Nil(t, mcpErrors)
			require.Equal(t, map[string]int{origin: 1}, toolOrigins(tools))
			require.NotSame(t, original, cachedUserClient(manager, "alice", origin))
			require.Equal(t, 2, harness.pluginSrv[plugin.PluginID].dialCount())
		})
	}
}

// Unregistering must drop the session at once and stop all contact until the
// source plugin registers again, which then opens exactly one fresh session.
func TestUnregisterPluginServerStopsReconnectUntilReregister(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	remote := harness.addRemote("remotea", 1, 0)
	plugin := harness.addPlugin("com.example.plug", "plugin", 1, 0)
	manager := harness.newManager()
	origin := pluginServerOriginKey(plugin.PluginID)

	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{remote.BaseURL: 1, origin: 1}, toolOrigins(tools))
	original := cachedUserClient(manager, "alice", origin)
	require.NotNil(t, original)
	requestsBeforeUnregister := harness.pluginSrv[plugin.PluginID].requestCount()

	manager.UnregisterPluginServer(plugin.PluginID)
	require.Nil(t, cachedUserClient(manager, "alice", origin))
	require.False(t, manager.IsPluginRegistered(plugin.PluginID))

	tools, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{remote.BaseURL: 1}, toolOrigins(tools))
	require.Equal(t, requestsBeforeUnregister, harness.pluginSrv[plugin.PluginID].requestCount(),
		"an unregistered plugin server must not be contacted")

	manager.RegisterPluginServer(plugin)
	require.Nil(t, cachedUserClient(manager, "alice", origin),
		"re-register must not resurrect the previous session")

	tools, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{remote.BaseURL: 1, origin: 1}, toolOrigins(tools))
	fresh := cachedUserClient(manager, "alice", origin)
	require.NotSame(t, original, fresh)
	require.Equal(t, 2, harness.pluginSrv[plugin.PluginID].dialCount(),
		"re-register must open exactly one fresh connection")

	_, mcpErrors = manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Same(t, fresh, cachedUserClient(manager, "alice", origin))
	require.Equal(t, 2, harness.pluginSrv[plugin.PluginID].dialCount())
}

// A plugin row that only exists in admin config, with no source plugin behind
// it, must never be dialed.
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

// Identity-neutral ReInits racing each other must not cost the user a session.
func TestClientManagerConcurrentReInitKeepsUnaffectedSessions(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	remote := harness.addRemote("remotea", 1, 0)
	harness.addPlugin("com.example.one", "pluginone", 1, 0)
	harness.withEmbedded("embedded_tool", 0)
	manager := harness.newManager()

	_, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	keep := cachedUserClient(manager, "alice", "remotea")
	require.NotNil(t, keep)

	var wg sync.WaitGroup
	for worker := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range 20 {
				reinitConfig(manager, func(cfg *Config) {
					cfg.IdleTimeoutMinutes = 10 + (worker+n)%7
					cfg.Servers[0].ToolConfigs = []ToolConfig{{Name: "remotea_0", Policy: ToolPolicyAsk, Enabled: true}}
					cfg.PluginServers[0].ExposeExternal = n%2 == 0
				})
			}
		}()
	}
	wg.Wait()

	require.Same(t, keep, cachedUserClient(manager, "alice", "remotea"))

	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Contains(t, toolOrigins(tools), remote.BaseURL)
	require.Equal(t, 1, harness.remoteSrv["remotea"].dialCount())
}

// Every lifecycle entry point must be safe against every other one, ending in
// a permanent Close.
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
	run := func(body func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40 && !stop.Load(); i++ {
				body()
			}
		}()
	}

	run(func() { manager.GetToolsForUser(context.Background(), "alice", ToolSelection{}) })
	run(func() { manager.GetToolsForUser(context.Background(), "bob", ToolSelection{}) })
	run(func() {
		reinitRemote(manager, "remotea", func(server *ServerConfig) {
			server.ToolConfigs = []ToolConfig{{Name: "remotea_0", Policy: ToolPolicyAsk, Enabled: true}}
		})
	})
	run(func() {
		reinitConfig(manager, func(cfg *Config) { cfg.IdleTimeoutMinutes = 15 + (cfg.IdleTimeoutMinutes % 3) })
	})
	run(func() { reinitConfig(manager, func(*Config) {}) })
	run(func() {
		manager.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.temp", Name: "Temp", Path: "/mcp", Enabled: true})
		manager.UnregisterPluginServer("com.example.temp")
	})
	run(func() { _, _, _ = manager.RefreshToolsForUser(context.Background(), "alice") })
	run(func() { manager.InvalidateUserClients("bob") })

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

var identityBaseRemote = ServerConfig{
	Name:         "Jira",
	Enabled:      true,
	BaseURL:      "https://jira.example.com/mcp",
	Headers:      map[string]string{"X-A": "1"},
	ClientID:     "client",
	ClientSecret: "topsecret",
	ToolConfigs:  []ToolConfig{{Name: "old", Enabled: true}},
}

func remoteIdentity(conflicting bool, apply func(server *ServerConfig)) originIdentity {
	server := identityBaseRemote
	server.Headers = maps.Clone(identityBaseRemote.Headers)
	if apply != nil {
		apply(&server)
	}
	return remoteOriginIdentity(server, conflicting)
}

// Connection identity decides which cached sessions survive a config change,
// so it must cover every input that changes how a remote server is reached,
// and nothing else.
func TestRemoteOriginIdentityCoversConnectionInputsOnly(t *testing.T) {
	testCases := []struct {
		name string
		// left and right mutate a copy of the same base server; nil leaves it
		// untouched. conflicting applies to the right side only.
		left, right func(server *ServerConfig)
		conflicting bool
		equal       bool
	}{
		{
			name:  "tool policy is not a connection input",
			right: func(server *ServerConfig) { server.ToolConfigs = []ToolConfig{{Name: "new", Enabled: false}} },
			equal: true,
		},
		{name: "header value", right: func(server *ServerConfig) { server.Headers["X-A"] = "2" }},
		{
			// These two header maps collide under the "key=value\n" encoding
			// that a flat string identity invites.
			name:  "header maps that collide under a flat key=value encoding",
			left:  func(server *ServerConfig) { server.Headers = map[string]string{"a=b\nc": ""} },
			right: func(server *ServerConfig) { server.Headers = map[string]string{"a": "b\nc="} },
		},
		{name: "client secret", right: func(server *ServerConfig) { server.ClientSecret = "rotated" }},
		{name: "cosmetic URL spelling", right: func(server *ServerConfig) { server.BaseURL += "/" }},
		{name: "admin disabled it", right: func(server *ServerConfig) { server.Enabled = false }},
		{name: "it became a duplicate conflict", conflicting: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.equal, remoteIdentity(false, tc.left) == remoteIdentity(tc.conflicting, tc.right))
		})
	}

	require.NotContains(t, fmt.Sprintf("%+v", remoteIdentity(false, nil)), "topsecret",
		"an identity must not carry credential material that could be logged")
}

func TestPluginAndEmbeddedOriginIdentity(t *testing.T) {
	plugin := PluginServerConfig{PluginID: "com.example.mcp", Name: "Example", Path: "/mcp", Enabled: true}
	embedded := &delayedEmbeddedServer{server: newTestMCPServer(0, "embedded_tool")}

	policyOnly := plugin
	policyOnly.ExposeExternal = true
	policyOnly.ToolConfigs = []ToolConfig{{Name: "echo", Enabled: true}}
	require.Equal(t, pluginOriginIdentity(plugin), pluginOriginIdentity(policyOnly),
		"tool policy and external exposure are not connection inputs")

	rerouted := plugin
	rerouted.Path = "/other"
	require.NotEqual(t, pluginOriginIdentity(plugin), pluginOriginIdentity(rerouted))
	require.NotEqual(t, pluginOriginIdentity(plugin), originIdentity{},
		"an unregistered plugin row has no live identity to match")

	require.NotEqual(t, embeddedOriginIdentity(embedded, true),
		embeddedOriginIdentity(&delayedEmbeddedServer{server: newTestMCPServer(0, "embedded_tool")}, true),
		"a replaced embedded server is a new identity")
	require.NotEqual(t, embeddedOriginIdentity(embedded, true), embeddedOriginIdentity(embedded, false))
}

// A cached session whose identity no longer matches must be detached before
// the replacement dials, so a failed replacement cannot leave the old one in
// place.
func TestPlanConnectionsDetachesMismatchedIdentity(t *testing.T) {
	server := newInstrumentedMCPServer("stale", 1, 0)
	t.Cleanup(server.Close)

	uc := NewUserClients("alice", newTestLogService(), nil, &http.Client{}, nil)
	cfg := ServerConfig{Name: "stale", BaseURL: server.URL, Enabled: true}
	ctx := context.Background()
	require.Nil(t, uc.ensureConnections(ctx, []connectTask{uc.remoteConnectTask(ctx, cfg, RemoteConnectTimeout, false)}))
	original := uc.snapshotClients()[0].client
	require.NotNil(t, original)

	replacement := uc.remoteConnectTask(ctx, cfg, RemoteConnectTimeout, false)
	replacement.identity.credentials = "rotated"
	replacement.dial = func() (*Client, error) {
		return nil, fmt.Errorf("replacement dial failed")
	}

	plans, discarded := uc.planConnections([]connectTask{replacement})
	require.Len(t, discarded, 1)
	require.Same(t, original, discarded[0])
	require.Len(t, plans, 1)
	require.True(t, plans[0].dialing)
	require.Empty(t, uc.snapshotClients(), "the old identity's client must be detached before the replacement dial")

	closeDetachedClients(uc.log, discarded)
	require.NotNil(t, uc.executeConnections(ctx, plans))
	require.Empty(t, uc.snapshotClients(), "a failed replacement must not restore the old client")
}
