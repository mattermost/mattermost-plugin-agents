// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// TestOAuthRoundTripper exercises the thin adapter used for the legacy
// HTTP+SSE transport: it must inject the stored token via the handler's token
// source and delegate 401 responses to handler.Authorize.
func TestOAuthRoundTripper(t *testing.T) {
	const userID = "user123"
	const serverID = "sse-server"
	const metadataURL = "https://resource.example.com/.well-known/oauth-protected-resource"

	tests := []struct {
		name            string
		storedToken     *oauth2.Token
		wantMetadataURL string
		wantStatus      int
	}{
		{
			name: "stored token is injected as bearer authorization",
			storedToken: &oauth2.Token{
				AccessToken: "stored-access",
				TokenType:   "Bearer",
				Expiry:      time.Now().Add(time.Hour),
			},
			wantStatus: http.StatusOK,
		},
		{
			name:            "401 without token surfaces mcpUnauthorized with metadata URL",
			storedToken:     nil,
			wantMetadataURL: metadataURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Captured in the handler goroutine and asserted from the test
			// goroutine after RoundTrip returns (require must not be called
			// from non-test goroutines).
			var gotAuthorization atomic.Value
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Metadata discovery endpoints intentionally 404 so
				// createOAuthConfig uses its hardcoded endpoint fallback.
				if r.URL.Path != "/mcp" {
					http.NotFound(w, r)
					return
				}
				if tt.storedToken == nil {
					w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+metadataURL+`"`)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				gotAuthorization.Store(r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(server.Close)

			manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
			tokenGet := mockClient.On("KVGet", buildTokenKey(userID, serverID), mock.AnythingOfType("*mcp.storedTokenEnvelope"))
			if tt.storedToken == nil {
				tokenGet.Return(mmapi.ErrKVNotFound)
			} else {
				envelope := boundTestEnvelope(server.URL, tt.storedToken)
				tokenGet.Run(func(args mock.Arguments) {
					*(args.Get(1).(*storedTokenEnvelope)) = *envelope
				}).Return(nil)
			}

			handler := newUserOAuthHandler(userID, ServerConfig{
				Name:         serverID,
				BaseURL:      server.URL + "/mcp",
				ClientID:     "static-client",
				ClientSecret: "static-secret",
			}, manager)

			transport := &oauthRoundTripper{handler: handler, base: server.Client().Transport}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/mcp", nil)
			require.NoError(t, err)

			resp, err := transport.RoundTrip(req)

			if tt.wantMetadataURL != "" {
				require.Error(t, err)
				require.Nil(t, resp)
				var unauthorized *mcpUnauthorized
				require.ErrorAs(t, err, &unauthorized)
				require.Equal(t, tt.wantMetadataURL, unauthorized.MetadataURL())
				return
			}

			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, tt.wantStatus, resp.StatusCode)
			require.Equal(t, "Bearer stored-access", gotAuthorization.Load(),
				"stored token must be injected as bearer authorization")
		})
	}
}
