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
	"time"

	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"golang.org/x/oauth2"
)

const (
	clientID = "mattermost-mcp-client"
)

type OAuthNeededError struct {
	authURL string
}

func (e *OAuthNeededError) Error() string {
	return fmt.Sprintf("OAuth flow needed, please visit: %s", e.authURL)
}
func (e *OAuthNeededError) AuthURL() string {
	return e.authURL
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

type OAuthManager struct {
	pluginAPI   mmapi.Client
	callbackURL string
	httpClient  *http.Client
}

func NewOAuthManager(pluginAPI mmapi.Client, callbackURL string, httpClient *http.Client) *OAuthManager {
	return &OAuthManager{
		pluginAPI:   pluginAPI,
		callbackURL: callbackURL,
		httpClient:  httpClient,
	}
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
func (m *OAuthManager) loadOrCreateClientCredentials(ctx context.Context, serverURL string, staticCreds *StaticOAuthCredentials) (*ClientCredentials, error) {
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

	// Perform complete client registration flow
	response, err := DiscoverAndRegisterClient(ctx, m.httpClient, serverURL, m.callbackURL, clientID, "")
	if err != nil {
		return nil, err
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
	authServerURL := serverURL        // Fallback - use protected resource as auth server
	var scopes []string

	// Attempt discovery (best effort, fall back to hardcoded endpoints if it fails).
	// Pass serverURL (not baseURL) so the well-known URL preserves any path component
	// per RFC 9728 Section 3.1 (e.g. /base/path -> /.well-known/oauth-protected-resource/base/path).
	if protectedMetadata, discErr := discoverProtectedResourceMetadata(ctx, m.httpClient, serverURL, metadataURL); discErr == nil {
		scopes = protectedMetadata.ScopesSupported
		if len(protectedMetadata.AuthorizationServers) > 0 {
			// Use first authorization server
			authServerIssuer := protectedMetadata.AuthorizationServers[0]
			if authMetadata, authErr := discoverAuthorizationServerMetadata(ctx, m.httpClient, authServerIssuer); authErr == nil {
				authURL = authMetadata.AuthorizationEndpoint
				tokenURL = authMetadata.TokenEndpoint
				// Per OAuth best practices, credentials are registered with the authorization server
				authServerURL = authServerIssuer
			}
		}
	} else {
		// If protected resource metadata fails, assume the resource server is the authorization server
		// and try the authorization server metadata endpoint directly (existing MCP server behavior).
		// Use serverURL (not baseURL) so path-scoped issuers are preserved per RFC 8414 Section 3.1.
		if authMetadata, authErr := discoverAuthorizationServerMetadata(ctx, m.httpClient, serverURL); authErr == nil {
			authURL = authMetadata.AuthorizationEndpoint
			tokenURL = authMetadata.TokenEndpoint
			// authServerURL already set to serverURL above
		}
	}

	// Get client credentials for the authorization server (not the protected resource)
	// Per OAuth 2.0 best practices, client credentials are registered with and belong to
	// the authorization server, not the protected resource.
	// If static credentials are provided, they are used directly (skipping DCR).
	clientCreds, err := m.loadOrCreateClientCredentials(ctx, authServerURL, staticCreds)
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

	// Store OAuth session (including static credentials so ProcessCallback can reuse them)
	if err := m.storeSession(&OAuthSession{
		UserID:             userID,
		ServerID:           serverID,
		ServerURL:          serverURL,
		ServerMetadataURL:  metadataURL,
		CodeVerifier:       codeVerifier,
		State:              state,
		StaticClientID:     staticCredsClientID(staticCreds),
		StaticClientSecret: staticCredsClientSecret(staticCreds),
		CreatedAt:          time.Now(),
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

func staticCredsClientSecret(creds *StaticOAuthCredentials) string {
	if creds == nil {
		return ""
	}
	return creds.ClientSecret
}

func (m *OAuthManager) ProcessCallback(ctx context.Context, loggedInUserID, state, code string) (*OAuthSession, error) {
	session, err := m.loadSession(loggedInUserID, state)
	if err != nil {
		return nil, fmt.Errorf("invalid or expired session: %w", err)
	}

	// Validate state
	if session.State == "" || session.State != state {
		return nil, fmt.Errorf("state mismatch")
	}

	// Validate userID
	if session.UserID != loggedInUserID {
		return nil, fmt.Errorf("user ID mismatch: expected %s, got %s", session.UserID, loggedInUserID)
	}

	// Reconstruct static credentials from the stored session
	var staticCreds *StaticOAuthCredentials
	if session.StaticClientID != "" {
		staticCreds = &StaticOAuthCredentials{
			ClientID:     session.StaticClientID,
			ClientSecret: session.StaticClientSecret,
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

	// Clean up session
	if err := m.deleteSession(loggedInUserID, state); err != nil {
		m.pluginAPI.LogError("Failed to delete OAuth session after processing callback")
	}

	return session, nil
}
