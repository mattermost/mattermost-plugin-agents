// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
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

func TestClientManagerInvalidateUserClients(t *testing.T) {
	now := time.Now()
	manager := &ClientManager{
		clients: map[string]*UserClients{
			"user-1": {
				clients: map[string]*Client{},
			},
			"user-2": {
				clients: map[string]*Client{},
			},
		},
		activity: map[string]time.Time{
			"user-1": now,
			"user-2": now.Add(time.Minute),
		},
	}

	manager.InvalidateUserClients("user-1")

	require.NotContains(t, manager.clients, "user-1")
	require.NotContains(t, manager.activity, "user-1")
	require.Contains(t, manager.clients, "user-2")
	require.Equal(t, now.Add(time.Minute), manager.activity["user-2"])

	manager.InvalidateUserClients("missing-user")
	manager.InvalidateUserClients("")

	require.Contains(t, manager.clients, "user-2")
	require.Equal(t, now.Add(time.Minute), manager.activity["user-2"])
}

func TestClientManagerCreateAndStoreUserClientSetsInitialActivity(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Maybe()
	client := pluginapi.NewClient(mockAPI, nil)
	manager := &ClientManager{
		config:   Config{},
		log:      client.Log,
		clients:  make(map[string]*UserClients),
		activity: make(map[string]time.Time),
	}

	before := time.Now()
	userClients, mcpErrors := manager.createAndStoreUserClient("user-1")
	after := time.Now()

	require.NotNil(t, userClients)
	require.Nil(t, mcpErrors)
	require.Contains(t, manager.clients, "user-1")

	lastActivity, ok := manager.activity["user-1"]
	require.True(t, ok)
	require.False(t, lastActivity.Before(before))
	require.False(t, lastActivity.After(after))
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
	require.Empty(t, manager.activity)
}
