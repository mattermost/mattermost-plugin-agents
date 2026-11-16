// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const lifecycleSourcePluginID = "com.example.source"

// lifecycleSourcePlugin is a source plugin built with a pluginmcp version that
// predates re-register requests, so reconciling it promotes the known entry
// after a probe. Its reported state is adjustable.
type lifecycleSourcePlugin struct {
	mmapi.Client
	handler http.Handler
	state   atomic.Int32
}

func (s *lifecycleSourcePlugin) GetPluginStatus(pluginID string) (*model.PluginStatus, error) {
	return &model.PluginStatus{PluginId: pluginID, State: int(s.state.Load())}, nil
}

func (s *lifecycleSourcePlugin) PluginHTTP(req *http.Request) *http.Response {
	routed := req.Clone(req.Context())
	routed.URL.Path = strings.TrimPrefix(req.URL.Path, "/"+lifecycleSourcePluginID)
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, routed)
	return rec.Result()
}

type countingRebuilder struct{ calls atomic.Int32 }

func (c *countingRebuilder) RequestRebuild() { c.calls.Add(1) }

func newLifecycleTestPlugin(t *testing.T) (*Plugin, *mcp.ClientManager, *countingRebuilder, *lifecycleSourcePlugin) {
	t.Helper()
	api := &plugintest.API{}
	for _, method := range []string{"LogDebug", "LogError", "LogWarn", "LogInfo"} {
		for arity := 1; arity <= 16; arity++ {
			args := make([]any, arity)
			for i := range args {
				args[i] = mock.Anything
			}
			api.On(method, args...).Return().Maybe()
		}
	}
	api.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
	api.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
	client := pluginapi.NewClient(api, nil)

	mcpServer := gosdkmcp.NewServer(&gosdkmcp.Implementation{Name: lifecycleSourcePluginID, Version: "1.0"}, nil)
	source := &lifecycleSourcePlugin{handler: gosdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *gosdkmcp.Server { return mcpServer },
		&gosdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true},
	)}
	source.state.Store(int32(model.PluginStateRunning))

	manager := mcp.NewClientManager(mcp.Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, source, mcp.RemoteMCPAlwaysAllowed, nil)
	t.Cleanup(manager.Close)
	rebuilder := &countingRebuilder{}
	p := &Plugin{mcpLifecycle: newPluginMCPLifecycle(manager, rebuilder, &client.Log)}
	t.Cleanup(p.mcpLifecycle.close)
	return p, manager, rebuilder, source
}

var lifecycleSourceServer = mcp.PluginServerConfig{
	PluginID: lifecycleSourcePluginID, Name: "Source", Path: "/mcp", Enabled: true, ExposeExternal: true,
}

func TestPluginLifecycleWatchStoppedPlugins(t *testing.T) {
	tests := []struct {
		name         string
		state        int
		wantLive     bool
		wantRebuilds bool
	}{
		{name: "running plugin stays in service", state: model.PluginStateRunning, wantLive: true},
		{name: "starting plugin stays in service", state: model.PluginStateStarting, wantLive: true},
		{name: "stopped plugin is taken out of service", state: model.PluginStateNotRunning, wantRebuilds: true},
		{name: "plugin that failed to stay running is taken out of service", state: model.PluginStateFailedToStayRunning, wantRebuilds: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, manager, rebuilder, source := newLifecycleTestPlugin(t)
			manager.RegisterPluginServer(lifecycleSourceServer)
			source.state.Store(int32(tt.state))

			p.mcpLifecycle.watchStoppedPlugins(time.Millisecond)

			if tt.wantLive {
				time.Sleep(50 * time.Millisecond)
				require.True(t, manager.IsPluginRegistered(lifecycleSourcePluginID))
				require.Zero(t, rebuilder.calls.Load())
				return
			}
			require.Eventually(t, func() bool { return !manager.IsPluginRegistered(lifecycleSourcePluginID) }, 5*time.Second, 5*time.Millisecond)
			require.Equal(t, []string{lifecycleSourcePluginID}, knownPluginIDs(manager), "the entry stays known so the plugin can register again")
			require.Eventually(t, func() bool { return rebuilder.calls.Load() > 0 }, 5*time.Second, 5*time.Millisecond)
		})
	}
}

// After a restart, saved registrations are known but not live; activation
// brings back every one whose source plugin is running.
func TestPluginLifecycleReconcileKnownOnActivation(t *testing.T) {
	p, manager, rebuilder, _ := newLifecycleTestPlugin(t)
	manager.RegisterPluginServer(lifecycleSourceServer)
	manager.UnregisterPluginServer(lifecycleSourceServer.PluginID)
	require.False(t, manager.IsPluginRegistered(lifecycleSourcePluginID))

	p.mcpLifecycle.reconcileKnown()
	p.mcpLifecycle.wg.Wait()

	require.True(t, manager.IsPluginRegistered(lifecycleSourcePluginID))
	require.Empty(t, knownPluginIDs(manager))
	require.Equal(t, int32(1), rebuilder.calls.Load())
}

func knownPluginIDs(m *mcp.ClientManager) []string {
	var ids []string
	for _, known := range m.KnownPluginServers() {
		ids = append(ids, known.PluginID)
	}
	return ids
}
