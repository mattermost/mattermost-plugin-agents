// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// setupTestOAuthManager creates a test OAuth manager with mocked dependencies
func setupTestOAuthManager(t *testing.T) (*OAuthManager, *mocks.MockClient) {
	return setupTestOAuthManagerWithLookup(t, nil)
}

func setupTestOAuthManagerWithLookup(t *testing.T, lookup ServerConfigLookup) (*OAuthManager, *mocks.MockClient) {
	return setupTestOAuthManagerFull(t, lookup, nil)
}

func setupTestOAuthManagerFull(t *testing.T, lookup ServerConfigLookup, httpClient *http.Client) (*OAuthManager, *mocks.MockClient) {
	mockClient := mocks.NewMockClient(t)
	manager := NewOAuthManager(mockClient, "http://test.com/callback", httpClient, lookup)
	return manager, mockClient
}

func TestBuildClientCredentialsKey(t *testing.T) {
	_, _ = setupTestOAuthManager(t)

	tests := []struct {
		name      string
		serverURL string
		wantSame  bool
		otherURL  string
	}{
		{
			name:      "basic URL",
			serverURL: "https://api.example.com",
			wantSame:  true,
			otherURL:  "https://api.example.com",
		},
		{
			name:      "different URLs produce different keys",
			serverURL: "https://api.example.com",
			wantSame:  false,
			otherURL:  "https://api.different.com",
		},
		{
			name:      "URL with path",
			serverURL: "https://api.example.com/v1/mcp",
			wantSame:  true,
			otherURL:  "https://api.example.com/v1/mcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key1 := buildClientCredentialsKey(tt.serverURL)
			key2 := buildClientCredentialsKey(tt.otherURL)

			// Keys should always start with the prefix
			require.Contains(t, key1, "mcp_oauth_client_v2")
			require.Contains(t, key2, "mcp_oauth_client_v2")

			// Keys should be consistent for same URL
			if tt.wantSame {
				require.Equal(t, key1, key2)
			} else {
				require.NotEqual(t, key1, key2)
			}
		})
	}
}

func TestBuildSessionKey(t *testing.T) {
	_, _ = setupTestOAuthManager(t)

	tests := []struct {
		name   string
		userID string
		state  string
	}{
		{
			name:   "basic session key",
			userID: "user123",
			state:  "state456",
		},
		{
			name:   "different user and state",
			userID: "user789",
			state:  "state999",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := buildSessionKey(tt.userID, tt.state)

			// Should contain both user ID and state
			require.Contains(t, key, tt.userID)
			require.Contains(t, key, tt.state)
			require.Contains(t, key, "oauth_session")

			// Should be consistent for same inputs
			key2 := buildSessionKey(tt.userID, tt.state)
			require.Equal(t, key, key2)
		})
	}

	// Different inputs must produce different keys
	key1 := buildSessionKey("user123", "state456")
	key2 := buildSessionKey("user789", "state999")
	require.NotEqual(t, key1, key2)
}

func TestLoadOrCreateClientCredentials_ExistingCredentials(t *testing.T) {
	manager, mockClient := setupTestOAuthManager(t)

	serverURL := "https://api.example.com"
	existingCreds := &ClientCredentials{
		ClientID:     "existing-client-id",
		ClientSecret: "existing-client-secret",
		ServerURL:    serverURL,
		CreatedAt:    time.Now(),
	}

	// Mock KV store returning existing credentials
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.ClientCredentials")).Run(func(args mock.Arguments) {
		creds := args.Get(1).(*ClientCredentials)
		*creds = *existingCreds
	}).Return(nil)

	ctx := context.Background()
	creds, err := manager.loadOrCreateClientCredentials(ctx, serverURL, nil)

	require.NoError(t, err)
	require.NotNil(t, creds)
	require.Equal(t, existingCreds.ClientID, creds.ClientID)
	require.Equal(t, existingCreds.ClientSecret, creds.ClientSecret)
	require.Equal(t, existingCreds.ServerURL, creds.ServerURL)
}

func TestLoadOrCreateClientCredentials_StaticCredentials(t *testing.T) {
	manager, _ := setupTestOAuthManager(t)

	serverURL := "https://github.com/login/oauth"
	staticCreds := &StaticOAuthCredentials{
		ClientID:     "static-github-client-id",
		ClientSecret: "static-github-client-secret",
	}

	ctx := context.Background()
	creds, err := manager.loadOrCreateClientCredentials(ctx, serverURL, staticCreds)

	require.NoError(t, err)
	require.NotNil(t, creds)
	require.Equal(t, "static-github-client-id", creds.ClientID)
	require.Equal(t, "static-github-client-secret", creds.ClientSecret)
	require.Equal(t, serverURL, creds.ServerURL)
}

