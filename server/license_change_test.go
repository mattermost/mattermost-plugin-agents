// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/api"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestOnLicenseChangedRunsEveryListener(t *testing.T) {
	calls := 0
	mockAPI := &plugintest.API{}
	mockAPI.On("PublishWebSocketEvent", api.WebsocketEventBotsInvalidate, mock.Anything, mock.Anything).Once()
	defer mockAPI.AssertExpectations(t)
	p := &Plugin{pluginAPI: pluginapi.NewClient(mockAPI, nil), licenseChangeListeners: []func(){
		func() { calls++ },
		func() { calls++ },
	}}

	p.OnLicenseChanged(nil, &model.License{SkuShortName: model.LicenseShortSkuEnterprise})

	require.Equal(t, 2, calls)
}

type fakePluginServerRegistry []mcp.PluginServerConfig

func (f fakePluginServerRegistry) ListPluginServers() []mcp.PluginServerConfig { return f }

func TestLicensedPluginServers(t *testing.T) {
	registry := fakePluginServerRegistry{{PluginID: "com.example.demo", Enabled: true, ExposeExternal: true}}
	tests := []struct {
		name    string
		allowed func() bool
		want    int
	}{
		{name: "remote MCP available", allowed: func() bool { return true }, want: 1},
		{name: "remote MCP unavailable", allowed: func() bool { return false }, want: 0},
		{name: "nil predicate fails closed", allowed: nil, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			servers := licensedPluginServers{registry: registry, allowed: tc.allowed}.ListPluginServers()
			require.Len(t, servers, tc.want)
		})
	}
}
