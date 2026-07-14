// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// stubServerAccessChecker denies the listed server IDs and records calls.
type stubServerAccessChecker struct {
	denied map[string]bool
	calls  []string // serverIDs, in call order
}

func (s *stubServerAccessChecker) CanUseMCPServer(_ context.Context, _, serverID string) error {
	s.calls = append(s.calls, serverID)
	if s.denied[serverID] {
		return fmt.Errorf("server %s: denied", serverID)
	}
	return nil
}

func TestFilterToolsByUserAccess(t *testing.T) {
	const (
		allowedOrigin = "https://mcp-allowed.example.com"
		deniedOrigin  = "https://mcp-denied.example.com"
		noIDOrigin    = "https://mcp-legacy-no-id.example.com"
	)
	allowedID := "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	deniedID := "dddddddddddddddddddddddddd"

	cfg := Config{
		Servers: []ServerConfig{
			{ID: allowedID, Name: "Allowed", Enabled: true, BaseURL: allowedOrigin},
			{ID: deniedID, Name: "Denied", Enabled: true, BaseURL: deniedOrigin},
			{Name: "Legacy", Enabled: true, BaseURL: noIDOrigin}, // no stable ID: not policy-addressable
		},
	}

	tools := []llm.Tool{
		{Name: "allowed_tool", ServerOrigin: allowedOrigin},
		{Name: "denied_tool_a", ServerOrigin: deniedOrigin},
		{Name: "denied_tool_b", ServerOrigin: deniedOrigin},
		{Name: "legacy_tool", ServerOrigin: noIDOrigin},
		{Name: "embedded_tool", ServerOrigin: EmbeddedClientKey},
		{Name: "plugin_tool", ServerOrigin: "plugin://com.example.mcp"},
	}

	tests := []struct {
		name       string
		checker    *stubServerAccessChecker
		nilChecker bool
		wantNames  []string
		wantCalls  []string
	}{
		{
			name:    "denied server tools dropped silently, others untouched",
			checker: &stubServerAccessChecker{denied: map[string]bool{deniedID: true}},
			wantNames: []string{
				"allowed_tool", "legacy_tool", "embedded_tool", "plugin_tool",
			},
			// One decision call per distinct gated origin, not per tool.
			wantCalls: []string{allowedID, deniedID},
		},
		{
			name:      "all allowed keeps everything",
			checker:   &stubServerAccessChecker{},
			wantNames: []string{"allowed_tool", "denied_tool_a", "denied_tool_b", "legacy_tool", "embedded_tool", "plugin_tool"},
			wantCalls: []string{allowedID, deniedID},
		},
		{
			name:       "nil checker disables filtering",
			nilChecker: true,
			wantNames:  []string{"allowed_tool", "denied_tool_a", "denied_tool_b", "legacy_tool", "embedded_tool", "plugin_tool"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
			client := pluginapi.NewClient(mockAPI, nil)

			m := &ClientManager{
				log:    client.Log,
				config: cfg,
			}
			if !tt.nilChecker {
				m.accessChecker = tt.checker
			}

			filtered := m.filterToolsByUserAccess(context.Background(), "userid", tools)

			var names []string
			for _, tool := range filtered {
				names = append(names, tool.Name)
			}
			assert.Equal(t, tt.wantNames, names)

			if tt.checker != nil {
				assert.ElementsMatch(t, tt.wantCalls, tt.checker.calls)
			}
		})
	}
}