func TestLoadOrCreateClientCredentials_StaticCredentialsSkipKVStore(t *testing.T) {
	manager, mockClient := setupTestOAuthManager(t)

	serverURL := "https://github.com/login/oauth"
	staticCreds := &StaticOAuthCredentials{
		ClientID:     "static-client-id",
		ClientSecret: "static-client-secret",
	}

	ctx := context.Background()
	creds, err := manager.loadOrCreateClientCredentials(ctx, serverURL, staticCreds)

	require.NoError(t, err)
	require.NotNil(t, creds)
	require.Equal(t, "static-client-id", creds.ClientID)
	require.Equal(t, "static-client-secret", creds.ClientSecret)
	mockClient.AssertNotCalled(t, "KVGet", mock.Anything, mock.Anything)
}

func TestLoadOrCreateClientCredentials_NilStaticCredsFallsBackToKVStore(t *testing.T) {
	manager, mockClient := setupTestOAuthManager(t)

	serverURL := "https://api.example.com"
	existingCreds := &ClientCredentials{
		ClientID:     "kv-client-id",
		ClientSecret: "kv-client-secret",
		ServerURL:    serverURL,
		CreatedAt:    time.Now(),
	}

	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.ClientCredentials")).Run(func(args mock.Arguments) {
		creds := args.Get(1).(*ClientCredentials)
		*creds = *existingCreds
	}).Return(nil)

	ctx := context.Background()
	creds, err := manager.loadOrCreateClientCredentials(ctx, serverURL, nil)

	require.NoError(t, err)
	require.NotNil(t, creds)
	require.Equal(t, "kv-client-id", creds.ClientID)
	require.Equal(t, "kv-client-secret", creds.ClientSecret)
	mockClient.AssertCalled(t, "KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.ClientCredentials"))
}

func TestLoadOrCreateClientCredentials_EmptyStaticCredsFallsBackToKVStore(t *testing.T) {
	manager, mockClient := setupTestOAuthManager(t)

	serverURL := "https://api.example.com"
	existingCreds := &ClientCredentials{
		ClientID:     "kv-client-id",
		ClientSecret: "kv-client-secret",
		ServerURL:    serverURL,
		CreatedAt:    time.Now(),
	}

	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.ClientCredentials")).Run(func(args mock.Arguments) {
		creds := args.Get(1).(*ClientCredentials)
		*creds = *existingCreds
	}).Return(nil)

	ctx := context.Background()
	creds, err := manager.loadOrCreateClientCredentials(ctx, serverURL, &StaticOAuthCredentials{})

	require.NoError(t, err)
	require.NotNil(t, creds)
	require.Equal(t, "kv-client-id", creds.ClientID)
	require.Equal(t, "kv-client-secret", creds.ClientSecret)
	mockClient.AssertCalled(t, "KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.ClientCredentials"))
}

func TestStaticCredsHelpers(t *testing.T) {
	tests := []struct {
		name         string
		creds        *StaticOAuthCredentials
		wantClientID string
	}{
		{
			name:         "nil creds returns empty string",
			creds:        nil,
			wantClientID: "",
		},
		{
			name: "populated creds returns value",
			creds: &StaticOAuthCredentials{
				ClientID:     "test-id",
				ClientSecret: "test-secret",
			},
			wantClientID: "test-id",
		},
		{
			name:         "empty creds returns empty string",
			creds:        &StaticOAuthCredentials{},
			wantClientID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantClientID, staticCredsClientID(tt.creds))
		})
	}
}

func TestProcessCallback_InvalidSession(t *testing.T) {
	manager, mockClient := setupTestOAuthManager(t)

	userID := "user123"
	state := "test-state"
	code := "auth-code"

	// Mock session not found - KVGet should return an error
	appErr := model.NewAppError("test", "not_found", nil, "session not found", 404)
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession")).Return(appErr)

	ctx := context.Background()
	session, err := manager.ProcessCallback(ctx, userID, state, code)

	require.Error(t, err)
	require.Nil(t, session)
	require.Contains(t, err.Error(), "invalid or expired session")
}

