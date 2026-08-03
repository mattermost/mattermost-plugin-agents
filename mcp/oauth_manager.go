// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

const (
	// oauthClientName is the human-readable RFC 7591 client_name shown on
	// authorization server consent screens. "Mattermost Agents" is a brand
	// name and is deliberately not translated.
	oauthClientName         = "Mattermost Agents"
	oauthCallbackPathSuffix = "/oauth/callback"
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
}

func NewOAuthManager(pluginAPI mmapi.Client, callbackURL string, httpClient *http.Client, serverConfigLookup ServerConfigLookup) *OAuthManager {
	return &OAuthManager{
		pluginAPI:          pluginAPI,
		callbackURL:        callbackURL,
		httpClient:         httpClient,
		serverConfigLookup: serverConfigLookup,
	}
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
		// The selected authorization server's own metadata advertised no
		// registration endpoint. We deliberately do NOT fall back to
		// stripping the issuer's path and rediscovering at the root: that
		// could register the client against a different (root) authorization
		// server and then hand those credentials to this path-scoped one.
		// Fail closed instead.
		return nil, fmt.Errorf("authorization server %s does not advertise a dynamic client registration endpoint", serverURL)
	}

	// Register a new client via Dynamic Client Registration (RFC 7591). The
	// body-limited client caps the response size the SDK would otherwise read
	// unbounded, guarding against a hostile registration endpoint.
	// application_type is required by the MCP 2026-07-28 spec and inferred
	// from the callback URI (loopback => native, otherwise web), matching the
	// SDK's higher-level handler; strict OIDC servers reject a mismatch.
	response, err := oauthex.RegisterClient(ctx, registrationEndpoint, &oauthex.ClientRegistrationMetadata{
		RedirectURIs:            []string{m.callbackURL},
		TokenEndpointAuthMethod: "client_secret_basic",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              oauthClientName,
		ApplicationType:         inferApplicationType(m.callbackURL),
	}, m.bodyLimitedHTTPClient())
	if err != nil {
		return nil, fmt.Errorf("failed to register OAuth client with server %s (registration endpoint: %s): %w", serverURL, registrationEndpoint, err)
	}

	// Create new credentials from registration response, preserving the
	// authentication method (public clients register with
	// token_endpoint_auth_method "none" and no secret) and secret expiry.
	newCreds := &ClientCredentials{
		ClientID:                response.ClientID,
		ClientSecret:            response.ClientSecret,
		ServerURL:               serverURL,
		CreatedAt:               time.Now(),
		TokenEndpointAuthMethod: response.TokenEndpointAuthMethod,
	}
	if !response.ClientSecretExpiresAt.IsZero() {
		newCreds.SecretExpiresAt = response.ClientSecretExpiresAt.Unix()
	}

	// Store the new credentials
	if err := m.storeClientCredentials(newCreds); err != nil {
		return nil, fmt.Errorf("failed to store client credentials: %w", err)
	}

	m.pluginAPI.LogDebug("Successfully registered and stored new client credentials", "serverURL", serverURL, "clientID", response.ClientID)
	return newCreds, nil
}

// resolvedOAuthConfig carries the oauth2 configuration together with the
// discovery outcome it was derived from, so flows can bind sessions to the
// issuer/endpoints that were actually selected (see OAuthSession) and send
// the canonical RFC 8707 resource with authorization requests.
type resolvedOAuthConfig struct {
	config *oauth2.Config
	// issuer is the authorization server's issuer identifier from its
	// metadata (falls back to the URL discovery derived it from), used for
	// RFC 9207 iss verification.
	issuer string
	// requireIss reports whether the AS metadata advertised RFC 9207 support
	// (authorization_response_iss_parameter_supported), in which case the
	// callback must carry a matching iss parameter.
	requireIss bool
	// authServerURL is the URL client credentials are registered under.
	authServerURL string
	// resource is the canonical RFC 8707 resource identifier of the MCP
	// server (the PRM resource value when discovery produced one).
	resource string
	// creds are the client credentials the config was assembled from.
	creds *ClientCredentials
}

