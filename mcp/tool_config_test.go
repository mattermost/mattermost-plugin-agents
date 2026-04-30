// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServerConfigGetToolPolicy(t *testing.T) {
	tests := []struct {
		name        string
		config      *ServerConfig
		toolName    string
		wantPolicy  string
		wantEnabled bool
	}{
		{
			name:        "nil config returns ask false",
			config:      nil,
			toolName:    "search",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: false,
		},
		{
			name:        "empty tool name returns ask false",
			config:      &ServerConfig{},
			toolName:    "",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: false,
		},
		{
			name:        "missing tool config returns ask true",
			config:      &ServerConfig{Enabled: true},
			toolName:    "search",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: true,
		},
		{
			name: "ask enabled",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAsk, Enabled: true},
				},
			},
			toolName:    "search",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: true,
		},
		{
			name: "auto run enabled",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAutoRunInDM, Enabled: true},
				},
			},
			toolName:    "search",
			wantPolicy:  ToolPolicyAutoRunInDM,
			wantEnabled: true,
		},
		{
			name: "auto run everywhere enabled",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAutoRunEverywhere, Enabled: true},
				},
			},
			toolName:    "search",
			wantPolicy:  ToolPolicyAutoRunEverywhere,
			wantEnabled: true,
		},
		{
			name: "auto run disabled",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAutoRunInDM, Enabled: false},
				},
			},
			toolName:    "search",
			wantPolicy:  ToolPolicyAutoRunInDM,
			wantEnabled: false,
		},
		{
			name: "invalid policy normalizes to ask",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: "invalid", Enabled: true},
				},
			},
			toolName:    "search",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: true,
		},
		{
			name: "empty policy normalizes to ask",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: "", Enabled: true},
				},
			},
			toolName:    "search",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: true,
		},
		{
			name: "duplicate tool configs last matching entry wins",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAutoRunInDM, Enabled: true},
					{Name: "search", Policy: ToolPolicyAsk, Enabled: false},
				},
			},
			toolName:    "search",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: false,
		},
		{
			name: "exact name match only",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "get_me", Policy: ToolPolicyAutoRunInDM, Enabled: true},
				},
			},
			toolName:    "GET_ME",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: true,
		},
		{
			name: "disabled server returns ask false regardless of tool config",
			config: &ServerConfig{
				Enabled: false,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAutoRunInDM, Enabled: true},
				},
			},
			toolName:    "search",
			wantPolicy:  ToolPolicyAsk,
			wantEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, enabled := tt.config.GetToolPolicy(tt.toolName)
			require.Equal(t, tt.wantPolicy, policy)
			require.Equal(t, tt.wantEnabled, enabled)
		})
	}
}

func TestServerConfigIsToolAutoRunInDM(t *testing.T) {
	tests := []struct {
		name     string
		config   *ServerConfig
		toolName string
		want     bool
	}{
		{
			name:     "nil config",
			config:   nil,
			toolName: "search",
			want:     false,
		},
		{
			name:     "missing tool config",
			config:   &ServerConfig{},
			toolName: "search",
			want:     false,
		},
		{
			name: "ask enabled",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAsk, Enabled: true},
				},
			},
			toolName: "search",
			want:     false,
		},
		{
			name: "auto run disabled",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAutoRunInDM, Enabled: false},
				},
			},
			toolName: "search",
			want:     false,
		},
		{
			name: "auto run enabled",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAutoRunInDM, Enabled: true},
				},
			},
			toolName: "search",
			want:     true,
		},
		{
			name: "auto run everywhere enabled",
			config: &ServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAutoRunEverywhere, Enabled: true},
				},
			},
			toolName: "search",
			want:     true,
		},
		{
			name: "disabled server never auto runs",
			config: &ServerConfig{
				Enabled: false,
				ToolConfigs: []ToolConfig{
					{Name: "search", Policy: ToolPolicyAutoRunInDM, Enabled: true},
				},
			},
			toolName: "search",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.config.IsToolAutoRunInDM(tt.toolName))
		})
	}
}
