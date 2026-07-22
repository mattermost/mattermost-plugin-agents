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

func TestMCPAppsConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MCPAppsConfig
		wantErr bool
	}{
		{
			name:    "zero value",
			cfg:     MCPAppsConfig{},
			wantErr: false,
		},
		{
			name:    "valid https URL",
			cfg:     MCPAppsConfig{SandboxURL: "https://apps.example.com"},
			wantErr: false,
		},
		{
			name:    "valid with port + path",
			cfg:     MCPAppsConfig{SandboxURL: "https://mm.example.com:8443/apps"},
			wantErr: false,
		},
		{
			name:    "relative URL",
			cfg:     MCPAppsConfig{SandboxURL: "/apps"},
			wantErr: true,
		},
		{
			name:    "non-http scheme",
			cfg:     MCPAppsConfig{SandboxURL: "ftp://x"},
			wantErr: true,
		},
		{
			name:    "URL with query",
			cfg:     MCPAppsConfig{SandboxURL: "https://x?a=1"},
			wantErr: true,
		},
		{
			name:    "URL with fragment",
			cfg:     MCPAppsConfig{SandboxURL: "https://x#f"},
			wantErr: true,
		},
		{
			name:    "valid listen :8066",
			cfg:     MCPAppsConfig{SandboxListenAddress: ":8066"},
			wantErr: false,
		},
		{
			name:    "valid listen host:port",
			cfg:     MCPAppsConfig{SandboxListenAddress: "127.0.0.1:9000"},
			wantErr: false,
		},
		{
			name:    "listen without port",
			cfg:     MCPAppsConfig{SandboxListenAddress: "localhost"},
			wantErr: true,
		},
		{
			name:    "listen garbage",
			cfg:     MCPAppsConfig{SandboxListenAddress: "not a port"},
			wantErr: true,
		},
		{
			name:    "listen port 0 rejected",
			cfg:     MCPAppsConfig{SandboxListenAddress: ":0"},
			wantErr: true,
		},
		{
			name:    "listen port too high",
			cfg:     MCPAppsConfig{SandboxListenAddress: ":99999"},
			wantErr: true,
		},
		{
			name:    "listen non-numeric port",
			cfg:     MCPAppsConfig{SandboxListenAddress: ":abc"},
			wantErr: true,
		},
		{
			name:    "listen port 1 ok",
			cfg:     MCPAppsConfig{SandboxListenAddress: ":1"},
			wantErr: false,
		},
		{
			name:    "listen port 65535 ok",
			cfg:     MCPAppsConfig{SandboxListenAddress: ":65535"},
			wantErr: false,
		},
		{
			name:    "userinfo rejected",
			cfg:     MCPAppsConfig{SandboxURL: "https://u:p@apps.example.com"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestMCPAppsConfigJSONKeys(t *testing.T) {
	data, err := json.Marshal(MCPConfig{
		Apps: MCPAppsConfig{
			Enabled:                        true,
			SandboxURL:                     "https://apps.example.com",
			SandboxListenAddress:           ":8066",
			AllowInsecureSameOriginSandbox: true,
		},
	})
	require.NoError(t, err)
	raw := string(data)
	require.Contains(t, raw, `"apps"`)
	require.Contains(t, raw, `"sandboxURL"`)
	require.Contains(t, raw, `"sandboxListenAddress"`)
	require.Contains(t, raw, `"allowInsecureSameOriginSandbox"`)
}