func (m *OAuthManager) createOAuthConfig(ctx context.Context, serverURL, metadataURL string, staticCreds *StaticOAuthCredentials) (*oauth2.Config, error) {
	resolved, err := m.resolveOAuthConfig(ctx, serverURL, metadataURL, staticCreds)
	if err != nil {
		return nil, err
	}
	return resolved.config, nil
}

func (m *OAuthManager) resolveOAuthConfig(ctx context.Context, serverURL, metadataURL string, staticCreds *StaticOAuthCredentials) (*resolvedOAuthConfig, error) {
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server URL: %w", err)
	}
	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	// Try to discover OAuth endpoints using RFC 8414/9728
	authURL := baseURL + "/authorize" // Fallback
	tokenURL := baseURL + "/token"    // Fallback
	authServerURL := baseURL          // Fallback - per MCP spec, auth server is at base URL (path stripped)
	issuer := ""
	requireIss := false
	resource := serverURL // Fallback: canonical resource is the MCP server URL
	registrationEndpoint := ""
	var scopes []string

	// Attempt discovery (best effort, fall back to hardcoded endpoints if it fails).
	// Pass serverURL (not baseURL) so the well-known URL preserves any path component
	// per RFC 9728 Section 3.1 (e.g. /base/path -> /.well-known/oauth-protected-resource/base/path).
	if protectedMetadata, discErr := m.fetchProtectedResourceMetadata(ctx, serverURL, metadataURL); discErr == nil {
		scopes = protectedMetadata.ScopesSupported
		resource = protectedMetadata.Resource
		// Use first authorization server (fetchProtectedResourceMetadata
		// guarantees at least one).
		authServerIssuer := protectedMetadata.AuthorizationServers[0]
		if authMetadata, authErr := m.fetchAuthorizationServerMetadata(ctx, authServerIssuer); authErr == nil {
			authURL = authMetadata.AuthorizationEndpoint
			tokenURL = authMetadata.TokenEndpoint
			// Per OAuth best practices, credentials are registered with the authorization server
			authServerURL = authServerIssuer
			issuer = authMetadata.Issuer
			requireIss = authMetadata.AuthorizationResponseIssParameterSupported
			registrationEndpoint = authMetadata.RegistrationEndpoint
		} else {
			// Discovery explicitly named an authorization server but its
			// metadata is unavailable: fall back to conventional endpoints on
			// that issuer rather than on the resource server, and register
			// credentials against the right host.
			authURL = strings.TrimSuffix(authServerIssuer, "/") + "/authorize"
			tokenURL = strings.TrimSuffix(authServerIssuer, "/") + "/token"
			authServerURL = authServerIssuer
			issuer = authServerIssuer
		}
	} else {
		// If protected resource metadata fails, assume the resource server is the authorization server
		// and try the authorization server metadata endpoint directly (existing MCP server behavior).
		// Use baseURL (path stripped) per MCP spec: the authorization base URL is derived by
		// discarding the path component from the MCP server URL.
		if authMetadata, authErr := m.fetchAuthorizationServerMetadata(ctx, baseURL); authErr == nil {
			authURL = authMetadata.AuthorizationEndpoint
			tokenURL = authMetadata.TokenEndpoint
			issuer = authMetadata.Issuer
			requireIss = authMetadata.AuthorizationResponseIssParameterSupported
			registrationEndpoint = authMetadata.RegistrationEndpoint
			// authServerURL already set to baseURL above
		}
	}
	if issuer == "" {
		issuer = authServerURL
	}

	// Get client credentials for the authorization server (not the protected resource)
	// Per OAuth 2.0 best practices, client credentials are registered with and belong to
	// the authorization server, not the protected resource.
	// If static credentials are provided, they are used directly (skipping DCR).
	clientCreds, err := m.loadOrCreateClientCredentials(ctx, authServerURL, staticCreds, registrationEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get client credentials: %w", err)
	}

	return &resolvedOAuthConfig{
		config:        m.oauthConfigFromCredentials(clientCreds, authURL, tokenURL, scopes),
		issuer:        issuer,
		requireIss:    requireIss,
		authServerURL: authServerURL,
		resource:      resource,
		creds:         clientCreds,
	}, nil
}

