// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClientManagerReInitIdleTimeoutDefaulting(t *testing.T) {
	testCases := []struct {
		name                string
		idleTimeoutMinutes  int
		expectedConfigValue int
		expectedTimeout     time.Duration
	}{
		{
			name:                "defaults when timeout is zero",
			idleTimeoutMinutes:  0,
			expectedConfigValue: 30,
			expectedTimeout:     30 * time.Minute,
		},
		{
			name:                "defaults when timeout is negative",
			idleTimeoutMinutes:  -10,
			expectedConfigValue: 30,
			expectedTimeout:     30 * time.Minute,
		},
		{
			name:                "keeps positive timeout",
			idleTimeoutMinutes:  12,
			expectedConfigValue: 12,
			expectedTimeout:     12 * time.Minute,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := &ClientManager{}
			t.Cleanup(manager.Close)

			manager.ReInit(Config{
				IdleTimeoutMinutes: tc.idleTimeoutMinutes,
			}, nil)

			require.Equal(t, tc.expectedConfigValue, manager.config.IdleTimeoutMinutes)
			require.Equal(t, tc.expectedTimeout, manager.clientTimeout)
		})
	}
}

func TestClientManagerGetToolRetrievalOverridesForUserRemote(t *testing.T) {
	manager := &ClientManager{
		config: Config{
			Servers: []ServerConfig{
				{
					Name:    "Jira",
					Enabled: true,
					BaseURL: "https://jira.example.com",
					ToolConfigs: []ToolConfig{
						{Name: "get_issue", Policy: ToolPolicyAsk, Enabled: true, RetrievalDescriptionOverride: "Find Jira issues by key"},
						{Name: "create_issue", Policy: ToolPolicyAsk, Enabled: true},
					},
				},
			},
		},
	}

	overrides := manager.GetToolRetrievalOverridesForUser("user-id")

	require.Equal(t, map[string]MCPToolRetrievalOverride{
		MCPToolRetrievalOverrideKey("https://jira.example.com", "get_issue"): {
			Summary: "Find Jira issues by key",
		},
	}, overrides)
}

func TestClientManagerGetToolRetrievalOverridesForUserEmbedded(t *testing.T) {
	manager := &ClientManager{
		config: Config{
			EmbeddedServer: EmbeddedServerConfig{
				ToolConfigs: []ToolConfig{
					{Name: "search_users", Policy: ToolPolicyAsk, Enabled: true, RetrievalDescriptionOverride: "Find Mattermost people"},
				},
			},
		},
	}

	overrides := manager.GetToolRetrievalOverridesForUser("user-id")

	require.Equal(t, map[string]MCPToolRetrievalOverride{
		MCPToolRetrievalOverrideKey(EmbeddedClientKey, "search_users"): {
			Summary: "Find Mattermost people",
		},
	}, overrides)
}

func TestClientManagerGetToolRetrievalOverridesTrimsAndSkipsEmpty(t *testing.T) {
	manager := &ClientManager{
		config: Config{
			Servers: []ServerConfig{
				{
					Name:    "Jira",
					Enabled: true,
					BaseURL: "https://jira.example.com",
					ToolConfigs: []ToolConfig{
						{Name: "get_issue", RetrievalDescriptionOverride: "  Find Jira issues  "},
						{Name: "create_issue", RetrievalDescriptionOverride: "   "},
					},
				},
			},
		},
	}

	overrides := manager.GetToolRetrievalOverridesForUser("user-id")

	require.Equal(t, map[string]MCPToolRetrievalOverride{
		MCPToolRetrievalOverrideKey("https://jira.example.com", "get_issue"): {
			Summary: "Find Jira issues",
		},
	}, overrides)
}

func TestClientManagerGetToolRetrievalOverridesLastDuplicateWins(t *testing.T) {
	manager := &ClientManager{
		config: Config{
			Servers: []ServerConfig{
				{
					Name:    "Jira",
					Enabled: true,
					BaseURL: "https://jira.example.com",
					ToolConfigs: []ToolConfig{
						{Name: "get_issue", RetrievalDescriptionOverride: "old summary"},
						{Name: "get_issue", RetrievalDescriptionOverride: "new summary"},
					},
				},
			},
		},
	}

	overrides := manager.GetToolRetrievalOverridesForUser("user-id")

	require.Equal(t, "new summary", overrides[MCPToolRetrievalOverrideKey("https://jira.example.com", "get_issue")].Summary)
}

func TestClientManagerGetToolRetrievalOverridesDisabledServer(t *testing.T) {
	manager := &ClientManager{
		config: Config{
			Servers: []ServerConfig{
				{
					Name:    "Jira",
					Enabled: false,
					BaseURL: "https://jira.example.com",
					ToolConfigs: []ToolConfig{
						{Name: "get_issue", RetrievalDescriptionOverride: "Find Jira issues"},
					},
				},
			},
		},
	}

	require.Empty(t, manager.GetToolRetrievalOverridesForUser("user-id"))
}
