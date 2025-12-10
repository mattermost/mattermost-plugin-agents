// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// setupTestOAuthManager creates a test OAuth manager with mocked dependencies
func setupTestOAuthManager(t *testing.T) (*OAuthManager, *mocks.MockClient) {
	mockClient := mocks.NewMockClient(t)
	manager := NewOAuthManager(mockClient, "http://test.com/callback")

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
	creds, err := manager.loadOrCreateClientCredentials(ctx, serverURL)

	require.NoError(t, err)
	require.NotNil(t, creds)
	require.Equal(t, existingCreds.ClientID, creds.ClientID)
	require.Equal(t, existingCreds.ClientSecret, creds.ClientSecret)
	require.Equal(t, existingCreds.ServerURL, creds.ServerURL)
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
	_, err := manager.ProcessCallback(ctx, userID, state, code)

	require.Error(t, err)
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

	ctx := context.Background()
	_, err := manager.ProcessCallback(ctx, userID, wrongState, "auth-code")

	require.Error(t, err)
	require.Contains(t, err.Error(), "state mismatch")
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

	ctx := context.Background()
	_, err := manager.ProcessCallback(ctx, wrongUserID, state, "auth-code")

	require.Error(t, err)
	require.Contains(t, err.Error(), "user ID mismatch")
	require.Contains(t, err.Error(), correctUserID)
	require.Contains(t, err.Error(), wrongUserID)
}
