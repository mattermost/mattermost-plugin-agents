// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// newRebuildTestHandlers returns handlers over an initially empty registry with
// a fast retry backoff, so tests control when discovery first runs.
func newRebuildTestHandlers(t *testing.T, mockAPI *stubPluginAPI) (*PluginMCPHandlers, *stubRegistry) {
	t.Helper()
	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	registry := &stubRegistry{}
	h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, registry, mockAPI, nil, nil, allowAllStateChangingTools)
	require.NoError(t, err)
	h.retryBaseDelay = 5 * time.Millisecond
	h.retryMaxDelay = 20 * time.Millisecond
	t.Cleanup(h.Close)
	return h, registry
}

func exposedPluginServer(pluginID string) mcppkg.PluginServerConfig {
	return mcppkg.PluginServerConfig{PluginID: pluginID, Name: pluginID, Path: "/mcp", Enabled: true, ExposeExternal: true}
}

// A source plugin registers from its own OnActivate, before the server routes
// requests to it. Discovery must not call it then, and must pick its tools up
// once it is running without another registration.
func TestRequestRebuildWaitsForSourcePluginToRun(t *testing.T) {
	const pluginID = "com.example.starting"
	target := newFakePluginMCPServerWithToolNames(t, "starting_tool")
	t.Cleanup(target.Close)

	var state atomic.Int32
	state.Store(int32(model.PluginStateStarting))
	var calls atomic.Int32
	mockAPI := &stubPluginAPI{
		pluginHTTP: func(req *http.Request) *http.Response {
			calls.Add(1)
			rec := httptest.NewRecorder()
			target.Config.Handler.ServeHTTP(rec, req)
			return rec.Result()
		},
		pluginState: func(string) int { return int(state.Load()) },
	}
	h, registry := newRebuildTestHandlers(t, mockAPI)

	registry.set([]mcppkg.PluginServerConfig{exposedPluginServer(pluginID)})
	h.RequestRebuild()

	time.Sleep(50 * time.Millisecond)
	require.Zero(t, calls.Load(), "a source plugin that is not running must not be called")
	require.NotContains(t, listToolNames(t, h), "starting_tool")

	state.Store(int32(model.PluginStateRunning))
	require.Eventually(t, func() bool {
		names, err := listToolNamesNoRequire(t, h)
		return err == nil && slices.Contains(names, "starting_tool")
	}, 5*time.Second, 10*time.Millisecond, "the retry must discover the plugin once it runs")

	h.Close()
	callsAfterClose := calls.Load()
	registry.set([]mcppkg.PluginServerConfig{exposedPluginServer(pluginID), exposedPluginServer("com.example.other")})
	h.RequestRebuild()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, callsAfterClose, calls.Load(), "no rebuild may run after Close")
}

// The register route requests a rebuild while the source plugin may be slow
// or still starting; the request must return without waiting for discovery.
func TestRequestRebuildDoesNotWaitForDiscovery(t *testing.T) {
	const pluginID = "com.example.slow"
	target := newFakePluginMCPServerWithToolNames(t, "slow_tool")
	t.Cleanup(target.Close)

	release := make(chan struct{})
	mockAPI := &stubPluginAPI{pluginHTTP: func(req *http.Request) *http.Response {
		<-release
		rec := httptest.NewRecorder()
		target.Config.Handler.ServeHTTP(rec, req)
		return rec.Result()
	}}
	h, registry := newRebuildTestHandlers(t, mockAPI)
	registry.set([]mcppkg.PluginServerConfig{exposedPluginServer(pluginID)})

	returned := make(chan struct{})
	go func() {
		h.RequestRebuild()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("RequestRebuild blocked on discovery")
	}

	close(release)
	require.Eventually(t, func() bool {
		names, err := listToolNamesNoRequire(t, h)
		return err == nil && slices.Contains(names, "slow_tool")
	}, 5*time.Second, 10*time.Millisecond)
}
