// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewClientSurfacesOAuthNeededErrorOn401 verifies that connecting to an MCP
// server that answers every request with 401 surfaces an *OAuthNeededError
// carrying the resource_metadata URL from the WWW-Authenticate challenge. The
// 401 is converted into *mcpUnauthorized by authenticationTransport and must be
// detected via errors.As through the error chain returned by client.Connect.
func TestNewClientSurfacesOAuthNeededErrorOn401(t *testing.T) {
	tests := []struct {
		name            string
		wwwAuthenticate string
		wantMetadataURL string
	}{
		{
			name:            "401 with WWW-Authenticate resource metadata",
			wwwAuthenticate: `Bearer resource_metadata="https://oauth.example.com/.well-known/oauth-protected-resource"`,
			wantMetadataURL: "https://oauth.example.com/.well-known/oauth-protected-resource",
		},
		{
			name:            "401 without WWW-Authenticate header",
			wwwAuthenticate: "",
			wantMetadataURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.wwwAuthenticate != "" {
					w.Header().Set("WWW-Authenticate", tt.wwwAuthenticate)
				}
				w.WriteHeader(http.StatusUnauthorized)
			}))
			t.Cleanup(httpServer.Close)

			client, err := NewClient(context.Background(), "user-id", ServerConfig{
				Name:    "oauth-server",
				BaseURL: httpServer.URL,
				Enabled: true,
			}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), newTestToolsCache(), false)
			require.Error(t, err)
			require.Nil(t, client)

			var oauthErr *OAuthNeededError
			require.ErrorAs(t, err, &oauthErr)
			require.Equal(t, tt.wantMetadataURL, oauthErr.MetadataURL())
		})
	}
}
