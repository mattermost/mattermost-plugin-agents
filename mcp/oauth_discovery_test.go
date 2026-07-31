// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestCreateOAuthConfig_DiscoveryStrictnessAndLeniency verifies the discovery
// strictness posture: spec-compliant metadata resolves the advertised
// (non-conventional) endpoints; a missing PKCE advertisement — the one
// deliberate leniency — recovers with a logged warning; and spec violations
// (RFC 9728 resource mismatch, RFC 8414 issuer mismatch) are treated as
// discovery failures that degrade to the documented fallbacks instead of
// being papered over.
//
// The advertised endpoints use /custom-* paths so discovered endpoints are
// observably different from the conventional /authorize and /token fallbacks.
func TestCreateOAuthConfig_DiscoveryStrictnessAndLeniency(t *testing.T) {
	tests := []struct {
		name string
		// prmResourceSuffix is appended to the server URL in the advertised
		// protected resource metadata; non-empty values violate RFC 9728 §3.3.
		prmResourceSuffix string
		// asIssuer overrides the advertised issuer; empty means the compliant
		// value (the server URL).
		asIssuer      string
		pkceMethods   []string
		wantWarn      bool
		wantAuthPath  string
		wantTokenPath string
		wantScopes    []string
	}{
		{
			name:          "strict compliant metadata resolves discovered endpoints",
			pkceMethods:   []string{"S256"},
			wantAuthPath:  "/custom-authorize",
			wantTokenPath: "/custom-token",
			wantScopes:    []string{"read"},
		},
		{
			name:          "missing PKCE advertisement recovers via lenient fallback",
			wantWarn:      true,
			wantAuthPath:  "/custom-authorize",
			wantTokenPath: "/custom-token",
			wantScopes:    []string{"read"},
		},
		{
			// PRM succeeded and named the issuer, but its metadata fails the
			// RFC 8414 issuer check, so conventional endpoints on the issuer
			// are used instead of the advertised custom ones.
			name:          "issuer mismatch fails AS discovery and falls back to conventional endpoints",
			asIssuer:      "https://legacy.example.com",
			pkceMethods:   []string{"S256"},
			wantAuthPath:  "/authorize",
			wantTokenPath: "/token",
			wantScopes:    []string{"read"},
		},
		{
			// PRM discovery fails the RFC 9728 §3.3 resource check, so scopes
			// are lost and discovery proceeds via AS metadata at the base URL
			// (which is compliant here and yields the custom endpoints).
			name:              "resource mismatch fails PRM discovery and falls back to AS metadata at base URL",
			prmResourceSuffix: "/",
			pkceMethods:       []string{"S256"},
			wantAuthPath:      "/custom-authorize",
			wantTokenPath:     "/custom-token",
			wantScopes:        nil,
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
						AuthorizationEndpoint:         serverURL + "/custom-authorize",
						TokenEndpoint:                 serverURL + "/custom-token",
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
			require.Equal(t, serverURL+tt.wantAuthPath, config.Endpoint.AuthURL)
			require.Equal(t, serverURL+tt.wantTokenPath, config.Endpoint.TokenURL)
			require.Equal(t, tt.wantScopes, config.Scopes)
			if tt.wantWarn {
				mockClient.AssertCalled(t, "LogWarn", mock.AnythingOfType("string"), mock.Anything)
			}
			// When wantWarn is false, LogWarn is not registered on the mock,
			// so any lenient fallback would fail the test.
		})
	}
}

// TestCreateOAuthConfig_ASMetadataFailureFallsBackToIssuer verifies that when
// protected resource metadata names an external authorization server whose
// metadata cannot be fetched, the conventional /authorize and /token fallback
// endpoints are derived from that issuer — not from the MCP resource server.
func TestCreateOAuthConfig_ASMetadataFailureFallsBackToIssuer(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(oauthex.ProtectedResourceMetadata{
				Resource: serverURL,
				// The issuer's well-known metadata path is not served, so
				// fetchAuthorizationServerMetadata fails for it.
				AuthorizationServers: []string{serverURL + "/as"},
			}))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	serverURL = server.URL

	manager, _ := setupTestOAuthManagerFull(t, nil, server.Client())

	config, err := manager.createOAuthConfig(context.Background(), serverURL, "", &StaticOAuthCredentials{
		ClientID:     "static-client",
		ClientSecret: "static-secret",
	})
	require.NoError(t, err)
	require.Equal(t, serverURL+"/as/authorize", config.Endpoint.AuthURL,
		"fallback authorize endpoint must live on the discovered issuer")
	require.Equal(t, serverURL+"/as/token", config.Endpoint.TokenURL,
		"fallback token endpoint must live on the discovered issuer")
}

// TestCreateOAuthConfigCachedReusesDiscovery verifies the hot-path cache: a
// second call within the TTL must not re-run metadata discovery, and the
// uncached createOAuthConfig must remain unaffected by the cache.
func TestCreateOAuthConfigCachedReusesDiscovery(t *testing.T) {
	var prmFetches atomic.Int32
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			prmFetches.Add(1)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(oauthex.ProtectedResourceMetadata{
				Resource:             serverURL,
				AuthorizationServers: []string{serverURL},
			}))
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
				Issuer:                        serverURL,
				AuthorizationEndpoint:         serverURL + "/authorize",
				TokenEndpoint:                 serverURL + "/token",
				CodeChallengeMethodsSupported: []string{"S256"},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	serverURL = server.URL

	manager, _ := setupTestOAuthManagerFull(t, nil, server.Client())
	staticCreds := &StaticOAuthCredentials{ClientID: "static-client", ClientSecret: "static-secret"}

	first, err := manager.createOAuthConfigCached(context.Background(), serverURL, "", staticCreds)
	require.NoError(t, err)
	require.Equal(t, int32(1), prmFetches.Load())

	second, err := manager.createOAuthConfigCached(context.Background(), serverURL, "", staticCreds)
	require.NoError(t, err)
	require.Same(t, first, second, "cached call within the TTL must reuse the discovered config")
	require.Equal(t, int32(1), prmFetches.Load(), "cached call must not re-run discovery")

	_, err = manager.createOAuthConfig(context.Background(), serverURL, "", staticCreds)
	require.NoError(t, err)
	require.Equal(t, int32(2), prmFetches.Load(), "uncached call must always run discovery")

	rotated := &StaticOAuthCredentials{ClientID: staticCreds.ClientID, ClientSecret: "rotated-secret"}
	rotatedConfig, err := manager.createOAuthConfigCached(context.Background(), serverURL, "", rotated)
	require.NoError(t, err)
	require.NotSame(t, first, rotatedConfig, "rotating the static secret must bypass the cached config")
	require.Equal(t, int32(3), prmFetches.Load(), "rotated secret must re-run discovery immediately")
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
