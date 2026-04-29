// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/stretchr/testify/mock"
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

func TestClientManagerMarkOAuthNeededInvalidatesUserClient(t *testing.T) {
	manager := &ClientManager{}
	manager.clients = map[string]*UserClients{
		"user-1": {
			clients: map[string]*Client{},
		},
	}
	manager.activity = map[string]time.Time{
		"user-1": time.Now(),
	}

	mockClient := mocks.NewMockClient(t)
	mockClient.On("KVSetWithExpiry", "mcp_oauth_needed_v1_user-1_GitHub", mock.AnythingOfType("*mcp.OAuthNeededState"), oauthNeededStateTTL).Return(nil)
	manager.oauthManager = NewOAuthManager(mockClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", nil, nil)

	err := manager.MarkOAuthNeeded("user-1", "GitHub", "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/GitHub/start")
	require.NoError(t, err)
	require.Empty(t, manager.clients)
}