// oauthConfigFromCredentials assembles the oauth2.Config for a set of client
// credentials. Public clients (empty secret) authenticate by sending only
// client_id in the request body.
func (m *OAuthManager) oauthConfigFromCredentials(clientCreds *ClientCredentials, authURL, tokenURL string, scopes []string) *oauth2.Config {
	endpoint := oauth2.Endpoint{
		AuthURL:  authURL,
		TokenURL: tokenURL,
	}
	if clientCreds.ClientSecret == "" {
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	return &oauth2.Config{
		ClientID:     clientCreds.ClientID,
		ClientSecret: clientCreds.ClientSecret,
		RedirectURL:  m.callbackURL,
		Scopes:       scopes,
		Endpoint:     endpoint,
	}
}

func (m *OAuthManager) InitiateOAuthFlowForServer(ctx context.Context, userID string, serverConfig ServerConfig) (string, error) {
	return m.InitiateOAuthFlowForServerWithMetadata(ctx, userID, serverConfig, "", "")
}

// InitiateOAuthFlowForServerWithMetadata starts OAuth like InitiateOAuthFlowForServer but passes
// resource_metadata from the upstream 401 when present (RFC 9728) and the
// space-separated scope from an insufficient_scope challenge when present
// (treated as authoritative per the MCP specification).
func (m *OAuthManager) InitiateOAuthFlowForServerWithMetadata(ctx context.Context, userID string, serverConfig ServerConfig, metadataURL, challengeScope string) (string, error) {
	return m.InitiateOAuthFlow(ctx, userID, serverConfig.Name, serverConfig.BaseURL, metadataURL, challengeScope, staticOAuthCreds(serverConfig))
}

func (m *OAuthManager) InitiateOAuthFlow(ctx context.Context, userID, serverID, serverURL, metadataURL, challengeScope string, staticCreds *StaticOAuthCredentials) (string, error) {
	// Generate PKCE parameters
	codeVerifier := oauth2.GenerateVerifier()

	// Generate state parameter
	state, err := generateState()
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	// Get OAuth config together with the discovery outcome it is based on.
	resolved, err := m.resolveOAuthConfig(ctx, serverURL, metadataURL, staticCreds)
	if err != nil {
		return "", fmt.Errorf("failed to create OAuth config: %w", err)
	}

	// Scopes from a WWW-Authenticate challenge are authoritative per the MCP
	// specification: they override the metadata-advertised default.
	if challengeScope != "" {
		resolved.config.Scopes = strings.Fields(challengeScope)
	}

	// Static credentials are pinned to the first issuer they were used with
	// (trust on first use): a compromised MCP server that later advertises a
	// different authorization server must not receive the static secret.
	if err := m.checkStaticCredsIssuerPin(serverID, serverURL, staticCreds, resolved.issuer); err != nil {
		return "", err
	}

	// Build authorization URL with PKCE and the canonical RFC 8707 resource
	// so the authorization server audience-restricts the issued token to this
	// MCP server.
	authURL := resolved.config.AuthCodeURL(state,
		oauth2.S256ChallengeOption(codeVerifier),
		oauth2.SetAuthURLParam("resource", resolved.resource),
	)

	// Store OAuth session, bound to the discovery outcome so the callback
	// exchanges the code against exactly these endpoints (see OAuthSession).
	// Only the StaticClientID is persisted so ProcessCallback knows whether
	// to look up static credentials; the secret itself is re-derived from the
	// live plugin config via serverConfigLookup at callback time.
	if err := m.storeSession(&OAuthSession{
		UserID:            userID,
		ServerID:          serverID,
		ServerURL:         serverURL,
		ServerMetadataURL: metadataURL,
		CodeVerifier:      codeVerifier,
		State:             state,
		StaticClientID:    staticCredsClientID(staticCreds),
		CreatedAt:         time.Now(),
		Issuer:            resolved.issuer,
		RequireIss:        resolved.requireIss,
		AuthServerURL:     resolved.authServerURL,
		TokenEndpoint:     resolved.config.Endpoint.TokenURL,
		ResourceURL:       resolved.resource,
		Scopes:            resolved.config.Scopes,
		ClientID:          resolved.creds.ClientID,
		AuthMethod:        resolved.creds.TokenEndpointAuthMethod,
	}); err != nil {
		return "", fmt.Errorf("failed to store OAuth session: %w", err)
	}

	return authURL, nil
}

// staticIssuerPin records the first issuer a static client credential was
// used with.
type staticIssuerPin struct {
	Issuer string `json:"issuer"`
}

// checkStaticCredsIssuerPin enforces trust-on-first-use issuer pinning for
// statically configured client credentials. The pin key incorporates the
// server name, client ID, and base URL, so intentionally reconfiguring the
// server naturally re-pins. Dynamically registered credentials need no pin:
// each authorization server registers (and receives) its own client.
func (m *OAuthManager) checkStaticCredsIssuerPin(serverID, serverURL string, staticCreds *StaticOAuthCredentials, issuer string) error {
	if staticCreds == nil || staticCreds.ClientID == "" {
		return nil
	}

	sum := sha256.Sum256([]byte(serverID + "|" + staticCreds.ClientID + "|" + serverURL))
	pinKey := "mcp_oauth_issuer_pin_v1_" + hex.EncodeToString(sum[:16])

	var pin staticIssuerPin
	err := m.pluginAPI.KVGet(pinKey, &pin)
	if err != nil && !mmapi.IsKVNotFound(err) {
		return fmt.Errorf("failed to load issuer pin: %w", err)
	}
	if pin.Issuer == "" {
		// First use: pin the issuer. A lost race against a concurrent
		// initiation pins whichever equal-or-different issuer won; enforce it
		// on the next flow rather than failing this one.
		if _, casErr := m.pluginAPI.KVCompareAndSet(pinKey, nil, &staticIssuerPin{Issuer: issuer}); casErr != nil {
			return fmt.Errorf("failed to store issuer pin: %w", casErr)
		}
		return nil
	}
	if pin.Issuer != issuer {
		return fmt.Errorf("authorization server issuer %q does not match the issuer %q this server's static OAuth credentials were first used with; if this change is intentional, update the MCP server configuration (name, base URL, or client ID) to re-pin", issuer, pin.Issuer)
	}
	return nil
}

// callbackOAuthConfig builds the oauth2 configuration for the code exchange,
// strictly from the session's persisted binding: the persisted token endpoint
// and exactly the client registration the authorization request was made with
// (load-only — never registering, rediscovering, or adopting a different
// client, all of which would either break the exchange or hand material to a
// server that changed its metadata mid-flow).
func (m *OAuthManager) callbackOAuthConfig(session *OAuthSession, staticCreds *StaticOAuthCredentials) (*oauth2.Config, *ClientCredentials, error) {
	var clientCreds *ClientCredentials
	switch {
	case staticCreds != nil && staticCreds.ClientID != "":
		if session.ClientID != "" && staticCreds.ClientID != session.ClientID {
			return nil, nil, fmt.Errorf("configured client ID no longer matches the client this authorization was started with; restart authorization")
		}
		clientCreds = &ClientCredentials{
			ClientID:     staticCreds.ClientID,
			ClientSecret: staticCreds.ClientSecret,
		}
	default:
		stored, err := m.loadClientCredentials(session.AuthServerURL)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load client credentials: %w", err)
		}
		if stored == nil || (session.ClientID != "" && stored.ClientID != session.ClientID) {
			return nil, nil, fmt.Errorf("the client registration this authorization was started with is no longer available; restart authorization")
		}
		clientCreds = stored
	}

	// The authorization endpoint is not used during the exchange; only the
	// token endpoint matters here.
	return m.oauthConfigFromCredentials(clientCreds, "", session.TokenEndpoint, session.Scopes), clientCreds, nil
}

