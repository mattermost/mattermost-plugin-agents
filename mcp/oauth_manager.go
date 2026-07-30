// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

const (
	// oauthClientName is the human-readable RFC 7591 client_name shown on
	// authorization server consent screens.
	oauthClientName         = "Mattermost Agents"
	oauthCallbackPathSuffix = "/oauth/callback"

	// oauthConfigCacheTTL bounds how long a discovered OAuth configuration
	// (metadata endpoints + client credentials) is reused on the hot request
	// path before discovery runs again.
	oauthConfigCacheTTL = 5 * time.Minute
)

type OAuthNeededError struct {
	authURL     string
	metadataURL string
}

func (e *OAuthNeededError) Error() string {
	if e.authURL == "" {
		return "OAuth flow needed"
	}
	return fmt.Sprintf("OAuth flow needed, please visit: %s", e.authURL)
}
func (e *OAuthNeededError) AuthURL() string {
	return e.authURL
}

// MetadataURL returns the RFC 9728 resource_metadata URL from the upstream
// 401 challenge when known (may be empty).
func (e *OAuthNeededError) MetadataURL() string {
	return e.metadataURL
}

func (e *OAuthNeededError) Unwrap() error {
	return nil
}

// generateState generates a random state parameter for OAuth
func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ServerConfigLookup resolves a server's current configuration by its ID.
// It returns the config and true if found, or a zero value and false if not.
type ServerConfigLookup func(serverID string) (ServerConfig, bool)

type OAuthManager struct {
	pluginAPI          mmapi.Client
	callbackURL        string
	httpClient         *http.Client
	serverConfigLookup ServerConfigLookup

	// configCacheMu guards configCache, a small TTL cache of discovered OAuth
	// configurations used on the hot request path (see
	// createOAuthConfigCached).
	configCacheMu sync.Mutex
	configCache   map[string]oauthConfigCacheEntry
}

type oauthConfigCacheEntry struct {
	config    *oauth2.Config
	expiresAt time.Time
}

func NewOAuthManager(pluginAPI mmapi.Client, callbackURL string, httpClient *http.Client, serverConfigLookup ServerConfigLookup) *OAuthManager {
	return &OAuthManager{
		pluginAPI:          pluginAPI,
		callbackURL:        callbackURL,
		httpClient:         httpClient,
		serverConfigLookup: serverConfigLookup,
		configCache:        make(map[string]oauthConfigCacheEntry),
	}
}

// createOAuthConfigCached returns the OAuth configuration for a server,
// reusing a recently discovered one when available. Token sources ask for the
// configuration on every outgoing MCP request; without a cache each request
// would pay two or more metadata round trips (protected resource +
// authorization server metadata) even when the stored token is still valid.
// Flow initiation and callback processing deliberately keep using
// createOAuthConfig directly so explicit user actions always see fresh
// discovery results.
func (m *OAuthManager) createOAuthConfigCached(ctx context.Context, serverURL, metadataURL string, staticCreds *StaticOAuthCredentials) (*oauth2.Config, error) {
	key := serverURL + "|" + metadataURL + "|" + staticCredsClientID(staticCreds)

	m.configCacheMu.Lock()
	entry, ok := m.configCache[key]
	m.configCacheMu.Unlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.config, nil
	}

	config, err := m.createOAuthConfig(ctx, serverURL, metadataURL, staticCreds)
	if err != nil {
		return nil, err
	}

	m.configCacheMu.Lock()
	m.configCache[key] = oauthConfigCacheEntry{config: config, expiresAt: time.Now().Add(oauthConfigCacheTTL)}
	m.configCacheMu.Unlock()

	return config, nil
}

func (m *OAuthManager) StartURL(serverID string) string {
	baseURL := strings.TrimSuffix(m.callbackURL, oauthCallbackPathSuffix)
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return ""
	}

	return fmt.Sprintf("%s/mcp/oauth/%s/start", baseURL, url.PathEscape(serverID))
}

// StaticOAuthCredentials holds pre-configured OAuth client credentials from server config.
// When set, these bypass Dynamic Client Registration (RFC 7591) for providers that
// require a pre-registered OAuth application.
type StaticOAuthCredentials struct {
	ClientID     string
	ClientSecret string
}

