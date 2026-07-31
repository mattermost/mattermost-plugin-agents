// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
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

func buildAuthNeededKey(userID, serverID string) string {
	prefix := "mcp_oauth_needed_v1"
	return fmt.Sprintf("%s_%s_%s", prefix, userID, serverID)
}

type OAuthNeededState struct {
	AuthURL string    `json:"authURL"`
	SeenAt  time.Time `json:"seenAt"`
}

// loadToken retrieves the OAuth token for a user and server from the KV store
// If no token is found, it returns nil to indicate no token exists
func (m *OAuthManager) loadToken(userID, serverID string) (*oauth2.Token, error) {
	tokenKey := buildTokenKey(userID, serverID)

	var oauth2Token oauth2.Token
	err := m.pluginAPI.KVGet(tokenKey, &oauth2Token)
	if err != nil {
		if mmapi.IsKVNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to retrieve token from KV store: %w", err)
	}

	if oauth2Token.AccessToken == "" {
		// If no token is found, return nil to indicate no token exists
		return nil, nil
	}

	return &oauth2Token, nil
}

// HasStoredToken returns true when a non-expired OAuth token exists for the
// given user and server. It does not refresh the token or contact upstream.
func (m *OAuthManager) HasStoredToken(userID, serverID string) (bool, error) {
	tok, err := m.loadToken(userID, serverID)
	if err != nil {
		return false, err
	}
	if tok == nil {
		return false, nil
	}
	// Consider a token present even if it might be expired — the caller only
	// needs to know whether the user has ever authenticated with this server.
	return true, nil
}

const oauthNeededStateTTL = 24 * time.Hour

func (m *OAuthManager) LoadAuthNeededState(userID, serverID string) (*OAuthNeededState, error) {
	authNeededKey := buildAuthNeededKey(userID, serverID)

	var state OAuthNeededState
	err := m.pluginAPI.KVGet(authNeededKey, &state)
	if err != nil {
		if mmapi.IsKVNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to retrieve OAuth-needed state from KV store: %w", err)
	}

	if state.AuthURL == "" {
		return nil, nil
	}

	return &state, nil
}

func (m *OAuthManager) StoreAuthNeededState(userID, serverID, authURL string) error {
	authNeededKey := buildAuthNeededKey(userID, serverID)
	state := &OAuthNeededState{
		AuthURL: authURL,
		SeenAt:  time.Now(),
	}

	if err := m.pluginAPI.KVSetWithExpiry(authNeededKey, state, oauthNeededStateTTL); err != nil {
		return fmt.Errorf("failed to store OAuth-needed state: %w", err)
	}

	return nil
}

func (m *OAuthManager) DeleteAuthNeededState(userID, serverID string) error {
	authNeededKey := buildAuthNeededKey(userID, serverID)
	if err := m.pluginAPI.KVDelete(authNeededKey); err != nil {
		return fmt.Errorf("failed to delete OAuth-needed state: %w", err)
	}
	return nil
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

// DeleteUserToken removes the stored OAuth token for a user and server,
// effectively disconnecting the user from that MCP server.
func (m *OAuthManager) DeleteUserToken(userID, serverID string) error {
	tokenErr := m.deleteToken(userID, serverID)
	authNeededErr := m.DeleteAuthNeededState(userID, serverID)
	return errors.Join(tokenErr, authNeededErr)
}

type ClientCredentials struct {
	ClientID     string    `json:"clientID"`
	ClientSecret string    `json:"clientSecret"`
	ServerURL    string    `json:"serverURL"`
	CreatedAt    time.Time `json:"createdAt"`
	// TokenEndpointAuthMethod is the RFC 7591 auth method returned by dynamic
	// client registration. "none" identifies a public client, whose empty
	// secret is valid. Empty means the pre-existing default
	// (client_secret_basic).
	TokenEndpointAuthMethod string `json:"tokenEndpointAuthMethod,omitempty"`
	// SecretExpiresAt is the RFC 7591 client_secret_expires_at (Unix seconds,
	// 0 = never expires). Expired credentials are treated as absent so a new
	// registration replaces them instead of failing token exchanges.
	SecretExpiresAt int64 `json:"secretExpiresAt,omitempty"`
}

// isPublicClient reports whether the registration produced a public client
// (RFC 7591 token_endpoint_auth_method "none"), which legitimately has no
// client secret.
func (c *ClientCredentials) isPublicClient() bool {
	return c.TokenEndpointAuthMethod == "none"
}

func (m *OAuthManager) loadClientCredentials(serverURL string) (*ClientCredentials, error) {
	credKey := buildClientCredentialsKey(serverURL)

	var creds ClientCredentials
	err := m.pluginAPI.KVGet(credKey, &creds)
	if err != nil {
		if mmapi.IsKVNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to retrieve client credentials from KV store: %w", err)
	}

	if creds.ClientID == "" || (creds.ClientSecret == "" && !creds.isPublicClient()) {
		// If no credentials are found, return nil to indicate no credentials
		// exist. Public clients (token_endpoint_auth_method "none")
		// legitimately have no secret.
		return nil, nil
	}

	if creds.SecretExpiresAt > 0 && time.Now().Unix() >= creds.SecretExpiresAt {
		// The registered secret expired; treat the credentials as absent so
		// the caller registers a fresh client instead of failing exchanges.
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
	StaticClientID    string    `json:"staticClientID,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`

	// Binding of the authorization request to the discovery outcome at
	// initiation time. ProcessCallback exchanges the code against these
	// persisted values instead of re-running discovery, so a server that
	// swaps its advertised authorization server metadata mid-flow cannot
	// redirect the code, verifier, or client secret to a different host.
	// Empty on sessions created before the fields existed (an in-flight
	// upgrade), in which case the callback falls back to re-discovery.
	Issuer        string   `json:"issuer,omitempty"`
	RequireIss    bool     `json:"requireIss,omitempty"` // AS advertises RFC 9207 support: iss must be present and match
	AuthServerURL string   `json:"authServerURL,omitempty"`
	TokenEndpoint string   `json:"tokenEndpoint,omitempty"`
	ResourceURL   string   `json:"resourceURL,omitempty"` // canonical RFC 8707 resource
	Scopes        []string `json:"scopes,omitempty"`
}

// Unlike the other loaders, a missing key surfaces as an error here:
// ProcessCallback nil-derefs the returned session unguarded.
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

const oauthSessionTTL = 10 * time.Minute

func (m *OAuthManager) storeSession(session *OAuthSession) error {
	sessionKey := buildSessionKey(session.UserID, session.State)

	if err := m.pluginAPI.KVSetWithExpiry(sessionKey, session, oauthSessionTTL); err != nil {
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
