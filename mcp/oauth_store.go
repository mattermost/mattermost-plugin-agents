// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
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

// tokenEnvelopeVersion is the current storedTokenEnvelope version.
const tokenEnvelopeVersion = 1

// storedTokenEnvelope pins an OAuth grant to the authorization server it was
// issued by. Refreshes go to exactly the pinned token endpoint with exactly
// the pinned client — never through rediscovery — so a compromised MCP server
// that starts advertising a different authorization server can neither
// receive the refresh token nor the client secret.
//
// Version 0 (a bare oauth2.Token, written before the envelope existed) is
// still readable: the access token remains usable until it expires, but it
// cannot be refreshed (no pinned destination), forcing one re-authorization.
type storedTokenEnvelope struct {
	Version int           `json:"version"`
	Token   *oauth2.Token `json:"token"`
	// Issuer is the authorization server's issuer identifier.
	Issuer string `json:"issuer,omitempty"`
	// TokenEndpoint is the only endpoint refreshes may be sent to.
	TokenEndpoint string `json:"tokenEndpoint,omitempty"`
	// AuthServerURL keys the stored client credentials.
	AuthServerURL string `json:"authServerURL,omitempty"`
	// ClientID is the client the grant was issued to; refresh credentials
	// must match it.
	ClientID string `json:"clientID,omitempty"`
	// AuthMethod is the RFC 7591 token endpoint auth method ("none" for
	// public clients).
	AuthMethod string `json:"authMethod,omitempty"`
	// Resource is the canonical RFC 8707 resource the grant is bound to.
	Resource string `json:"resource,omitempty"`
	// RevocationEndpoint is the authorization server's RFC 7009 revocation
	// endpoint, when it advertised one. Disconnecting revokes the grant there
	// so access is actually cut off at the provider, not just locally. Empty
	// for grants stored before this field existed (revocation is skipped) or
	// when the AS advertises no revocation endpoint.
	RevocationEndpoint string `json:"revocationEndpoint,omitempty"`
}

