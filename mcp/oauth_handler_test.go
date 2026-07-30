// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
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
