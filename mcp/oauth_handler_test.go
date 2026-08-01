// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// TestUserOAuthHandlerAuthorize covers WWW-Authenticate parsing through the
// handler, including edge cases ported from the previous hand-rolled parser
// tests. Authorize must never return nil, and must always return a
// *mcpUnauthorized so callers can convert it into an *OAuthNeededError.
func TestUserOAuthHandlerAuthorize(t *testing.T) {
	const metadataURL = "https://resource.example.com/.well-known/oauth-protected-resource"

	tests := []struct {
		name            string
		wwwAuthenticate []string
		wantMetadataURL string
	}{
		{
			name:            "valid bearer with resource_metadata",
			wwwAuthenticate: []string{`Bearer resource_metadata="` + metadataURL + `"`},
			wantMetadataURL: metadataURL,
		},
		{
			name:            "valid bearer with resource_metadata and other parameters",
			wwwAuthenticate: []string{`Bearer realm="protected", resource_metadata="` + metadataURL + `", max_age=3600`},
			wantMetadataURL: metadataURL,
		},
		{
			name:            "different scheme (DPoP)",
			wwwAuthenticate: []string{`DPoP resource_metadata="` + metadataURL + `"`},
			wantMetadataURL: metadataURL,
		},
		{
			name:            "multiple challenges with resource_metadata in second",
			wwwAuthenticate: []string{`Basic realm="simple", Bearer resource_metadata="` + metadataURL + `"`},
			wantMetadataURL: metadataURL,
		},
		{
			name:            "quoted comma inside another parameter",
			wwwAuthenticate: []string{`Bearer realm="a,b", resource_metadata="` + metadataURL + `"`},
			wantMetadataURL: metadataURL,
		},
		{
			name:            "multiple header values with resource_metadata in second",
			wwwAuthenticate: []string{`Basic realm="simple"`, `Bearer resource_metadata="` + metadataURL + `"`},
			wantMetadataURL: metadataURL,
		},
		{
			name:            "case insensitive scheme",
			wwwAuthenticate: []string{`bearer resource_metadata="` + metadataURL + `"`},
			wantMetadataURL: metadataURL,
		},
		{
			name:            "case insensitive parameter name",
			wwwAuthenticate: []string{`Bearer Resource_Metadata="` + metadataURL + `"`},
			wantMetadataURL: metadataURL,
		},
		{
			// RFC 9110 auth-params allow the token form, which the previous
			// hand-rolled parser rejected; the SDK parser accepts it.
			name:            "unquoted token value",
			wwwAuthenticate: []string{`Bearer resource_metadata=` + metadataURL},
			wantMetadataURL: metadataURL,
		},
		{
			name:            "resource metadata with query parameters",
			wwwAuthenticate: []string{`Bearer resource_metadata="` + metadataURL + `?version=1.0"`},
			wantMetadataURL: metadataURL + "?version=1.0",
		},
		{
			name:            "missing resource_metadata parameter",
			wwwAuthenticate: []string{`Bearer realm="protected"`},
			wantMetadataURL: "",
		},
		{
			name:            "no header",
			wwwAuthenticate: nil,
			wantMetadataURL: "",
		},
		{
			name:            "unterminated quoted string",
			wwwAuthenticate: []string{`Bearer resource_metadata="` + metadataURL},
			wantMetadataURL: "",
		},
		{
			name:            "empty resource_metadata value",
			wwwAuthenticate: []string{`Bearer resource_metadata=""`},
			wantMetadataURL: "",
		},
		{
			name:            "invalid URL - no scheme",
			wwwAuthenticate: []string{`Bearer resource_metadata="resource.example.com/.well-known/oauth-protected-resource"`},
			wantMetadataURL: "",
		},
		{
			name:            "invalid URL - no host",
			wwwAuthenticate: []string{`Bearer resource_metadata="https:///.well-known/oauth-protected-resource"`},
			wantMetadataURL: "",
		},
		{
			name:            "URL too long",
			wwwAuthenticate: []string{`Bearer resource_metadata="https://resource.example.com/` + strings.Repeat("a", 2100) + `"`},
			wantMetadataURL: "",
		},
		{
			// Rejected before parsing: bounds the quadratic cost of the SDK's
			// comma splitting on attacker-controlled headers.
			name:            "oversized header rejected before parsing",
			wwwAuthenticate: []string{"Bearer " + strings.Repeat(",", maxWWWAuthenticateBytes+1)},
			wantMetadataURL: "",
		},
	}

	handler := &userOAuthHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("unauthorized")),
			}
			for _, headerValue := range tt.wwwAuthenticate {
				resp.Header.Add("WWW-Authenticate", headerValue)
			}

			err := handler.Authorize(context.Background(), &http.Request{}, resp)

			// Authorize must never return nil: our OAuth flow is asynchronous,
			// so we never want the transport's silent retry.
			require.Error(t, err)
			var unauthorized *mcpUnauthorized
			require.ErrorAs(t, err, &unauthorized)
			require.Equal(t, tt.wantMetadataURL, unauthorized.MetadataURL())
			if tt.wantMetadataURL == "" {
				require.Error(t, unauthorized.Unwrap())
			}
		})
	}
}

