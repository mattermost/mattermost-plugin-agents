// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const reconcilePluginID = "com.example.source"

// sourcePluginRouter plays the Mattermost server between the Agents plugin and
// one source plugin: it reports the source plugin's state and dispatches
// PluginHTTP to its handler with the trusted inter-plugin header.
type sourcePluginRouter struct {
	mmapi.Client
	state   int
	handler http.Handler

	mu       sync.Mutex
	requests []*http.Request
}

func (r *sourcePluginRouter) GetPluginStatus(pluginID string) (*model.PluginStatus, error) {
	return &model.PluginStatus{PluginId: pluginID, State: r.state}, nil
}

func (r *sourcePluginRouter) PluginHTTP(req *http.Request) *http.Response {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()

	routed := req.Clone(req.Context())
	routed.URL.Path = strings.TrimPrefix(req.URL.Path, "/"+reconcilePluginID)
	routed.Header.Set("Mattermost-Plugin-ID", "mattermost-ai")
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, routed)
	return rec.Result()
}

func (r *sourcePluginRouter) requestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// agentsBridge records the registrations a pluginmcp source plugin posts back
// to the Agents plugin.
type agentsBridge struct {
	mu        sync.Mutex
	registers int
}

func (b *agentsBridge) PluginHTTP(req *http.Request) *http.Response {
	if strings.HasSuffix(req.URL.Path, "/bridge/v1/mcp/register") {
		b.mu.Lock()
		b.registers++
		b.mu.Unlock()
	}
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}
}

func (b *agentsBridge) registerCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.registers
}

// legacySourcePluginHandler is a source plugin MCP endpoint built before
// re-register requests existed: every request goes to the MCP handler.
func legacySourcePluginHandler() http.Handler {
	server := gosdkmcp.NewServer(&gosdkmcp.Implementation{Name: reconcilePluginID, Version: "1.0"}, nil)
	return gosdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *gosdkmcp.Server { return server },
		&gosdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true},
	)
}

// newKnownPluginServerManager returns a manager that restored one known plugin
// server from the KV store, as after a restart.
func newKnownPluginServerManager(t *testing.T, router *sourcePluginRouter, remoteAllowed func() bool) *ClientManager {
	t.Helper()
	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	known := PluginServerConfig{PluginID: reconcilePluginID, Name: "Source", Path: "/mcp", Enabled: true, ExposeExternal: true}
	setupPluginRegistrationKV(t, pluginTestAPI, map[string]PluginServerConfig{reconcilePluginID: known})
	pluginTestAPI.On("GetConfig").Return(&model.Config{PluginSettings: model.PluginSettings{
		PluginStates: map[string]*model.PluginState{reconcilePluginID: {Enable: true}},
	}}).Maybe()
	client := pluginapi.NewClient(pluginTestAPI, nil)

	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, nil, router, remoteAllowed, nil)
	t.Cleanup(m.Close)
	require.False(t, m.IsPluginRegistered(reconcilePluginID))
	return m
}

func TestReconcileKnownPluginServer(t *testing.T) {
	tests := []struct {
		name          string
		state         int
		source        func(t *testing.T, bridge *agentsBridge) http.Handler
		remoteAllowed func() bool
		wantPromoted  bool
		wantErr       bool
		wantContacted bool
		wantRegisters int
	}{
		{
			name:  "current library registers again",
			state: model.PluginStateRunning,
			source: func(t *testing.T, bridge *agentsBridge) http.Handler {
				s := pluginmcp.NewServer(bridge, pluginmcp.Config{PluginID: reconcilePluginID, Name: "Source", Path: "/mcp"})
				t.Cleanup(func() { _ = s.Unregister() })
				return s
			},
			wantContacted: true,
			wantRegisters: 1,
		},
		{
			name:  "unregistered current library is left alone",
			state: model.PluginStateRunning,
			source: func(t *testing.T, bridge *agentsBridge) http.Handler {
				s := pluginmcp.NewServer(bridge, pluginmcp.Config{PluginID: reconcilePluginID, Name: "Source", Path: "/mcp"})
				require.NoError(t, s.Unregister())
				return s
			},
			wantContacted: true,
		},
		{
			name:          "legacy library is promoted after answering a probe",
			state:         model.PluginStateRunning,
			source:        func(*testing.T, *agentsBridge) http.Handler { return legacySourcePluginHandler() },
			wantPromoted:  true,
			wantContacted: true,
		},
		{
			name:  "legacy library that fails the probe stays known",
			state: model.PluginStateRunning,
			source: func(*testing.T, *agentsBridge) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				})
			},
			wantErr:       true,
			wantContacted: true,
		},
		{
			name:   "source plugin that is not running is not contacted",
			state:  model.PluginStateStarting,
			source: func(*testing.T, *agentsBridge) http.Handler { return legacySourcePluginHandler() },
		},
		{
			name:          "nothing is contacted without the license",
			state:         model.PluginStateRunning,
			source:        func(*testing.T, *agentsBridge) http.Handler { return legacySourcePluginHandler() },
			remoteAllowed: func() bool { return false },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bridge := &agentsBridge{}
			router := &sourcePluginRouter{state: tt.state, handler: tt.source(t, bridge)}
			remoteAllowed := tt.remoteAllowed
			if remoteAllowed == nil {
				remoteAllowed = RemoteMCPAlwaysAllowed
			}
			m := newKnownPluginServerManager(t, router, remoteAllowed)

			promoted, err := m.ReconcileKnownPluginServer(context.Background(), reconcilePluginID)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantPromoted, promoted)
			require.Equal(t, tt.wantPromoted, m.IsPluginRegistered(reconcilePluginID))
			require.Equal(t, tt.wantContacted, router.requestCount() > 0)
			require.Eventually(t, func() bool { return bridge.registerCount() == tt.wantRegisters }, time.Second, 5*time.Millisecond)
		})
	}
}

func TestReconcileKnownPluginServerIgnoresLiveEntries(t *testing.T) {
	router := &sourcePluginRouter{state: model.PluginStateRunning, handler: legacySourcePluginHandler()}
	m := newKnownPluginServerManager(t, router, RemoteMCPAlwaysAllowed)
	m.RegisterPluginServer(PluginServerConfig{PluginID: reconcilePluginID, Name: "Source", Path: "/mcp", Enabled: true})

	promoted, err := m.ReconcileKnownPluginServer(context.Background(), reconcilePluginID)
	require.NoError(t, err)
	require.False(t, promoted)
	require.Zero(t, router.requestCount())
}