func TestProcessCallback_StateValidation(t *testing.T) {
	manager, mockClient := setupTestOAuthManager(t)

	userID := "user123"
	serverID := "server456"
	serverURL := "https://api.example.com"
	correctState := "correct-state"
	wrongState := "wrong-state"

	// Test mismatched states
	session := &OAuthSession{
		UserID:       userID,
		ServerID:     serverID,
		ServerURL:    serverURL,
		CodeVerifier: "test-verifier",
		State:        correctState,
		CreatedAt:    time.Now(),
	}

	// Mock session retrieval for state mismatch test
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession")).Run(func(args mock.Arguments) {
		sess := args.Get(1).(*OAuthSession)
		*sess = *session
	}).Return(nil).Once()
	mockClient.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Once()

	ctx := context.Background()
	session, err := manager.ProcessCallback(ctx, userID, wrongState, "auth-code")

	require.Error(t, err)
	require.Nil(t, session)
	require.Contains(t, err.Error(), "state mismatch")
	mockClient.AssertCalled(t, "KVDelete", mock.AnythingOfType("string"))
}

func TestProcessCallback_UserIDValidation(t *testing.T) {
	manager, mockClient := setupTestOAuthManager(t)

	correctUserID := "user123"
	wrongUserID := "wrong-user"
	serverID := "server456"
	serverURL := "https://api.example.com"
	state := "test-state"

	// Create test session with specific user ID
	session := &OAuthSession{
		UserID:       correctUserID,
		ServerID:     serverID,
		ServerURL:    serverURL,
		CodeVerifier: "test-verifier",
		State:        state,
		CreatedAt:    time.Now(),
	}

	// Mock session retrieval
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession")).Run(func(args mock.Arguments) {
		sess := args.Get(1).(*OAuthSession)
		*sess = *session
	}).Return(nil)
	mockClient.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Once()

	ctx := context.Background()
	session, err := manager.ProcessCallback(ctx, wrongUserID, state, "auth-code")

	require.Error(t, err)
	require.Nil(t, session)
	require.Contains(t, err.Error(), "user ID mismatch")
	require.Contains(t, err.Error(), correctUserID)
	require.Contains(t, err.Error(), wrongUserID)
	mockClient.AssertCalled(t, "KVDelete", mock.AnythingOfType("string"))
}

func TestProcessCallback_RederivesStaticCredsFromConfig(t *testing.T) {
	serverID := "my-server"
	serverURL := "https://api.example.com"

	lookup := func(id string) (ServerConfig, bool) {
		if id == serverID {
			return ServerConfig{
				Name:         serverID,
				BaseURL:      serverURL,
				ClientID:     "cfg-client-id",
				ClientSecret: "cfg-client-secret",
			}, true
		}
		return ServerConfig{}, false
	}

	manager, mockClient := setupTestOAuthManagerFull(t, lookup, &http.Client{})

	userID := "user123"
	state := "test-state"

	session := &OAuthSession{
		UserID:            userID,
		ServerID:          serverID,
		ServerURL:         serverURL,
		ServerMetadataURL: "",
		CodeVerifier:      "test-verifier",
		State:             state,
		StaticClientID:    "cfg-client-id",
		CreatedAt:         time.Now(),
	}

	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession")).Run(func(args mock.Arguments) {
		sess := args.Get(1).(*OAuthSession)
		*sess = *session
	}).Return(nil)
	mockClient.On("KVDelete", mock.AnythingOfType("string")).Return(nil)

	ctx := context.Background()
	// ProcessCallback will fail at the token exchange (no real OAuth server),
	// but we can verify it gets past session validation and attempts to create
	// an OAuth config -- which means the static creds were successfully
	// re-derived from the config lookup.
	result, err := manager.ProcessCallback(ctx, userID, state, "auth-code")

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "failed to exchange code for token")
}

func TestProcessCallback_LogsWarningWhenLookupMissesServer(t *testing.T) {
	lookup := func(_ string) (ServerConfig, bool) {
		return ServerConfig{}, false
	}

	manager, mockClient := setupTestOAuthManagerFull(t, lookup, &http.Client{})

	userID := "user123"
	state := "test-state"
	serverID := "removed-server"

	session := &OAuthSession{
		UserID:            userID,
		ServerID:          serverID,
		ServerURL:         "https://api.example.com",
		ServerMetadataURL: "",
		CodeVerifier:      "test-verifier",
		State:             state,
		StaticClientID:    "some-client-id",
		CreatedAt:         time.Now(),
	}

	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession")).Run(func(args mock.Arguments) {
		sess := args.Get(1).(*OAuthSession)
		*sess = *session
	}).Return(nil)
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.ClientCredentials")).Return(nil)
	mockClient.On("KVDelete", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("LogWarn", mock.AnythingOfType("string"), mock.Anything).Return()

	ctx := context.Background()
	result, err := manager.ProcessCallback(ctx, userID, state, "auth-code")

	require.Error(t, err)
	require.Nil(t, result)

	expectedMsg := "Static OAuth credentials were expected but server config not found; falling back to dynamic registration"
	mockClient.AssertCalled(t, "LogWarn", expectedMsg, []interface{}{"serverID", serverID})
}