// TestUserOAuthHandlerAuthorize403Gating verifies that a 403 only surfaces as
// an OAuth problem when the challenge is OAuth-remediable (resource_metadata
// or error="insufficient_scope"); a plain authorization denial must not be
// converted into a re-authentication prompt.
func TestUserOAuthHandlerAuthorize403Gating(t *testing.T) {
	const metadataURL = "https://resource.example.com/.well-known/oauth-protected-resource"

	tests := []struct {
		name             string
		wwwAuthenticate  string
		wantUnauthorized bool
		wantMetadataURL  string
	}{
		{
			name:             "403 with insufficient_scope is OAuth-remediable",
			wwwAuthenticate:  `Bearer error="insufficient_scope", scope="files:read"`,
			wantUnauthorized: true,
		},
		{
			name:             "403 with resource_metadata is OAuth-remediable",
			wwwAuthenticate:  `Bearer resource_metadata="` + metadataURL + `"`,
			wantUnauthorized: true,
			wantMetadataURL:  metadataURL,
		},
		{
			name:             "403 with plain challenge is not an OAuth problem",
			wwwAuthenticate:  `Bearer realm="mcp"`,
			wantUnauthorized: false,
		},
		{
			name:             "403 without challenge is not an OAuth problem",
			wwwAuthenticate:  "",
			wantUnauthorized: false,
		},
	}

	handler := &userOAuthHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("forbidden")),
			}
			if tt.wwwAuthenticate != "" {
				resp.Header.Add("WWW-Authenticate", tt.wwwAuthenticate)
			}

			err := handler.Authorize(context.Background(), &http.Request{}, resp)
			require.Error(t, err, "Authorize must never return nil")

			var unauthorized *mcpUnauthorized
			if tt.wantUnauthorized {
				require.ErrorAs(t, err, &unauthorized)
				require.Equal(t, tt.wantMetadataURL, unauthorized.MetadataURL())
			} else {
				require.False(t, errors.As(err, &unauthorized),
					"plain 403 must not surface as an OAuth re-authentication prompt")
			}
		})
	}
}

// boundTestEnvelope returns a v1 grant envelope pinned to the given token
// endpoint and the static test client.
func boundTestEnvelope(serverURL string, token *oauth2.Token) *storedTokenEnvelope {
	return &storedTokenEnvelope{
		Version:       tokenEnvelopeVersion,
		Token:         token,
		Issuer:        serverURL,
		TokenEndpoint: serverURL + "/token",
		AuthServerURL: serverURL,
		ClientID:      "static-client",
		Resource:      serverURL + "/mcp",
	}
}

// TestPersistingTokenSourceInvalidGrantClearsGrant verifies that when the
// refresh token is genuinely dead (invalid_grant under the serialized lease,
// i.e. not a concurrency artifact) the grant is cleared and re-authentication
// is required.
func TestPersistingTokenSourceInvalidGrantClearsGrant(t *testing.T) {
	const userID = "user123"
	const serverID = "rotated-server"

	server := newRefreshTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
	})
	manager, kv := newStatefulKVManager(t, nil, server.Client())

	kv.putEnvelope(t, userID, serverID, boundTestEnvelope(server.URL, &oauth2.Token{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(-time.Hour),
	}))

	handler := newUserOAuthHandler(userID, ServerConfig{
		Name:         serverID,
		BaseURL:      server.URL,
		ClientID:     "static-client",
		ClientSecret: "static-secret",
	}, manager)

	tokenSource, err := handler.TokenSource(context.Background())
	require.NoError(t, err)

	_, err = tokenSource.Token()
	require.Error(t, err)
	var unauthorized *mcpUnauthorized
	require.ErrorAs(t, err, &unauthorized)
	require.Contains(t, err.Error(), "re-authentication required")
	require.False(t, kv.exists(userID, serverID), "the dead grant must be cleared")
}

