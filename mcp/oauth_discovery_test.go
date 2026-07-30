// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestCreateOAuthConfig_DiscoveryStrictnessAndLeniency verifies that discovery
// succeeds on strict, spec-compliant metadata without any fallback, and that
// the deliberate leniency fallbacks (RFC 9728 resource mismatch, RFC 8414
// issuer mismatch, missing PKCE advertisement) recover with a logged warning.
func TestCreateOAuthConfig_DiscoveryStrictnessAndLeniency(t *testing.T) {
	tests := []struct {
		name string
		// prmResourceSuffix is appended to the server URL in the advertised
		// protected resource metadata; non-empty values violate RFC 9728 §3.3.
		prmResourceSuffix string
		// asIssuer overrides the advertised issuer; empty means the compliant
		// value (the server URL).
		asIssuer    string
		pkceMethods []string
		wantWarn    bool
	}{
		{
			name:        "strict compliant metadata passes without fallback",
			pkceMethods: []string{"S256"},
			wantWarn:    false,
		},
		{
			name:              "protected resource mismatch recovers via lenient fallback",
			prmResourceSuffix: "/",
			pkceMethods:       []string{"S256"},
			wantWarn:          true,
		},
		{
			name:        "issuer mismatch recovers via lenient fallback",
			asIssuer:    "https://legacy.example.com",
			pkceMethods: []string{"S256"},
			wantWarn:    true,
		},
		{
			name:     "missing PKCE advertisement recovers via lenient fallback",
			wantWarn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var serverURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource":
					w.Header().Set("Content-Type", "application/json")
					require.NoError(t, json.NewEncoder(w).Encode(oauthex.ProtectedResourceMetadata{
						Resource:             serverURL + tt.prmResourceSuffix,
						AuthorizationServers: []string{serverURL},
						ScopesSupported:      []string{"read"},
					}))
				case "/.well-known/oauth-authorization-server":
					issuer := tt.asIssuer
					if issuer == "" {
						issuer = serverURL
					}
					w.Header().Set("Content-Type", "application/json")
					require.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
						Issuer:                        issuer,
						AuthorizationEndpoint:         serverURL + "/authorize",
						TokenEndpoint:                 serverURL + "/token",
						CodeChallengeMethodsSupported: tt.pkceMethods,
					}))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			serverURL = server.URL

			manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
			if tt.wantWarn {
				mockClient.On("LogWarn", mock.AnythingOfType("string"), mock.Anything).Return()
			}

			config, err := manager.createOAuthConfig(context.Background(), serverURL, "", &StaticOAuthCredentials{
				ClientID:     "static-client",
				ClientSecret: "static-secret",
			})

			require.NoError(t, err)
			require.Equal(t, serverURL+"/authorize", config.Endpoint.AuthURL)
			require.Equal(t, serverURL+"/token", config.Endpoint.TokenURL)
			require.Equal(t, []string{"read"}, config.Scopes)
			if tt.wantWarn {
				mockClient.AssertCalled(t, "LogWarn", mock.AnythingOfType("string"), mock.Anything)
			}
			// When wantWarn is false, LogWarn is not registered on the mock,
			// so any lenient fallback would fail the test.
		})
	}
}

