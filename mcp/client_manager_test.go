// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

type recordKVSetWithExpiryClient struct {
	mmapi.Client
	key    string
	value  any
	ttl    time.Duration
	setErr error
}

func (c *recordKVSetWithExpiryClient) KVSetWithExpiry(key string, value interface{}, ttl time.Duration) error {
	c.key = key
	c.value = value
	c.ttl = ttl
	return c.setErr
}

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
	testCases := []struct {
		name                 string
		userID               string
		expectedClientKeys   []string
		expectedActivityKeys []string
	}{
		{
			name:                 "removes existing user",
			userID:               "user-1",
			expectedClientKeys:   []string{"user-2"},
			expectedActivityKeys: []string{"user-2"},
		},
		{
			name:                 "ignores missing user",
			userID:               "missing-user",
			expectedClientKeys:   []string{"user-1", "user-2"},
			expectedActivityKeys: []string{"user-1", "user-2"},
		},
		{
			name:                 "ignores empty user",
			userID:               "",
			expectedClientKeys:   []string{"user-1", "user-2"},
			expectedActivityKeys: []string{"user-1", "user-2"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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

			manager.InvalidateUserClients(tc.userID)

			require.Len(t, manager.clients, len(tc.expectedClientKeys))
			for _, key := range tc.expectedClientKeys {
				require.Contains(t, manager.clients, key)
			}
			require.Len(t, manager.activity, len(tc.expectedActivityKeys))
			for _, key := range tc.expectedActivityKeys {
				require.Contains(t, manager.activity, key)
			}
			require.Equal(t, now.Add(time.Minute), manager.activity["user-2"])
		})
	}
}

func TestClientManagerCreateAndStoreUserClientSetsInitialActivity(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("LogDebug", "No remote MCP servers provided for user", "userID", "user-1").Return().Maybe()
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
	testCases := []struct {
		name                     string
		manager                  *ClientManager
		expectedErr              string
		expectedStoredKey        string
		expectedStoredAuthURL    string
		expectedStoredTTL        time.Duration
		expectPersistenceAttempt bool
	}{
		{
			name: "persists state when oauth manager exists",
			manager: func() *ClientManager {
				storeClient := &recordKVSetWithExpiryClient{}
				manager := &ClientManager{
					clients: map[string]*UserClients{
						"user-1": {clients: map[string]*Client{}},
					},
					activity: map[string]time.Time{
						"user-1": time.Now(),
					},
				}
				manager.oauthManager = NewOAuthManager(storeClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", nil, nil)
				return manager
			}(),
			expectedStoredKey:        "mcp_oauth_needed_v1_user-1_GitHub",
			expectedStoredAuthURL:    "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/GitHub/start",
			expectedStoredTTL:        oauthNeededStateTTL,
			expectPersistenceAttempt: true,
		},
		{
			name: "returns persistence error but still invalidates",
			manager: func() *ClientManager {
				storeClient := &recordKVSetWithExpiryClient{
					setErr: model.NewAppError("test", "oauth_needed_store_failed", nil, "persist failed", http.StatusInternalServerError),
				}
				manager := &ClientManager{
					clients: map[string]*UserClients{
						"user-1": {clients: map[string]*Client{}},
					},
					activity: map[string]time.Time{
						"user-1": time.Now(),
					},
				}
				manager.oauthManager = NewOAuthManager(storeClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", nil, nil)
				return manager
			}(),
			expectedErr:              "failed to store OAuth-needed state",
			expectedStoredKey:        "mcp_oauth_needed_v1_user-1_GitHub",
			expectedStoredAuthURL:    "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/GitHub/start",
			expectedStoredTTL:        oauthNeededStateTTL,
			expectPersistenceAttempt: true,
		},
		{
			name: "still invalidates without oauth manager",
			manager: &ClientManager{
				clients: map[string]*UserClients{
					"user-1": {clients: map[string]*Client{}},
				},
				activity: map[string]time.Time{
					"user-1": time.Now(),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.manager.MarkOAuthNeeded("user-1", "GitHub", "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/GitHub/start")

			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErr)
			}
			require.Empty(t, tc.manager.clients)
			require.Empty(t, tc.manager.activity)

			if !tc.expectPersistenceAttempt {
				return
			}

			storeClient, ok := tc.manager.oauthManager.pluginAPI.(*recordKVSetWithExpiryClient)
			require.True(t, ok)
			require.Equal(t, tc.expectedStoredKey, storeClient.key)
			require.Equal(t, tc.expectedStoredTTL, storeClient.ttl)

			state, ok := storeClient.value.(*OAuthNeededState)
			require.True(t, ok)
			require.Equal(t, tc.expectedStoredAuthURL, state.AuthURL)
		})
	}
}

func TestClientManagerProcessOAuthCallbackRequiresOAuthManager(t *testing.T) {
	manager := &ClientManager{}

	session, err := manager.ProcessOAuthCallback(t.Context(), "user-1", "state", "code")

	require.Nil(t, session)
	require.ErrorIs(t, err, ErrOAuthNotConfigured)
}

func TestClientManagerDisconnectUserOAuthRequiresOAuthManager(t *testing.T) {
	manager := &ClientManager{}

	err := manager.DisconnectUserOAuth("user-1", "GitHub")

	require.ErrorIs(t, err, ErrOAuthNotConfigured)
}