// TestPersistingTokenSourceRefreshTimeout verifies that a hung identity
// provider cannot stall a token refresh indefinitely: the refresh is bounded
// by oauthPrepTimeout even though the SDK supplies an unbounded connection
// context.
func TestPersistingTokenSourceRefreshTimeout(t *testing.T) {
	const userID = "user123"
	const serverID = "hung-server"

	previousTimeout := oauthPrepTimeout
	oauthPrepTimeout = 200 * time.Millisecond
	t.Cleanup(func() { oauthPrepTimeout = previousTimeout })

	release := make(chan struct{})
	server := newRefreshTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	// Registered after the server so it runs BEFORE server.Close (LIFO):
	// Close waits for in-flight handlers, which block on release.
	t.Cleanup(func() { close(release) })
	manager, kv := newStatefulKVManager(t, nil, server.Client())

	kv.putEnvelope(t, userID, serverID, boundTestEnvelope(server.URL, &oauth2.Token{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(-time.Hour),
	}))

	handler := newUserOAuthHandler(userID, ServerConfig{
		Name:         serverID,
		BaseURL:      server.URL,
		ClientID:     "static-client",
		ClientSecret: "static-secret",
	}, manager)

	tokenSource, err := handler.TokenSource(context.Background())
	require.NoError(t, err)

	start := time.Now()
	_, err = tokenSource.Token()
	elapsed := time.Since(start)
	require.Error(t, err, "hung token endpoint must fail the refresh")
	require.Less(t, elapsed, 5*time.Second, "refresh must be bounded by oauthPrepTimeout, not hang")
}

// TestPersistingTokenSourceLegacyTokenForcesReauth verifies that a
// pre-envelope grant (bare token, no pinned refresh destination) is usable
// while valid but cannot be refreshed: refreshing through rediscovery would
// let a compromised server redirect the refresh token, so the grant is
// cleared and re-authentication is required.
func TestPersistingTokenSourceLegacyTokenForcesReauth(t *testing.T) {
	const userID = "user123"
	const serverID = "legacy-server"

	tests := []struct {
		name       string
		token      *oauth2.Token
		wantReAuth bool
	}{
		{
			name: "valid legacy token is still usable",
			token: &oauth2.Token{
				AccessToken: "legacy-access",
				TokenType:   "Bearer",
				Expiry:      time.Now().Add(time.Hour),
			},
		},
		{
			name: "expired legacy token forces re-authentication",
			token: &oauth2.Token{
				AccessToken:  "legacy-access",
				RefreshToken: "legacy-refresh",
				TokenType:    "Bearer",
				Expiry:       time.Now().Add(-time.Hour),
			},
			wantReAuth: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, kv := newStatefulKVManager(t, nil, &http.Client{})
			// A bare oauth2.Token in the KV store (pre-envelope layout).
			kv.putLegacyToken(t, userID, serverID, tt.token)

			handler := newUserOAuthHandler(userID, ServerConfig{Name: serverID, BaseURL: "https://mcp.example.com"}, manager)
			tokenSource, err := handler.TokenSource(context.Background())
			require.NoError(t, err)
			require.NotNil(t, tokenSource)

			token, err := tokenSource.Token()
			if tt.wantReAuth {
				require.Error(t, err)
				var unauthorized *mcpUnauthorized
				require.ErrorAs(t, err, &unauthorized)
				require.Contains(t, err.Error(), "re-authentication required")
				require.False(t, kv.exists(userID, serverID), "expired legacy token must be cleared")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "legacy-access", token.AccessToken)
		})
	}
}

