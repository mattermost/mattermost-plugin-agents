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

func TestMCPServerConfigServiceAccountHeadersMarshal(t *testing.T) {
	tests := []struct {
		name          string
		serverConfig  MCPServerConfig
		wantKeyInJSON bool
	}{
		{
			name: "populated map is serialized and decodes back unchanged",
			serverConfig: MCPServerConfig{
				Name:                  "Jira",
				BaseURL:               "https://jira.example.com",
				ServiceAccountHeaders: map[string]string{"Authorization": "Bearer service-pat"},
			},
			wantKeyInJSON: true,
		},
		{
			name:          "nil map is omitted",
			serverConfig:  MCPServerConfig{Name: "Jira"},
			wantKeyInJSON: false,
		},
		{
			name:          "empty map is omitted",
			serverConfig:  MCPServerConfig{Name: "Jira", ServiceAccountHeaders: map[string]string{}},
			wantKeyInJSON: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.serverConfig)
			require.NoError(t, err)

			var decoded MCPServerConfig
			require.NoError(t, json.Unmarshal(data, &decoded))
			if tt.wantKeyInJSON {
				require.Contains(t, string(data), `"serviceAccountHeaders"`)
				require.Equal(t, tt.serverConfig.ServiceAccountHeaders, decoded.ServiceAccountHeaders)
			} else {
				require.NotContains(t, string(data), "serviceAccountHeaders")
				require.Nil(t, decoded.ServiceAccountHeaders)
			}
		})
	}
}

func TestMCPServerConfigServiceAccountHeadersDecode(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantHeaders map[string]string
	}{
		{
			name:    "JSON without the key decodes to a nil map",
			payload: `{"name":"Jira","enabled":true}`,
		},
		{
			name:        "legacy JSON with only headers leaves service account headers nil",
			payload:     `{"name":"Jira","enabled":true,"headers":{"X-Trace":"on"}}`,
			wantHeaders: map[string]string{"X-Trace": "on"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var decoded MCPServerConfig
			require.NoError(t, json.Unmarshal([]byte(tt.payload), &decoded))
			require.Nil(t, decoded.ServiceAccountHeaders)
			require.Equal(t, tt.wantHeaders, decoded.Headers)
		})
	}
}

// HasServiceAccountAuth must be true exactly when EffectiveServiceAccountHeaders is non-empty.
func TestMCPServerConfigServiceAccountHeaderFiltering(t *testing.T) {
	tests := []struct {
		name         string
		serverConfig *MCPServerConfig
		wantHeaders  map[string]string
	}{
		{
			name:         "nil receiver",
			serverConfig: nil,
		},
		{
			name:         "nil map",
			serverConfig: &MCPServerConfig{Enabled: true},
		},
		{
			name:         "empty map",
			serverConfig: &MCPServerConfig{Enabled: true, ServiceAccountHeaders: map[string]string{}},
		},
		{
			name:         "empty header name",
			serverConfig: &MCPServerConfig{Enabled: true, ServiceAccountHeaders: map[string]string{"": "Bearer pat"}},
		},
		{
			name:         "whitespace header name",
			serverConfig: &MCPServerConfig{Enabled: true, ServiceAccountHeaders: map[string]string{"   ": "Bearer pat"}},
		},
		{
			name:         "empty header value",
			serverConfig: &MCPServerConfig{Enabled: true, ServiceAccountHeaders: map[string]string{"Authorization": ""}},
		},
		{
			name:         "whitespace header value",
			serverConfig: &MCPServerConfig{Enabled: true, ServiceAccountHeaders: map[string]string{"Authorization": "   "}},
		},
		{
			name:         "blank name and blank value",
			serverConfig: &MCPServerConfig{Enabled: true, ServiceAccountHeaders: map[string]string{"": ""}},
		},
		{
			name:         "one valid entry",
			serverConfig: &MCPServerConfig{Enabled: true, ServiceAccountHeaders: map[string]string{"Authorization": "Bearer pat"}},
			wantHeaders:  map[string]string{"Authorization": "Bearer pat"},
		},
		{
			name: "several valid entries are all kept",
			serverConfig: &MCPServerConfig{
				Enabled:               true,
				ServiceAccountHeaders: map[string]string{"Authorization": "Bearer pat", "X-Tenant": "acme"},
			},
			wantHeaders: map[string]string{"Authorization": "Bearer pat", "X-Tenant": "acme"},
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
			name:         "disabled server with a valid entry",
			serverConfig: &MCPServerConfig{Enabled: false, ServiceAccountHeaders: map[string]string{"Authorization": "Bearer pat"}},
			wantHeaders:  map[string]string{"Authorization": "Bearer pat"},
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