// TestLoadOrCreateClientCredentials_RegistrationEndpointDiscovery covers the
// DCR path where no registration endpoint was discovered upstream: it must be
// discovered from the authorization server metadata at the base URL, and
// registration failures must surface as errors.
func TestLoadOrCreateClientCredentials_RegistrationEndpointDiscovery(t *testing.T) {
	tests := []struct {
		name                 string
		advertiseEndpoint    bool
		registrationHandler  http.HandlerFunc
		wantClientID         string
		wantErrContains      string
		expectStoreAndDebug  bool
		expectRegistration   bool
		expectMetadataLookup bool
	}{
		{
			name:              "discovers registration endpoint from server metadata",
			advertiseEndpoint: true,
			registrationHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"client_id":     "discovered-client",
					"client_secret": "discovered-secret",
				})
			},
			wantClientID:        "discovered-client",
			expectStoreAndDebug: true,
		},
		{
			name:              "fails when server does not support dynamic client registration",
			advertiseEndpoint: false,
			wantErrContains:   "does not support dynamic client registration",
		},
		{
			name:              "surfaces registration error response",
			advertiseEndpoint: true,
			registrationHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":             "invalid_redirect_uri",
					"error_description": "redirect URI not allowed",
				})
			},
			wantErrContains: "failed to register OAuth client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var serverURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/oauth-authorization-server":
					metadata := oauthex.AuthServerMeta{
						Issuer:                serverURL,
						AuthorizationEndpoint: serverURL + "/authorize",
						TokenEndpoint:         serverURL + "/token",
					}
					if tt.advertiseEndpoint {
						metadata.RegistrationEndpoint = serverURL + "/register"
					}
					w.Header().Set("Content-Type", "application/json")
					require.NoError(t, json.NewEncoder(w).Encode(metadata))
				case "/register":
					require.Equal(t, http.MethodPost, r.Method)
					require.NotNil(t, tt.registrationHandler)
					tt.registrationHandler(w, r)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			serverURL = server.URL

			manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
			mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.ClientCredentials")).Return(nil).Once()
			if tt.expectStoreAndDebug {
				mockClient.On("KVSet", mock.AnythingOfType("string"), mock.Anything).Return(nil).Once()
				mockClient.On("LogDebug", mock.AnythingOfType("string"), mock.Anything).Return().Once()
			}

			creds, err := manager.loadOrCreateClientCredentials(context.Background(), serverURL, nil, "")

			if tt.wantErrContains != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrContains)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantClientID, creds.ClientID)
		})
	}
}

// TestConstructWellKnownURL is ported from the previous hand-rolled metadata
// implementation; the RFC 8414 §3.1 path-insertion behavior must be preserved.
func TestConstructWellKnownURL(t *testing.T) {
	tests := []struct {
		name        string
		issuer      string
		suffix      string
		expectedURL string
	}{
		{
			name:        "Simple URL without path",
			issuer:      "https://example.com",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server",
		},
		{
			name:        "URL with single path component",
			issuer:      "https://example.com/issuer1",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server/issuer1",
		},
		{
			name:        "URL with multiple path components",
			issuer:      "https://example.com/path/to/issuer",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server/path/to/issuer",
		},
		{
			name:        "URL with trailing slash",
			issuer:      "https://example.com/issuer1/",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server/issuer1",
		},
		{
			name:        "URL with port",
			issuer:      "https://example.com:8443",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com:8443/.well-known/oauth-authorization-server",
		},
		{
			name:        "URL with port and path",
			issuer:      "https://example.com:8443/issuer1",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com:8443/.well-known/oauth-authorization-server/issuer1",
		},
		{
			name:        "Protected resource metadata suffix",
			issuer:      "https://resource.example.com",
			suffix:      "oauth-protected-resource",
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
		},
		{
			name:        "Protected resource metadata suffix with path",
			issuer:      "https://resource.example.com/api/v1",
			suffix:      "oauth-protected-resource",
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource/api/v1",
		},
		{
			name:        "localhost URL",
			issuer:      "http://localhost:3000",
			suffix:      "oauth-authorization-server",
			expectedURL: "http://localhost:3000/.well-known/oauth-authorization-server",
		},
		{
			name:        "localhost URL with path",
			issuer:      "http://localhost:3000/oauth",
			suffix:      "oauth-authorization-server",
			expectedURL: "http://localhost:3000/.well-known/oauth-authorization-server/oauth",
		},
		{
			name:        "URL with only root path",
			issuer:      "https://example.com/",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server",
		},
		{
			name:        "URL with subdomain",
			issuer:      "https://auth.example.com/oauth",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://auth.example.com/.well-known/oauth-authorization-server/oauth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := constructWellKnownURL(tt.issuer, tt.suffix)
			require.NoError(t, err)
			require.Equal(t, tt.expectedURL, result)
		})
	}
}