func staticCredsClientID(creds *StaticOAuthCredentials) string {
	if creds == nil {
		return ""
	}
	return creds.ClientID
}

// ProcessCallback finishes the authorization flow: it validates state, user,
// and (RFC 9207) issuer, exchanges the code against the token endpoint the
// session was bound to at initiation time, and persists the token. iss is the
// issuer identifier from the authorization response's "iss" query parameter
// (may be empty when the server does not send one).
func (m *OAuthManager) ProcessCallback(ctx context.Context, loggedInUserID, state, code, iss string) (*OAuthSession, error) {
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

	// Sessions created before the binding fields existed cannot be verified
	// against the discovery outcome they were initiated with; re-running
	// discovery here would let a server that swapped its metadata mid-flow
	// receive the code, verifier, and client secret. Fail closed: the user
	// restarts authorization (sessions live only a few minutes, so this can
	// only occur for flows in flight across a plugin upgrade).
	if session.TokenEndpoint == "" || session.ClientID == "" {
		return nil, fmt.Errorf("authorization session predates security binding and cannot be completed; please restart authorization")
	}

	// RFC 9207 issuer verification: when the authorization response carries
	// an iss parameter it must match the issuer the session was bound to
	// exactly (the MCP specification forbids normalization during this
	// comparison); and when the AS metadata advertised iss support at
	// initiation time, the parameter is mandatory (its absence indicates a
	// mix-up attack).
	if iss != "" && iss != session.Issuer {
		return nil, fmt.Errorf("issuer mismatch in authorization response: got %q, expected %q", iss, session.Issuer)
	}
	if iss == "" && session.RequireIss {
		return nil, fmt.Errorf("authorization response is missing the iss parameter required by the authorization server's metadata")
	}

	// Re-derive static credentials from the live plugin config so the secret
	// never needs to be persisted in the KV store session.
	var staticCreds *StaticOAuthCredentials
	if session.StaticClientID != "" && m.serverConfigLookup != nil {
		if cfg, ok := m.serverConfigLookup(session.ServerID); ok {
			staticCreds = staticOAuthCreds(cfg)
		} else {
			m.pluginAPI.LogWarn("Static OAuth credentials were expected but server config not found",
				"serverID", session.ServerID)
		}
	}

	// Exchange the code against exactly the endpoints and client the session
	// was bound to at initiation time.
	oauthConfig, clientCreds, err := m.callbackOAuthConfig(session, staticCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth config: %w", err)
	}

	// Exchange code for token with PKCE and the canonical RFC 8707 resource
	// bound at initiation time.
	exchangeOpts := []oauth2.AuthCodeOption{oauth2.VerifierOption(session.CodeVerifier)}
	if session.ResourceURL != "" {
		exchangeOpts = append(exchangeOpts, oauth2.SetAuthURLParam("resource", session.ResourceURL))
	}
	ctxWithClient := context.WithValue(ctx, oauth2.HTTPClient, m.httpClient)
	token, err := oauthConfig.Exchange(ctxWithClient, code, exchangeOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code for token: %w", err)
	}

	// Store the grant pinned to the authorization server and client it was
	// issued by; refreshes go exactly there, never through rediscovery.
	if err := m.storeTokenEnvelope(loggedInUserID, session.ServerID, &storedTokenEnvelope{
		Version:       tokenEnvelopeVersion,
		Token:         token,
		Issuer:        session.Issuer,
		TokenEndpoint: session.TokenEndpoint,
		AuthServerURL: session.AuthServerURL,
		ClientID:      clientCreds.ClientID,
		AuthMethod:    clientCreds.TokenEndpointAuthMethod,
		Resource:      session.ResourceURL,
	}); err != nil {
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
