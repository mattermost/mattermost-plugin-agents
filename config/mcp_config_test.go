// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPToolConfigRetrievalDescriptionOverrideJSON(t *testing.T) {
	toolConfig := MCPToolConfig{
		Name:                         "get_issue",
		Policy:                       MCPToolPolicyAutoRunInDM,
		Enabled:                      true,
		RetrievalDescriptionOverride: "Find Jira issues by key or text",
	}

	data, err := json.Marshal(toolConfig)
	require.NoError(t, err)
	require.Contains(t, string(data), `"retrieval_description_override":"Find Jira issues by key or text"`)

	var decoded MCPToolConfig
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, toolConfig, decoded)
}

func TestMCPToolConfigEmptyRetrievalDescriptionOverrideOmitted(t *testing.T) {
	data, err := json.Marshal(MCPToolConfig{
		Name:    "get_issue",
		Policy:  MCPToolPolicyAsk,
		Enabled: true,
	})
	require.NoError(t, err)
	require.NotContains(t, string(data), "retrieval_description_override")

	var decoded MCPToolConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"get_issue","policy":"ask","enabled":true}`), &decoded))
	require.Empty(t, decoded.RetrievalDescriptionOverride)
}

func TestMCPConfigServerIDMaps(t *testing.T) {
	tests := []struct {
		name             string
		servers          []MCPServerConfig
		expectByOrigin   map[string]string
		expectByServerID map[string]string
	}{
		{
			name:             "empty servers",
			servers:          nil,
			expectByOrigin:   map[string]string{},
			expectByServerID: map[string]string{},
		},
		{
			name: "servers without IDs omitted",
			servers: []MCPServerConfig{
				{ID: "id-one", Name: "one", BaseURL: "https://one.example.com"},
				{Name: "no-id", BaseURL: "https://two.example.com"},
			},
			expectByOrigin:   map[string]string{"https://one.example.com": "id-one"},
			expectByServerID: map[string]string{"id-one": "https://one.example.com"},
		},
		{
			name: "duplicate BaseURL last entry wins",
			servers: []MCPServerConfig{
				{ID: "id-first", Name: "first", BaseURL: "https://dup.example.com"},
				{ID: "id-last", Name: "last", BaseURL: "https://dup.example.com"},
			},
			expectByOrigin: map[string]string{"https://dup.example.com": "id-last"},
			expectByServerID: map[string]string{
				"id-first": "https://dup.example.com",
				"id-last":  "https://dup.example.com",
			},
		},
		{
			name: "disabled server included",
			servers: []MCPServerConfig{
				{ID: "id-disabled", Name: "off", Enabled: false, BaseURL: "https://off.example.com"},
			},
			expectByOrigin:   map[string]string{"https://off.example.com": "id-disabled"},
			expectByServerID: map[string]string{"id-disabled": "https://off.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &MCPConfig{Servers: tt.servers}
			require.Equal(t, tt.expectByOrigin, cfg.ServerIDByOrigin())
			require.Equal(t, tt.expectByServerID, cfg.OriginByServerID())
		})
	}
}

func TestReconcileMCPServerIDs(t *testing.T) {
	tests := []struct {
		name      string
		next      []MCPServerConfig
		prev      []MCPServerConfig
		expectIDs []string
	}{
		{
			name: "ID already present untouched",
			next: []MCPServerConfig{
				{ID: "keep-me", Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectIDs: []string{"keep-me"},
		},
		{
			name: "match by name",
			next: []MCPServerConfig{
				{Name: "srv", BaseURL: "https://new-url.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://old-url.example.com"},
			},
			expectIDs: []string{"prev-id"},
		},
		{
			name: "match by BaseURL when renamed",
			next: []MCPServerConfig{
				{Name: "renamed", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectIDs: []string{"prev-id"},
		},
		{
			name: "each prev entry consumed at most once",
			next: []MCPServerConfig{
				{Name: "srv", BaseURL: "https://one.example.com"},
				{Name: "other", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectIDs: []string{"prev-id", ""},
		},
		{
			name: "no match keeps empty ID",
			next: []MCPServerConfig{
				{Name: "brand-new", BaseURL: "https://new.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://old.example.com"},
			},
			expectIDs: []string{""},
		},
		{
			name: "prev entries without IDs cannot be claimed",
			next: []MCPServerConfig{
				{Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectIDs: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ReconcileMCPServerIDs(tt.next, tt.prev)
			require.Len(t, result, len(tt.expectIDs))
			for i, id := range tt.expectIDs {
				require.Equal(t, id, result[i].ID, "server %d", i)
			}
		})
	}
}

func TestServerConfigGetToolPolicyIgnoresRetrievalOverride(t *testing.T) {
	serverConfig := &MCPServerConfig{
		Enabled: true,
		ToolConfigs: []MCPToolConfig{
			{
				Name:                         "get_issue",
				Policy:                       MCPToolPolicyAutoRunEverywhere,
				Enabled:                      true,
				RetrievalDescriptionOverride: strings.Repeat("override ", 4),
			},
		},
	}

	policy, enabled := serverConfig.GetToolPolicy("get_issue")
	require.Equal(t, MCPToolPolicyAutoRunEverywhere, policy)
	require.True(t, enabled)
}
