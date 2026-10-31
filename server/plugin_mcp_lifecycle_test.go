// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const lifecycleSourcePluginID = "com.example.source"

// lifecycleSourcePlugin is a running source plugin built with a pluginmcp
// version that predates re-register requests, so reconciling it promotes the
// known entry after a probe.
type lifecycleSourcePlugin struct {
	mmapi.Client
	handler http.Handler
}

func (s *lifecycleSourcePlugin) GetPluginStatus(pluginID string) (*model.PluginStatus, error) {
	return &model.PluginStatus{PluginId: pluginID, State: model.PluginStateRunning}, nil
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

func newLifecycleTestPlugin(t *testing.T) (*Plugin, *mcp.ClientManager, *countingRebuilder) {
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

	manager := mcp.NewClientManager(mcp.Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, source, mcp.RemoteMCPAlwaysAllowed, nil)
	t.Cleanup(manager.Close)
	rebuilder := &countingRebuilder{}
	p := &Plugin{mcpLifecycle: newPluginMCPLifecycle(manager, rebuilder, &client.Log)}
	t.Cleanup(p.mcpLifecycle.close)
	return p, manager, rebuilder
}

var lifecycleSourceServer = mcp.PluginServerConfig{
	PluginID: lifecycleSourcePluginID, Name: "Source", Path: "/mcp", Enabled: true, ExposeExternal: true,
}

func TestPluginLifecycleHooks(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(m *mcp.ClientManager)
		event         func(p *Plugin, manifest *model.Manifest)
		wantLive      bool
		wantRebuilds  int32
		wantKnownOnly bool
	}{
		{
			name:          "deactivated live server is taken out of service and stays known",
			setup:         func(m *mcp.ClientManager) { m.RegisterPluginServer(lifecycleSourceServer) },
			event:         (*Plugin).OnPluginDeactivated,
			wantRebuilds:  1,
			wantKnownOnly: true,
		},
		{
			name:  "deactivation of an unrelated plugin is ignored",
			setup: func(*mcp.ClientManager) {},
			event: (*Plugin).OnPluginDeactivated,
		},
		{
			name:         "activated live server is discovered again",
			setup:        func(m *mcp.ClientManager) { m.RegisterPluginServer(lifecycleSourceServer) },
			event:        (*Plugin).OnPluginActivated,
			wantLive:     true,
			wantRebuilds: 1,
		},
		{
			name: "activated known server is brought back into service",
			setup: func(m *mcp.ClientManager) {
				m.RegisterPluginServer(lifecycleSourceServer)
				m.UnregisterPluginServer(lifecycleSourceServer.PluginID)
			},
			event:        (*Plugin).OnPluginActivated,
			wantLive:     true,
			wantRebuilds: 1,
		},
		{
			name:  "activation of an unrelated plugin is ignored",
			setup: func(*mcp.ClientManager) {},
			event: (*Plugin).OnPluginActivated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, manager, rebuilder := newLifecycleTestPlugin(t)
			tt.setup(manager)

			tt.event(p, &model.Manifest{Id: lifecycleSourcePluginID})
			p.mcpLifecycle.wg.Wait()

			require.Equal(t, tt.wantLive, manager.IsPluginRegistered(lifecycleSourcePluginID))
			require.Equal(t, tt.wantRebuilds, rebuilder.calls.Load())
			if tt.wantKnownOnly {
				require.Equal(t, []string{lifecycleSourcePluginID}, knownPluginIDs(manager))
			}
		})
	}
}

// After a restart, saved registrations are known but not live; activation
// brings back every one whose source plugin is running.
func TestPluginLifecycleReconcileKnownOnActivation(t *testing.T) {
	p, manager, rebuilder := newLifecycleTestPlugin(t)
	manager.RegisterPluginServer(lifecycleSourceServer)
	manager.UnregisterPluginServer(lifecycleSourceServer.PluginID)
	require.False(t, manager.IsPluginRegistered(lifecycleSourcePluginID))

	p.mcpLifecycle.reconcileKnown()
	p.mcpLifecycle.wg.Wait()

	require.True(t, manager.IsPluginRegistered(lifecycleSourcePluginID))
	require.Empty(t, knownPluginIDs(manager))
	require.Equal(t, int32(1), rebuilder.calls.Load())
}

// The server only delivers a hook whose method matches plugin.Hooks exactly.
// The hooks are absent from server/public versions older than 12.2, where
// there is nothing to compare against.
func TestPluginLifecycleHookSignaturesMatchServer(t *testing.T) {
	hooks := reflect.TypeFor[plugin.Hooks]()
	impl := reflect.TypeFor[*Plugin]()
	for _, name := range []string{"OnPluginActivated", "OnPluginDeactivated"} {
		implMethod, ok := impl.MethodByName(name)
		require.True(t, ok, name)
		hookMethod, ok := hooks.MethodByName(name)
		if !ok {
			continue
		}
		require.Equal(t, hookMethod.Type.NumIn(), implMethod.Type.NumIn()-1, name)
		for i := range hookMethod.Type.NumIn() {
			require.Equal(t, hookMethod.Type.In(i), implMethod.Type.In(i+1), name)
		}
		require.Equal(t, hookMethod.Type.NumOut(), implMethod.Type.NumOut(), name)
	}
}

func knownPluginIDs(m *mcp.ClientManager) []string {
	var ids []string
	for _, known := range m.KnownPluginServers() {
		ids = append(ids, known.PluginID)
	}
	return ids
}
