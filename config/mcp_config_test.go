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

// HasServiceAccountAuth must be true exactly when EffectiveServiceAccountHeaders is non-empty.
func TestMCPServerConfigServiceAccountHeaderFiltering(t *testing.T) {
	tests := []struct {
		name         string
		serverConfig *MCPServerConfig
		wantHeaders  map[string]string
	}{
		{
			name:         "only blank entries",
			serverConfig: &MCPServerConfig{Enabled: true, ServiceAccountHeaders: map[string]string{"": "Bearer pat", "Authorization": "   "}},
		},
		{
			name: "blank entries alongside a valid entry are dropped",
			serverConfig: &MCPServerConfig{
				Enabled: true,
				ServiceAccountHeaders: map[string]string{
					"":              "",
					"   ":           "Bearer pat",
					"X-Blank-Value": "  ",
					"Authorization": "Bearer pat",
				},
			},
			wantHeaders: map[string]string{"Authorization": "Bearer pat"},
		},
		{
			name: "base headers do not count as service account auth",
			serverConfig: &MCPServerConfig{
				Enabled: true,
				Headers: map[string]string{"Authorization": "Bearer shared"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantHeaders, tt.serverConfig.EffectiveServiceAccountHeaders())
			require.Equal(t, len(tt.wantHeaders) > 0, tt.serverConfig.HasServiceAccountAuth())
		})
	}
}

// A broken JSON tag would make Config.Clone silently drop SA credentials cluster-wide.
func TestConfigClonePreservesServiceAccountHeaders(t *testing.T) {
	original := &Config{
		MCP: MCPConfig{
			Servers: []MCPServerConfig{
				{
					Name:                  "Jira",
					Enabled:               true,
					BaseURL:               "https://jira.example.com",
					Headers:               map[string]string{"X-Trace": "on"},
					ServiceAccountHeaders: map[string]string{"Authorization": "Bearer service-pat"},
				},
			},
		},
	}

	// No Config-wide equality: the JSON deep copy normalizes nil json.RawMessage fields to "null".
	clone := original.Clone()
	require.Equal(t, original.MCP.Servers, clone.MCP.Servers)

	clone.MCP.Servers[0].ServiceAccountHeaders["Authorization"] = "Bearer tampered"
	require.Equal(t, "Bearer service-pat", original.MCP.Servers[0].ServiceAccountHeaders["Authorization"])
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
