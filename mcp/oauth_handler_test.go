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

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/mock"
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

// TestPersistingTokenSourceInvalidGrantRotatedToken covers the HA race: this
// node's refresh fails with invalid_grant because another node already
// rotated the token. The concurrently stored (valid) token must NOT be
// deleted, and the error must not prompt re-authentication.
func TestPersistingTokenSourceInvalidGrantRotatedToken(t *testing.T) {
	const userID = "user123"
	const serverID = "rotated-server"

	server := newRefreshTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
	})
	manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())

	staleToken := &oauth2.Token{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(-time.Hour),
	}
	rotatedToken := &oauth2.Token{
		AccessToken:  "rotated-access",
		RefreshToken: "rotated-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}

	// First load (TokenSource) sees the stale token; the re-load after the
	// invalid_grant sees the token another node rotated in the meantime.
	// KVDelete is deliberately not registered: an unexpected delete of the
	// rotated token fails the test.
	mockClient.On("KVGet", buildTokenKey(userID, serverID), mock.AnythingOfType("*oauth2.Token")).
		Run(func(args mock.Arguments) { *(args.Get(1).(*oauth2.Token)) = *staleToken }).
		Return(nil).Once()
	mockClient.On("KVGet", buildTokenKey(userID, serverID), mock.AnythingOfType("*oauth2.Token")).
		Run(func(args mock.Arguments) { *(args.Get(1).(*oauth2.Token)) = *rotatedToken }).
		Return(nil).Once()

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
	require.Contains(t, err.Error(), "rotated concurrently")
	var unauthorized *mcpUnauthorized
	require.False(t, errors.As(err, &unauthorized),
		"a concurrent rotation must not prompt re-authentication")
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
	manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())

	expiredToken := &oauth2.Token{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(-time.Hour),
	}
	mockClient.On("KVGet", buildTokenKey(userID, serverID), mock.AnythingOfType("*oauth2.Token")).
		Run(func(args mock.Arguments) { *(args.Get(1).(*oauth2.Token)) = *expiredToken }).
		Return(nil)

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

func TestUserOAuthHandlerTokenSourceNoStoredToken(t *testing.T) {
	manager, mockClient := setupTestOAuthManager(t)
	mockClient.On("KVGet", buildTokenKey("user123", "no-token-server"), mock.AnythingOfType("*oauth2.Token")).
		Return(mmapi.ErrKVNotFound)

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
			manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())

			expiredToken := &oauth2.Token{
				AccessToken:  "old-access",
				RefreshToken: "old-refresh",
				TokenType:    "Bearer",
				Expiry:       time.Now().Add(-time.Hour),
			}
			mockClient.On("KVGet", buildTokenKey(userID, serverID), mock.AnythingOfType("*oauth2.Token")).
				Run(func(args mock.Arguments) {
					token := args.Get(1).(*oauth2.Token)
					*token = *expiredToken
				}).
				Return(nil)

			var storedToken *oauth2.Token
			if tt.wantUnauthorized {
				mockClient.On("KVDelete", buildTokenKey(userID, serverID)).Return(nil).Once()
			} else {
				mockClient.On("KVSet", buildTokenKey(userID, serverID), mock.AnythingOfType("*oauth2.Token")).
					Run(func(args mock.Arguments) {
						storedToken = args.Get(1).(*oauth2.Token)
					}).
					Return(nil).
					Once()
			}

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
				return
			}

			require.NoError(t, err)
			require.Equal(t, "new-access", token.AccessToken)
			require.NotNil(t, storedToken, "expected refreshed token to be persisted to the KV store")
			require.Equal(t, "new-access", storedToken.AccessToken)
			require.Equal(t, "new-refresh", storedToken.RefreshToken)
		})
	}
}