// loadOrCreateClientCredentials gets existing client credentials or creates new ones using dynamic client registration.
// If staticCreds is non-nil and has a ClientID, those credentials are used directly (skipping DCR).
func (m *OAuthManager) loadOrCreateClientCredentials(ctx context.Context, serverURL string, staticCreds *StaticOAuthCredentials, registrationEndpoint string) (*ClientCredentials, error) {
	if staticCreds != nil && staticCreds.ClientID != "" {
		return &ClientCredentials{
			ClientID:     staticCreds.ClientID,
			ClientSecret: staticCreds.ClientSecret,
			ServerURL:    serverURL,
		}, nil
	}

	// Try to load existing credentials
	creds, err := m.loadClientCredentials(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to load client credentials: %w", err)
	}
	if creds != nil {
		// Loaded existing credentials
		return creds, nil
	}

	if registrationEndpoint == "" {
		// No registration endpoint was discovered upstream; discover it from
		// the authorization server metadata at the server's base URL.
		registrationEndpoint, err = m.discoverRegistrationEndpoint(ctx, serverURL)
		if err != nil {
			return nil, fmt.Errorf("failed to discover registration endpoint for server %s: %w", serverURL, err)
		}
	}

	// Register a new client via Dynamic Client Registration (RFC 7591).
	response, err := oauthex.RegisterClient(ctx, registrationEndpoint, &oauthex.ClientRegistrationMetadata{
		RedirectURIs:            []string{m.callbackURL},
		TokenEndpointAuthMethod: "client_secret_basic",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              oauthClientName,
	}, m.httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to register OAuth client with server %s (registration endpoint: %s): %w", serverURL, registrationEndpoint, err)
	}

	// Create new credentials from registration response
	newCreds := &ClientCredentials{
		ClientID:     response.ClientID,
		ClientSecret: response.ClientSecret,
		ServerURL:    serverURL,
		CreatedAt:    time.Now(),
	}

	// Store the new credentials
	if err := m.storeClientCredentials(newCreds); err != nil {
		return nil, fmt.Errorf("failed to store client credentials: %w", err)
	}

	m.pluginAPI.LogDebug("Successfully registered and stored new client credentials", "serverURL", serverURL, "clientID", response.ClientID)
	return newCreds, nil
}

func (m *OAuthManager) createOAuthConfig(ctx context.Context, serverURL, metadataURL string, staticCreds *StaticOAuthCredentials) (*oauth2.Config, error) {
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server URL: %w", err)
	}
	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	// Try to discover OAuth endpoints using RFC 8414/9728
	authURL := baseURL + "/authorize" // Fallback
	tokenURL := baseURL + "/token"    // Fallback
	authServerURL := baseURL          // Fallback - per MCP spec, auth server is at base URL (path stripped)
	registrationEndpoint := ""
	var scopes []string

	// Attempt discovery (best effort, fall back to hardcoded endpoints if it fails).
	// Pass serverURL (not baseURL) so the well-known URL preserves any path component
	// per RFC 9728 Section 3.1 (e.g. /base/path -> /.well-known/oauth-protected-resource/base/path).
	if protectedMetadata, discErr := m.fetchProtectedResourceMetadata(ctx, serverURL, metadataURL); discErr == nil {
		scopes = protectedMetadata.ScopesSupported
		// Use first authorization server (fetchProtectedResourceMetadata
		// guarantees at least one).
		authServerIssuer := protectedMetadata.AuthorizationServers[0]
		if authMetadata, authErr := m.fetchAuthorizationServerMetadata(ctx, authServerIssuer); authErr == nil {
			authURL = authMetadata.AuthorizationEndpoint
			tokenURL = authMetadata.TokenEndpoint
			// Per OAuth best practices, credentials are registered with the authorization server
			authServerURL = authServerIssuer
			registrationEndpoint = authMetadata.RegistrationEndpoint
		} else {
			// Discovery explicitly named an authorization server but its
			// metadata is unavailable: fall back to conventional endpoints on
			// that issuer rather than on the resource server, and register
			// credentials against the right host.
			authURL = strings.TrimSuffix(authServerIssuer, "/") + "/authorize"
			tokenURL = strings.TrimSuffix(authServerIssuer, "/") + "/token"
			authServerURL = authServerIssuer
		}
	} else {
		// If protected resource metadata fails, assume the resource server is the authorization server
		// and try the authorization server metadata endpoint directly (existing MCP server behavior).
		// Use baseURL (path stripped) per MCP spec: the authorization base URL is derived by
		// discarding the path component from the MCP server URL.
		if authMetadata, authErr := m.fetchAuthorizationServerMetadata(ctx, baseURL); authErr == nil {
			authURL = authMetadata.AuthorizationEndpoint
			tokenURL = authMetadata.TokenEndpoint
			registrationEndpoint = authMetadata.RegistrationEndpoint
			// authServerURL already set to baseURL above
		}
	}

	// Get client credentials for the authorization server (not the protected resource)
	// Per OAuth 2.0 best practices, client credentials are registered with and belong to
	// the authorization server, not the protected resource.
	// If static credentials are provided, they are used directly (skipping DCR).
	clientCreds, err := m.loadOrCreateClientCredentials(ctx, authServerURL, staticCreds, registrationEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get client credentials: %w", err)
	}

	return &oauth2.Config{
		ClientID:     clientCreds.ClientID,
		ClientSecret: clientCreds.ClientSecret,
		RedirectURL:  m.callbackURL,
		Scopes:       scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
	}, nil
}