// TestTokenSourceRejectsUnknownEnvelopeVersion verifies that a grant written
// by a newer node (a future v2 layout) is not silently treated as v1 — which
// would ignore binding fields it requires. It fails closed.
func TestTokenSourceRejectsUnknownEnvelopeVersion(t *testing.T) {
	const userID = "user123"
	const serverID = "future-server"

	manager, kv := newStatefulKVManager(t, nil, &http.Client{})
	kv.putEnvelope(t, userID, serverID, &storedTokenEnvelope{
		Version: tokenEnvelopeVersion + 1,
		Token:   &oauth2.Token{AccessToken: "future-access", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)},
	})

	handler := newUserOAuthHandler(userID, ServerConfig{Name: serverID, BaseURL: "https://mcp.example.com"}, manager)
	_, err := handler.TokenSource(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported token envelope version")
}

func TestUserOAuthHandlerTokenSourceNoStoredToken(t *testing.T) {
	manager, _ := newStatefulKVManager(t, nil, &http.Client{})

	handler := newUserOAuthHandler("user123", ServerConfig{
		Name:    "no-token-server",
		BaseURL: "https://mcp.example.com",
	}, manager)

	tokenSource, err := handler.TokenSource(context.Background())
	require.NoError(t, err)
	require.Nil(t, tokenSource, "expected nil token source so the transport sends the request unauthenticated")
}

// newRefreshTestServer serves strict-compliant authorization server metadata
// and delegates /token to the provided handler.
func newRefreshTestServer(t *testing.T, tokenHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
				Issuer:                        server.URL,
				AuthorizationEndpoint:         server.URL + "/authorize",
				TokenEndpoint:                 server.URL + "/token",
				CodeChallengeMethodsSupported: []string{"S256"},
			}))
		case "/token":
			tokenHandler(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestPersistingTokenSourceRefresh drives a token refresh through the handler's
// token source against a live httptest authorization server, verifying the
// KV-persistence and invalid_grant semantics that used to live in
// authenticationTransport.
func TestPersistingTokenSourceRefresh(t *testing.T) {
	const userID = "user123"
	const serverID = "refresh-server"

	tests := []struct {
		name             string
		tokenHandler     http.HandlerFunc
		wantUnauthorized bool
	}{
		{
			name: "successful refresh persists rotated token",
			tokenHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "new-access",
					"refresh_token": "new-refresh",
					"token_type":    "Bearer",
					"expires_in":    3600,
				})
			},
			wantUnauthorized: false,
		},
		{
			name: "invalid_grant deletes stored token and surfaces unauthorized",
			tokenHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
			},
			wantUnauthorized: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newRefreshTestServer(t, tt.tokenHandler)
			manager, kv := newStatefulKVManager(t, nil, server.Client())

			expiredEnvelope := boundTestEnvelope(server.URL, &oauth2.Token{
				AccessToken:  "old-access",
				RefreshToken: "old-refresh",
				TokenType:    "Bearer",
				Expiry:       time.Now().Add(-time.Hour),
			})
			kv.putEnvelope(t, userID, serverID, expiredEnvelope)

			handler := newUserOAuthHandler(userID, ServerConfig{
				Name:         serverID,
				BaseURL:      server.URL,
				ClientID:     "static-client",
				ClientSecret: "static-secret",
			}, manager)

			tokenSource, err := handler.TokenSource(context.Background())
			require.NoError(t, err)
			require.NotNil(t, tokenSource)

			token, err := tokenSource.Token()
			if tt.wantUnauthorized {
				require.Error(t, err)
				var unauthorized *mcpUnauthorized
				require.ErrorAs(t, err, &unauthorized)
				require.Contains(t, err.Error(), "re-authentication required")
				require.False(t, kv.exists(userID, serverID), "dead grant must be cleared")
				return
			}

			require.NoError(t, err)
			require.Equal(t, "new-access", token.AccessToken)
			storedEnvelope := kv.storedEnvelope(t, userID, serverID)
			require.NotNil(t, storedEnvelope, "expected refreshed grant to be persisted to the KV store")
			require.Equal(t, "new-access", storedEnvelope.Token.AccessToken)
			require.Equal(t, "new-refresh", storedEnvelope.Token.RefreshToken)
			require.Equal(t, expiredEnvelope.TokenEndpoint, storedEnvelope.TokenEndpoint,
				"the refreshed grant must keep its authorization server binding")
		})
	}
}
