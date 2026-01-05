// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/oauth2"
)

func buildSessionKey(userID, state string) string {
	oauthSessionKeyPrefix := "oauth_session"
	return fmt.Sprintf("%s_%s_%s", oauthSessionKeyPrefix, userID, state)
}

func buildClientCredentialsKey(serverURL string) string {
	oauthClientKeyPrefix := "mcp_oauth_client_v2"
	// Create a hash of the server URL to use as a consistent key
	hash := sha256.Sum256([]byte(serverURL))
	urlHash := hex.EncodeToString(hash[:])
	return fmt.Sprintf("%s_%s", oauthClientKeyPrefix, urlHash)
}

func buildTokenKey(userID, serverID string) string {
	prefix := "mcp_oauth_token_v1"
	return fmt.Sprintf("%s_%s_%s", prefix, userID, serverID)
}

// loadToken retrieves the OAuth token for a user and server from the KV store
// If no token is found, it returns nil to indicate no token exists
func (m *OAuthManager) loadToken(userID, serverID string) (*oauth2.Token, error) {
	tokenKey := buildTokenKey(userID, serverID)

	var oauth2Token oauth2.Token
	err := m.pluginAPI.KVGet(tokenKey, &oauth2Token)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve token from KV store: %w", err)
	}

	if oauth2Token.AccessToken == "" {
		// If no token is found, return nil to indicate no token exists
		return nil, nil
	}

	return &oauth2Token, nil
}

func (m *OAuthManager) storeToken(userID, serverID string, token *oauth2.Token) error {
	tokenKey := buildTokenKey(userID, serverID)

	if err := m.pluginAPI.KVSet(tokenKey, token); err != nil {
		return fmt.Errorf("failed to store token in KV store: %w", err)
	}

	return nil
}

func (m *OAuthManager) deleteToken(userID, serverID string) error {
	tokenKey := buildTokenKey(userID, serverID)
	if err := m.pluginAPI.KVDelete(tokenKey); err != nil {
		return fmt.Errorf("failed to delete token from KV store: %w", err)
	}
	return nil
}

type ClientCredentials struct {
	ClientID     string    `json:"clientID"`
	ClientSecret string    `json:"clientSecret"`
	ServerURL    string    `json:"serverURL"`
	CreatedAt    time.Time `json:"createdAt"`
}

func (m *OAuthManager) loadClientCredentials(serverURL string) (*ClientCredentials, error) {
	credKey := buildClientCredentialsKey(serverURL)

	var creds ClientCredentials
	err := m.pluginAPI.KVGet(credKey, &creds)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve client credentials from KV store: %w", err)
	}

	if creds.ClientID == "" || creds.ClientSecret == "" {
		// If no credentials are found, return nil to indicate no credentials exist
		return nil, nil
	}

	// Verify URL matches
	if creds.ServerURL != serverURL {
		return nil, fmt.Errorf("server URL mismatch in stored credentials (possible hash collision)")
	}

	return &creds, nil
}

func (m *OAuthManager) storeClientCredentials(creds *ClientCredentials) error {
	credKey := buildClientCredentialsKey(creds.ServerURL)

	credData, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to marshal client credentials: %w", err)
	}

	if err := m.pluginAPI.KVSet(credKey, credData); err != nil {
		return fmt.Errorf("failed to store client credentials: %w", err)
	}

	return nil
}

type OAuthSession struct {
	UserID            string    `json:"userID"`
	ServerID          string    `json:"serverID"`
	ServerURL         string    `json:"serverURL"`
	ServerMetadataURL string    `json:"serverMetadataURL"`
	CodeVerifier      string    `json:"codeVerifier"`
	State             string    `json:"state"`
	CreatedAt         time.Time `json:"createdAt"`
}

func (m *OAuthManager) loadSession(userID, state string) (*OAuthSession, error) {
	sessionKey := buildSessionKey(userID, state)

	var session OAuthSession
	err := m.pluginAPI.KVGet(sessionKey, &session)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve OAuth session from KV store: %w", err)
	}

	if session.UserID == "" || session.ServerID == "" || session.CodeVerifier == "" {
		// If no session is found, return nil to indicate no session exists
		return nil, nil
	}

	return &session, nil
}

func (m *OAuthManager) storeSession(session *OAuthSession) error {
	sessionKey := buildSessionKey(session.UserID, session.State)
	sessionData, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal OAuth session: %w", err)
	}

	if err := m.pluginAPI.KVSet(sessionKey, sessionData); err != nil {
		return fmt.Errorf("failed to store OAuth session: %w", err)
	}

	return nil
}

func (m *OAuthManager) deleteSession(userID, state string) error {
	sessionKey := buildSessionKey(userID, state)
	if err := m.pluginAPI.KVDelete(sessionKey); err != nil {
		return fmt.Errorf("failed to delete OAuth session: %w", err)
	}
	return nil
}