func (m *OAuthManager) InitiateOAuthFlowForServer(ctx context.Context, userID string, serverConfig ServerConfig) (string, error) {
	return m.InitiateOAuthFlowForServerWithMetadata(ctx, userID, serverConfig, "")
}

// InitiateOAuthFlowForServerWithMetadata starts OAuth like InitiateOAuthFlowForServer but passes
// resource_metadata from the upstream 401 when present (RFC 9728).
func (m *OAuthManager) InitiateOAuthFlowForServerWithMetadata(ctx context.Context, userID string, serverConfig ServerConfig, metadataURL string) (string, error) {
	return m.InitiateOAuthFlow(ctx, userID, serverConfig.Name, serverConfig.BaseURL, metadataURL, staticOAuthCreds(serverConfig))
}

func (m *OAuthManager) InitiateOAuthFlow(ctx context.Context, userID, serverID, serverURL, metadataURL string, staticCreds *StaticOAuthCredentials) (string, error) {
	// Generate PKCE parameters
	codeVerifier := oauth2.GenerateVerifier()

	// Generate state parameter
	state, err := generateState()
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	// Get OAuth config
	oauthConfig, err := m.createOAuthConfig(ctx, serverURL, metadataURL, staticCreds)
	if err != nil {
		return "", fmt.Errorf("failed to create OAuth config: %w", err)
	}

	// Build authorization URL with PKCE
	authURL := oauthConfig.AuthCodeURL(state, oauth2.S256ChallengeOption(codeVerifier))

	// Store OAuth session. Only the StaticClientID is persisted so ProcessCallback
	// knows whether to look up static credentials; the secret itself is re-derived
	// from the live plugin config via serverConfigLookup at callback time.
	if err := m.storeSession(&OAuthSession{
		UserID:            userID,
		ServerID:          serverID,
		ServerURL:         serverURL,
		ServerMetadataURL: metadataURL,
		CodeVerifier:      codeVerifier,
		State:             state,
		StaticClientID:    staticCredsClientID(staticCreds),
		CreatedAt:         time.Now(),
	}); err != nil {
		return "", fmt.Errorf("failed to store OAuth session: %w", err)
	}

	return authURL, nil
}

func staticCredsClientID(creds *StaticOAuthCredentials) string {
	if creds == nil {
		return ""
	}
	return creds.ClientID
}

func (m *OAuthManager) ProcessCallback(ctx context.Context, loggedInUserID, state, code string) (*OAuthSession, error) {
	session, err := m.loadSession(loggedInUserID, state)
	if err != nil {
		return nil, fmt.Errorf("invalid or expired session: %w", err)
	}

	// Always clean up the session when we're done, whether we succeed or fail.
	// The session contains sensitive material (CodeVerifier) that should not
	// linger in the KV store.
	defer func() {
		if delErr := m.deleteSession(loggedInUserID, state); delErr != nil {
			m.pluginAPI.LogError("Failed to delete OAuth session after processing callback")
		}
	}()

	// Validate state
	if session.State == "" || session.State != state {
		return nil, fmt.Errorf("state mismatch")
	}

	// Validate userID
	if session.UserID != loggedInUserID {
		return nil, fmt.Errorf("user ID mismatch: expected %s, got %s", session.UserID, loggedInUserID)
	}

	// Re-derive static credentials from the live plugin config so the secret
	// never needs to be persisted in the KV store session.
	var staticCreds *StaticOAuthCredentials
	if session.StaticClientID != "" && m.serverConfigLookup != nil {
		if cfg, ok := m.serverConfigLookup(session.ServerID); ok {
			staticCreds = staticOAuthCreds(cfg)
		} else {
			m.pluginAPI.LogWarn("Static OAuth credentials were expected but server config not found; falling back to dynamic registration",
				"serverID", session.ServerID)
		}
	}

	// Get OAuth config
	oauthConfig, err := m.createOAuthConfig(ctx, session.ServerURL, session.ServerMetadataURL, staticCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth config: %w", err)
	}

	// Exchange code for token with PKCE
	ctxWithClient := context.WithValue(ctx, oauth2.HTTPClient, m.httpClient)
	token, err := oauthConfig.Exchange(ctxWithClient, code,
		oauth2.VerifierOption(session.CodeVerifier))
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code for token: %w", err)
	}

	// Store the token
	if err := m.storeToken(loggedInUserID, session.ServerID, token); err != nil {
		return nil, fmt.Errorf("failed to save token: %w", err)
	}
	if err := m.DeleteAuthNeededState(loggedInUserID, session.ServerID); err != nil {
		m.pluginAPI.LogWarn("Failed to clear OAuth-needed state after successful callback",
			"userID", loggedInUserID,
			"serverID", session.ServerID,
			"error", err)
	}

	return session, nil
}