// loadTokenEnvelope retrieves the stored OAuth grant for a user and server.
// It returns nil when no grant exists. The raw stored bytes are returned
// alongside so callers can perform exact compare-and-set/delete against the
// snapshot they observed (avoiding a re-marshal that might not reproduce the
// stored bytes). Pre-envelope grants (bare oauth2.Token values) are returned
// as a Version 0 envelope. The key is read exactly once so a concurrent write
// between reads cannot be misclassified.
func (m *OAuthManager) loadTokenEnvelope(userID, serverID string) (*storedTokenEnvelope, []byte, error) {
	tokenKey := buildTokenKey(userID, serverID)

	var raw []byte
	if err := m.pluginAPI.KVGet(tokenKey, &raw); err != nil {
		if mmapi.IsKVNotFound(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to retrieve token from KV store: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil, nil
	}

	// Decode the single snapshot as a versioned envelope first.
	var envelope storedTokenEnvelope
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Version > 0 {
		if envelope.Version != tokenEnvelopeVersion {
			// Fail closed for unknown versions (a newer node's v2 record):
			// treating it as v1 would silently ignore binding fields it
			// requires. NOTE for whoever introduces v2: during a rolling
			// upgrade, nodes still on v1 will error here for every grant a
			// v2 node has rewritten — plan the migration so v2 records are
			// only written once the whole cluster can read them (or make v2
			// additive and bump the version lazily).
			return nil, raw, fmt.Errorf("unsupported token envelope version %d (expected %d)", envelope.Version, tokenEnvelopeVersion)
		}
		if envelope.Token == nil || envelope.Token.AccessToken == "" {
			return nil, raw, nil
		}
		return &envelope, raw, nil
	}

	// Legacy layout: the same snapshot holds a bare oauth2.Token.
	var legacy oauth2.Token
	if err := json.Unmarshal(raw, &legacy); err != nil || legacy.AccessToken == "" {
		return nil, raw, nil
	}
	return &storedTokenEnvelope{Version: 0, Token: &legacy}, raw, nil
}

// loadToken retrieves the OAuth token for a user and server from the KV store
// If no token is found, it returns nil to indicate no token exists
func (m *OAuthManager) loadToken(userID, serverID string) (*oauth2.Token, error) {
	envelope, _, err := m.loadTokenEnvelope(userID, serverID)
	if err != nil {
		return nil, err
	}
	if envelope == nil {
		return nil, nil
	}
	return envelope.Token, nil
}

// storeTokenEnvelope persists an OAuth grant with its authorization server
// binding.
func (m *OAuthManager) storeTokenEnvelope(userID, serverID string, envelope *storedTokenEnvelope) error {
	if err := m.pluginAPI.KVSet(buildTokenKey(userID, serverID), envelope); err != nil {
		return fmt.Errorf("failed to store token in KV store: %w", err)
	}
	return nil
}

// casTokenEnvelope atomically replaces the stored grant only if the current
// bytes still equal oldRaw (the exact snapshot the caller observed). Returns
// false when another writer won.
func (m *OAuthManager) casTokenEnvelope(userID, serverID string, oldRaw []byte, updated *storedTokenEnvelope) (bool, error) {
	newRaw, err := json.Marshal(updated)
	if err != nil {
		return false, fmt.Errorf("failed to marshal token envelope: %w", err)
	}
	return m.pluginAPI.KVCompareAndSet(buildTokenKey(userID, serverID), oldRaw, newRaw)
}

// casDeleteTokenEnvelope atomically deletes the stored grant only if the
// current bytes still equal oldRaw. Returns false when another writer won.
func (m *OAuthManager) casDeleteTokenEnvelope(userID, serverID string, oldRaw []byte) (bool, error) {
	return m.pluginAPI.KVCompareAndSet(buildTokenKey(userID, serverID), oldRaw, nil)
}

const (
	// refreshLeaseTTL bounds how long a per-grant refresh lease is held; it
	// exceeds oauthPrepTimeout so a node actively refreshing keeps the lease
	// for the whole attempt, while a crashed holder's lease auto-expires.
	refreshLeaseTTL = 35 * time.Second
	// refreshLeaseWait caps how long a node blocks waiting for another node's
	// in-flight refresh. TokenSource runs under the SDK's connection-wide
	// send lock, so this is deliberately short.
	refreshLeaseWait = 2 * time.Second
)

func buildRefreshLeaseKey(userID, serverID string) string {
	return fmt.Sprintf("mcp_oauth_refresh_lease_v1_%s_%s", userID, serverID)
}

// acquireRefreshLease attempts to take the per-grant refresh lease. It
// succeeds only when no unexpired lease exists (atomic create with TTL).
func (m *OAuthManager) acquireRefreshLease(userID, serverID, leaseID string) (bool, error) {
	return m.pluginAPI.KVCompareAndSetWithExpiry(buildRefreshLeaseKey(userID, serverID), nil, leaseID, refreshLeaseTTL)
}

// releaseRefreshLease drops the lease if we still hold it.
func (m *OAuthManager) releaseRefreshLease(userID, serverID, leaseID string) {
	if _, err := m.pluginAPI.KVCompareAndSet(buildRefreshLeaseKey(userID, serverID), leaseID, nil); err != nil {
		m.pluginAPI.LogWarn("Failed to release MCP OAuth refresh lease", "userID", userID, "serverID", serverID, "error", err)
	}
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

func (m *OAuthManager) deleteToken(userID, serverID string) error {
	tokenKey := buildTokenKey(userID, serverID)
	if err := m.pluginAPI.KVDelete(tokenKey); err != nil {
		return fmt.Errorf("failed to delete token from KV store: %w", err)
	}
	return nil
}

// DeleteUserToken removes the stored OAuth token for a user and server,
// effectively disconnecting the user from that MCP server. It first makes a
// best-effort RFC 7009 revocation at the authorization server so the grant is
// actually invalidated at the provider (not just in our KV store); revocation
// failures never block the local delete.
func (m *OAuthManager) DeleteUserToken(ctx context.Context, userID, serverID string) error {
	m.revokeGrantBeforeDelete(ctx, userID, serverID)

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
	// ClientID and AuthMethod pin the exact client registration the
	// authorization request was made with; the callback refuses to exchange
	// with any other client.
	ClientID   string `json:"clientID,omitempty"`
	AuthMethod string `json:"authMethod,omitempty"`
	// RevocationEndpoint is the AS's RFC 7009 revocation endpoint, carried
	// into the stored grant so disconnect can revoke at the provider.
	RevocationEndpoint string `json:"revocationEndpoint,omitempty"`
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
