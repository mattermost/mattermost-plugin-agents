// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
)

// TestResolveOAuthConfig_DiscoveryStrictnessAndLeniency verifies the discovery
// strictness posture: spec-compliant metadata resolves the advertised
// (non-conventional) endpoints; a missing PKCE advertisement — the one
// deliberate leniency — recovers with a logged warning; and spec violations
// (RFC 9728 resource mismatch, RFC 8414 issuer mismatch) are treated as
// discovery failures that degrade to the documented fallbacks instead of
// being papered over.
//
// The advertised endpoints use /custom-* paths so discovered endpoints are
// observably different from the conventional /authorize and /token fallbacks.
func TestResolveOAuthConfig_DiscoveryStrictnessAndLeniency(t *testing.T) {
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
			// PRM succeeded and named the issuer, but its metadata declares a
			// different issuer whose own metadata is unreachable (loopback
			// port 1 fails fast), so the issuer-follow fails and conventional
			// endpoints on the issuer are used instead of the advertised
			// custom ones — with a warning.
			name:          "issuer mismatch with unreachable declared issuer falls back to conventional endpoints",
			asIssuer:      "https://127.0.0.1:1",
			pkceMethods:   []string{"S256"},
			wantWarn:      true,
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
					assert.NoError(t, json.NewEncoder(w).Encode(oauthex.ProtectedResourceMetadata{
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
					assert.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
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

			resolved, err := manager.resolveOAuthConfig(context.Background(), serverURL, "", &StaticOAuthCredentials{
				ClientID:     "static-client",
				ClientSecret: "static-secret",
			})

			require.NoError(t, err)
			config := resolved.config
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

// TestFetchAuthorizationServerMetadataOrderedVariants verifies the MCP
// specification's ordered well-known walk: RFC 8414 preferred, OpenID Connect
// Discovery accepted as fallback, path-inserted variants for pathed issuers.
func TestFetchAuthorizationServerMetadataOrderedVariants(t *testing.T) {
	writeMeta := func(t *testing.T, w http.ResponseWriter, issuer, marker string) {
		t.Helper()
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
			Issuer:                        issuer,
			AuthorizationEndpoint:         issuer + "/" + marker + "-authorize",
			TokenEndpoint:                 issuer + "/" + marker + "-token",
			CodeChallengeMethodsSupported: []string{"S256"},
		}))
	}

	tests := []struct {
		name         string
		issuerPath   string // path component of the issuer, "" for root
		servedPaths  []string
		wantAuthPath string
		wantErr      bool
	}{
		{
			name:         "root issuer served only via openid-configuration",
			servedPaths:  []string{"/.well-known/openid-configuration"},
			wantAuthPath: "/oidc-authorize",
		},
		{
			name:         "oauth-authorization-server preferred over openid-configuration",
			servedPaths:  []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"},
			wantAuthPath: "/rfc8414-authorize",
		},
		{
			name:         "pathed issuer served via path-inserted openid-configuration",
			issuerPath:   "/tenant1",
			servedPaths:  []string{"/.well-known/openid-configuration/tenant1"},
			wantAuthPath: "/oidc-authorize",
		},
		{
			name:         "pathed issuer served via path-appended openid-configuration",
			issuerPath:   "/tenant1",
			servedPaths:  []string{"/tenant1/.well-known/openid-configuration"},
			wantAuthPath: "/oidc-authorize",
		},
		{
			name:        "no variant served fails discovery",
			servedPaths: nil,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var issuer string
			served := make(map[string]bool, len(tt.servedPaths))
			for _, p := range tt.servedPaths {
				served[p] = true
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !served[r.URL.Path] {
					http.NotFound(w, r)
					return
				}
				marker := "rfc8414"
				if strings.Contains(r.URL.Path, "openid-configuration") {
					marker = "oidc"
				}
				writeMeta(t, w, issuer, marker)
			}))
			t.Cleanup(server.Close)
			issuer = server.URL + tt.issuerPath

			manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
			mockClient.On("LogDebug", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()
			asm, err := manager.fetchAuthorizationServerMetadata(context.Background(), issuer)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, issuer+tt.wantAuthPath, asm.AuthorizationEndpoint)
		})
	}
}

// TestFetchAuthorizationServerMetadataLenientIssuerTOCTOU verifies that the
// PKCE-lenient re-fetch re-validates issuer equality: a server answering the
// second fetch with a different issuer must fail discovery.
func TestFetchAuthorizationServerMetadataLenientIssuerTOCTOU(t *testing.T) {
	var issuer string
	var fetches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		respIssuer := issuer
		if fetches.Add(1) > 1 {
			// Second (lenient) fetch: switch the issuer.
			respIssuer = "https://evil.example.com"
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
			Issuer:                respIssuer,
			AuthorizationEndpoint: issuer + "/authorize",
			TokenEndpoint:         issuer + "/token",
			// No PKCE advertisement: strict fetch fails with the PKCE error,
			// triggering the lenient re-fetch.
		}))
	}))
	t.Cleanup(server.Close)
	issuer = server.URL

	manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
	mockClient.On("LogWarn", mock.AnythingOfType("string"), mock.Anything).Return()

	_, err := manager.fetchAuthorizationServerMetadata(context.Background(), issuer)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match expected issuer")
}

// TestCheckHTTPSOrLoopbackURL pins the endpoint scheme policy: HTTPS anywhere,
// plain HTTP only on loopback, all other schemes rejected even on loopback.
func TestCheckHTTPSOrLoopbackURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https any host", url: "https://as.example.com/token"},
		{name: "http localhost", url: "http://localhost:8080/token"},
		{name: "http loopback ip", url: "http://127.0.0.1:8080/token"},
		{name: "empty is allowed", url: ""},
		{name: "http non-loopback rejected", url: "http://as.example.com/token", wantErr: true},
		{name: "ftp on loopback rejected", url: "ftp://127.0.0.1/token", wantErr: true},
		{name: "javascript scheme rejected", url: "javascript:alert(1)", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkHTTPSOrLoopbackURL(tt.url)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestFetchProtectedResourceMetadataRootFallback verifies the ordered PRM walk
// for pathed server URLs: when the path-inserted variant is absent, the root
// well-known document (whose resource is the server origin) is used.
func TestFetchProtectedResourceMetadataRootFallback(t *testing.T) {
	var origin string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-protected-resource" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(oauthex.ProtectedResourceMetadata{
			Resource:             origin,
			AuthorizationServers: []string{origin},
		}))
	}))
	t.Cleanup(server.Close)
	origin = server.URL

	manager, _ := setupTestOAuthManagerFull(t, nil, server.Client())
	prm, err := manager.fetchProtectedResourceMetadata(context.Background(), origin+"/mcp/v1", "")
	require.NoError(t, err)
	require.Equal(t, []string{origin}, prm.AuthorizationServers)
}

// TestResolveOAuthConfig_ASMetadataFailureFallsBackToIssuer verifies that when
// protected resource metadata names an external authorization server whose
// metadata cannot be fetched, the conventional /authorize and /token fallback
// endpoints are derived from that issuer — not from the MCP resource server.
func TestResolveOAuthConfig_ASMetadataFailureFallsBackToIssuer(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			assert.NoError(t, json.NewEncoder(w).Encode(oauthex.ProtectedResourceMetadata{
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

	manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
	mockClient.On("LogWarn", mock.AnythingOfType("string"), mock.Anything).Return()

	resolved, err := manager.resolveOAuthConfig(context.Background(), serverURL, "", &StaticOAuthCredentials{
		ClientID:     "static-client",
		ClientSecret: "static-secret",
	})
	require.NoError(t, err)
	config := resolved.config
	require.Equal(t, serverURL+"/as/authorize", config.Endpoint.AuthURL,
		"fallback authorize endpoint must live on the discovered issuer")
	require.Equal(t, serverURL+"/as/token", config.Endpoint.TokenURL,
		"fallback token endpoint must live on the discovered issuer")
}

// TestLoadOrCreateClientCredentials_RegistrationEndpointDiscovery covers the
// DCR path where no registration endpoint was discovered upstream: it must be
// discovered from the authorization server metadata at the base URL, and
// registration failures must surface as errors.
func TestLoadOrCreateClientCredentials_Registration(t *testing.T) {
	tests := []struct {
		name                string
		registrationEndpt   bool // pass a real registration endpoint
		registrationHandler http.HandlerFunc
		wantClientID        string
		wantErrContains     string
		expectStoreAndDebug bool
	}{
		{
			name:              "registers against the provided endpoint",
			registrationEndpt: true,
			registrationHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"client_id":     "registered-client",
					"client_secret": "registered-secret",
				})
			},
			wantClientID:        "registered-client",
			expectStoreAndDebug: true,
		},
		{
			// The key fix: with no registration endpoint from the selected
			// issuer's own metadata, we fail closed instead of stripping the
			// path and rediscovering at the root (which could register against
			// a different authorization server).
			name:              "fails closed when no registration endpoint is available",
			registrationEndpt: false,
			wantErrContains:   "does not advertise a dynamic client registration endpoint",
		},
		{
			name:              "surfaces registration error response",
			registrationEndpt: true,
			registrationHandler: func(w http.ResponseWriter, _ *http.Request) {
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
			var registrationCalled bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/register" && tt.registrationHandler != nil {
					registrationCalled = true
					assert.Equal(t, http.MethodPost, r.Method)
					tt.registrationHandler(w, r)
					return
				}
				// No well-known metadata is served: fail-closed must NOT fall
				// back to root discovery.
				http.NotFound(w, r)
			}))
			t.Cleanup(server.Close)

			manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
			mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.ClientCredentials")).Return(nil).Once()
			if tt.expectStoreAndDebug {
				mockClient.On("KVSet", mock.AnythingOfType("string"), mock.Anything).Return(nil).Once()
			}

			registrationEndpoint := ""
			if tt.registrationEndpt {
				registrationEndpoint = server.URL + "/register"
			}
			creds, err := manager.loadOrCreateClientCredentials(context.Background(), server.URL, nil, registrationEndpoint, nil)

			if tt.wantErrContains != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrContains)
				if !tt.registrationEndpt {
					require.False(t, registrationCalled, "must not register anywhere when no endpoint is advertised")
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantClientID, creds.ClientID)
		})
	}
}

// TestFetchAuthorizationServerMetadataFollowsDeclaredIssuer replicates the
// Atlassian MCP server topology that produced a broken authorization redirect
// in the field: the MCP host serves no protected resource metadata, and its
// authorization-server metadata declares a DIFFERENT issuer host (a CDN/proxy
// split). Discovery must follow the declared issuer once, require
// self-consistency there, and use the advertised endpoints — not fall back to
// conventional /authorize on the MCP host (which 404s).
func TestFetchAuthorizationServerMetadataFollowsDeclaredIssuer(t *testing.T) {
	// "issuerHost" plays cf.mcp.atlassian.com; "mcpHost" plays mcp.atlassian.com.
	var issuerHost, mcpHost *httptest.Server
	writeMeta := func(t *testing.T, w http.ResponseWriter) {
		t.Helper()
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
			Issuer:                        issuerHost.URL,
			AuthorizationEndpoint:         mcpHost.URL + "/v1/authorize",
			TokenEndpoint:                 issuerHost.URL + "/v1/token",
			RegistrationEndpoint:          issuerHost.URL + "/v1/register",
			CodeChallengeMethodsSupported: []string{"plain", "S256"},
		}))
	}
	issuerHost = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		writeMeta(t, w)
	}))
	t.Cleanup(issuerHost.Close)
	mcpHost = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No protected resource metadata anywhere; AS metadata at the MCP
		// host declares the OTHER host as issuer (mismatch).
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		writeMeta(t, w)
	}))
	t.Cleanup(mcpHost.Close)

	manager, mockClient := setupTestOAuthManagerFull(t, nil, mcpHost.Client())
	mockClient.On("LogDebug", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()
	mockClient.On("LogWarn", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()

	resolved, err := manager.resolveOAuthConfig(context.Background(), mcpHost.URL+"/v1/mcp", "", &StaticOAuthCredentials{
		ClientID:     "static-client",
		ClientSecret: "static-secret",
	})
	require.NoError(t, err)
	require.Equal(t, mcpHost.URL+"/v1/authorize", resolved.config.Endpoint.AuthURL,
		"must use the advertised endpoint, not the conventional /authorize fallback")
	require.Equal(t, issuerHost.URL+"/v1/token", resolved.config.Endpoint.TokenURL)
	require.Equal(t, issuerHost.URL, resolved.issuer, "the declared issuer becomes the bound issuer")
}

// TestFetchAuthorizationServerMetadataIssuerFollowIsOneHop verifies the
// issuer-follow cannot chain: a declared issuer whose own metadata is again
// inconsistent must fail discovery rather than following further.
func TestFetchAuthorizationServerMetadataIssuerFollowIsOneHop(t *testing.T) {
	var hostA, hostB *httptest.Server
	// hostB declares yet another issuer (itself inconsistent).
	hostB = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
			Issuer:                        "https://elsewhere.example.com",
			AuthorizationEndpoint:         hostB.URL + "/authorize",
			TokenEndpoint:                 hostB.URL + "/token",
			CodeChallengeMethodsSupported: []string{"S256"},
		}))
	}))
	t.Cleanup(hostB.Close)
	hostA = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
			Issuer:                        hostB.URL,
			AuthorizationEndpoint:         hostA.URL + "/authorize",
			TokenEndpoint:                 hostA.URL + "/token",
			CodeChallengeMethodsSupported: []string{"S256"},
		}))
	}))
	t.Cleanup(hostA.Close)

	manager, mockClient := setupTestOAuthManagerFull(t, nil, hostA.Client())
	mockClient.On("LogDebug", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()

	_, err := manager.fetchAuthorizationServerMetadata(context.Background(), hostA.URL)
	require.Error(t, err, "a second issuer hop must not be followed")
}

// TestSelectTokenEndpointAuthMethod pins the DCR auth-method selection.
func TestSelectTokenEndpointAuthMethod(t *testing.T) {
	tests := []struct {
		name      string
		supported []string
		want      string
	}{
		{"empty defaults to basic", nil, "client_secret_basic"},
		{"basic preferred", []string{"none", "client_secret_post", "client_secret_basic"}, "client_secret_basic"},
		{"post when basic absent (Mattermost)", []string{"none", "client_secret_post"}, "client_secret_post"},
		{"none when only public", []string{"none"}, "none"},
		{"unsupported-only falls back to basic (fails clearly at registration)", []string{"private_key_jwt", "tls_client_auth"}, "client_secret_basic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, selectTokenEndpointAuthMethod(tt.supported))
		})
	}
}

// TestDCRHonorsServerAuthMethods reproduces the real Mattermost-server
// topology that failed in the field: the authorization server advertises only
// ["none","client_secret_post"] and rejects any other token_endpoint_auth_method
// at registration. Discovery must therefore register with client_secret_post,
// and that method must be persisted for later refreshes.
func TestDCRHonorsServerAuthMethods(t *testing.T) {
	var serverURL string
	var registeredMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
			// Mattermost serves its SPA (HTML) here, not RFC 9728 metadata.
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<!doctype html>"))
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			assert.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
				Issuer:                            serverURL,
				AuthorizationEndpoint:             serverURL + "/oauth/authorize",
				TokenEndpoint:                     serverURL + "/oauth/access_token",
				RegistrationEndpoint:              serverURL + "/register",
				CodeChallengeMethodsSupported:     []string{"S256"},
				ScopesSupported:                   []string{"user"},
				TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_post"},
			}))
		case "/register":
			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			registeredMethod, _ = body["token_endpoint_auth_method"].(string)
			// Reject anything the server doesn't support, like Mattermost does.
			if registeredMethod != "none" && registeredMethod != "client_secret_post" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":             "invalid_client_metadata",
					"error_description": "Unsupported token_endpoint_auth_method supplied.",
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"client_id":                  "mm-client",
				"client_secret":              "mm-secret",
				"token_endpoint_auth_method": registeredMethod,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	serverURL = server.URL

	manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
	mockClient.On("LogWarn", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()
	var stored []byte
	mockClient.On("KVGet", buildClientCredentialsKey(serverURL), mock.AnythingOfType("*mcp.ClientCredentials")).
		Run(func(args mock.Arguments) {
			if stored != nil {
				require.NoError(t, json.Unmarshal(stored, args.Get(1)))
			}
		}).Return(nil)
	mockClient.On("KVSet", buildClientCredentialsKey(serverURL), mock.Anything).
		Run(func(args mock.Arguments) { stored = args.Get(1).([]byte) }).Return(nil)

	resolved, err := manager.resolveOAuthConfig(context.Background(), serverURL+"/mcp", "", nil)
	require.NoError(t, err)
	require.Equal(t, "client_secret_post", registeredMethod,
		"must register with a method the server advertises, not the hardcoded basic")
	require.Equal(t, "mm-client", resolved.config.ClientID)
	require.Equal(t, []string{"user"}, resolved.config.Scopes, "scopes fall back to AS metadata")

	// The granted method is persisted so refresh uses it directly.
	require.NotNil(t, stored)
	var creds ClientCredentials
	require.NoError(t, json.Unmarshal(stored, &creds))
	require.Equal(t, "client_secret_post", creds.TokenEndpointAuthMethod)
}

// TestInferApplicationType pins the RFC 7591 application_type inference used
// in dynamic client registration.
func TestInferApplicationType(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"https://mm.example.com/plugins/mattermost-ai/oauth/callback", "web"},
		{"http://mm.example.com/callback", "web"},
		{"http://localhost:3333/callback", "native"},
		{"http://127.0.0.1:8065/callback", "native"},
		{"http://[::1]:8065/callback", "native"},
		{"myapp://callback", "native"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, inferApplicationType(tt.uri), tt.uri)
	}
}

// TestDCRSendsApplicationType verifies the registration request carries the
// inferred application_type (required by MCP 2026-07-28).
func TestDCRSendsApplicationType(t *testing.T) {
	var gotAppType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/register" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotAppType, _ = body["application_type"].(string)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "c", "client_secret": "s"})
	}))
	t.Cleanup(server.Close)

	manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.ClientCredentials")).Return(nil).Once()
	mockClient.On("KVSet", mock.AnythingOfType("string"), mock.Anything).Return(nil).Once()
	mockClient.On("LogDebug", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()
	// callbackURL defaults to http://test.com/callback (web) in the test helper.
	_, err := manager.loadOrCreateClientCredentials(context.Background(), server.URL, nil, server.URL+"/register", nil)
	require.NoError(t, err)
	require.Equal(t, "web", gotAppType)
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

// TestInitiateOAuthFlowFallsBackFromNonMatchingChallengeMetadata verifies that
// a 401 challenge naming protected resource metadata whose resource does not
// match the server URL (Google's Workspace MCP servers challenge with a
// per-tool document for ".../mcp" while serving ".../mcp/v1") falls back to the
// compliant well-known document instead of abandoning discovery, while a
// compliant challenge document is still preferred.
func TestInitiateOAuthFlowFallsBackFromNonMatchingChallengeMetadata(t *testing.T) {
	tests := []struct {
		name              string
		challengeResource string
		wantAuthEndpoint  string
	}{
		{name: "compliant challenge metadata is used", challengeResource: "/mcp/v1", wantAuthEndpoint: "/challenge-authorize"},
		{name: "non-matching challenge metadata falls back to the well-known document", challengeResource: "/mcp", wantAuthEndpoint: "/wellknown-authorize"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authServer := func(authorizePath string) *httptest.Server {
				var issuer string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/.well-known/oauth-authorization-server" {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					assert.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
						Issuer:                        issuer,
						AuthorizationEndpoint:         issuer + authorizePath,
						TokenEndpoint:                 issuer + "/token",
						CodeChallengeMethodsSupported: []string{"S256"},
					}))
				}))
				t.Cleanup(srv.Close)
				issuer = srv.URL
				return srv
			}
			challengeAS := authServer("/challenge-authorize")
			wellKnownAS := authServer("/wellknown-authorize")

			var origin string
			resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var prm oauthex.ProtectedResourceMetadata
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource/search_files":
					prm = oauthex.ProtectedResourceMetadata{Resource: origin + tt.challengeResource, AuthorizationServers: []string{challengeAS.URL}}
				case "/.well-known/oauth-protected-resource/mcp/v1":
					prm = oauthex.ProtectedResourceMetadata{Resource: origin + "/mcp/v1", AuthorizationServers: []string{wellKnownAS.URL}}
				default:
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(prm))
			}))
			t.Cleanup(resourceServer.Close)
			origin = resourceServer.URL
			serverURL := origin + "/mcp/v1"

			manager, mockClient := setupTestOAuthManagerFull(t, nil, http.DefaultClient)
			mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
			mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
			mockClient.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

			authURL, err := manager.InitiateOAuthFlowForServerWithMetadata(context.Background(), "user-id", ServerConfig{
				Name:         "Drive",
				BaseURL:      serverURL,
				ClientID:     "static-client",
				ClientSecret: "static-secret",
			}, origin+"/.well-known/oauth-protected-resource/search_files", "")
			require.NoError(t, err)

			parsed, err := url.Parse(authURL)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAuthEndpoint, parsed.Path)
			assert.Equal(t, serverURL, parsed.Query().Get("resource"))
		})
	}
}

// TestProtectedResourceMetadataWithQueryString covers the Atlassian v2 MCP
// topology: the configured server URL carries a query string (e.g.
// /v2/mcp?tools=all), PRM at /.well-known/oauth-protected-resource/v2/mcp
// advertises the query-less URL as its "resource" and points to a distinct
// authorization server, while the MCP origin itself serves a legacy v1 AS
// metadata document at /.well-known/oauth-authorization-server. Discovery must
// accept the query-less PRM resource, prefer an exact match when the server
// includes the query in "resource", and keep rejecting mismatched paths,
// hosts, ports, or queries.
func TestProtectedResourceMetadataWithQueryString(t *testing.T) {
	newAuthServer := func(t *testing.T, authPath string) *httptest.Server {
		t.Helper()
		var issuer string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/.well-known/oauth-authorization-server" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			assert.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
				Issuer:                        issuer,
				AuthorizationEndpoint:         issuer + authPath,
				TokenEndpoint:                 issuer + "/token",
				CodeChallengeMethodsSupported: []string{"S256"},
			}))
		}))
		t.Cleanup(srv.Close)
		issuer = srv.URL
		return srv
	}

	tests := []struct {
		name             string
		serverPathQuery  string
		challengePath    string
		prmResource      func(origin string) string
		wantPRMSuccess   bool
		wantResource     func(origin string) string
		wantAuthPath     string
		wantAuthOnV2Host bool
	}{
		{
			name:             "PRM resource omits query string (Atlassian v2)",
			serverPathQuery:  "/v2/mcp?tools=all",
			prmResource:      func(origin string) string { return origin + "/v2/mcp" },
			wantPRMSuccess:   true,
			wantResource:     func(origin string) string { return origin + "/v2/mcp" },
			wantAuthPath:     "/v2/authorize",
			wantAuthOnV2Host: true,
		},
		{
			name:             "PRM resource omits query and fragment",
			serverPathQuery:  "/v2/mcp?tools=all#section",
			prmResource:      func(origin string) string { return origin + "/v2/mcp" },
			wantPRMSuccess:   true,
			wantResource:     func(origin string) string { return origin + "/v2/mcp" },
			wantAuthPath:     "/v2/authorize",
			wantAuthOnV2Host: true,
		},
		{
			name:             "PRM resource includes exact query string",
			serverPathQuery:  "/v2/mcp?tools=all",
			prmResource:      func(origin string) string { return origin + "/v2/mcp?tools=all" },
			wantPRMSuccess:   true,
			wantResource:     func(origin string) string { return origin + "/v2/mcp?tools=all" },
			wantAuthPath:     "/v2/authorize",
			wantAuthOnV2Host: true,
		},
		{
			name:             "challenge metadata URL with query-less PRM resource",
			serverPathQuery:  "/v2/mcp?tools=all",
			challengePath:    "/.well-known/oauth-protected-resource/custom-challenge",
			prmResource:      func(origin string) string { return origin + "/v2/mcp" },
			wantPRMSuccess:   true,
			wantResource:     func(origin string) string { return origin + "/v2/mcp" },
			wantAuthPath:     "/v2/authorize",
			wantAuthOnV2Host: true,
		},
		{
			name:            "negative: PRM resource with different path is rejected",
			serverPathQuery: "/v2/mcp?tools=all",
			prmResource:     func(origin string) string { return origin + "/v1/mcp" },
			wantPRMSuccess:  false,
			wantAuthPath:    "/v1/authorize",
		},
		{
			name:            "negative: PRM resource with trailing slash mismatch is rejected",
			serverPathQuery: "/v2/mcp?tools=all",
			prmResource:     func(origin string) string { return origin + "/v2/mcp/" },
			wantPRMSuccess:  false,
			wantAuthPath:    "/v1/authorize",
		},
		{
			name:            "negative: PRM resource with different host is rejected",
			serverPathQuery: "/v2/mcp?tools=all",
			prmResource:     func(_ string) string { return "https://evil.example.com/v2/mcp" },
			wantPRMSuccess:  false,
			wantAuthPath:    "/v1/authorize",
		},
		{
			name:            "negative: PRM resource with different port is rejected",
			serverPathQuery: "/v2/mcp?tools=all",
			prmResource:     func(_ string) string { return "http://127.0.0.1:1/v2/mcp" },
			wantPRMSuccess:  false,
			wantAuthPath:    "/v1/authorize",
		},
		{
			name:            "negative: PRM resource with different query is rejected",
			serverPathQuery: "/v2/mcp?tools=all",
			prmResource:     func(origin string) string { return origin + "/v2/mcp?tools=readonly" },
			wantPRMSuccess:  false,
			wantAuthPath:    "/v1/authorize",
		},
		{
			name:            "negative: query-less server URL rejects PRM resource that adds a query",
			serverPathQuery: "/v2/mcp",
			prmResource:     func(origin string) string { return origin + "/v2/mcp?tools=all" },
			wantPRMSuccess:  false,
			wantAuthPath:    "/v1/authorize",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v2AS := newAuthServer(t, "/v2/authorize")

			var origin string
			resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource/v2/mcp", "/.well-known/oauth-protected-resource/custom-challenge":
					w.Header().Set("Content-Type", "application/json")
					assert.NoError(t, json.NewEncoder(w).Encode(oauthex.ProtectedResourceMetadata{
						Resource:             tt.prmResource(origin),
						AuthorizationServers: []string{v2AS.URL},
					}))
				case "/.well-known/oauth-authorization-server":
					// Fallback legacy v1 AS metadata served on the resource origin.
					w.Header().Set("Content-Type", "application/json")
					assert.NoError(t, json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
						Issuer:                        origin,
						AuthorizationEndpoint:         origin + "/v1/authorize",
						TokenEndpoint:                 origin + "/v1/token",
						CodeChallengeMethodsSupported: []string{"S256"},
					}))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(resourceServer.Close)
			origin = resourceServer.URL

			serverURL := origin + tt.serverPathQuery
			metadataURL := ""
			if tt.challengePath != "" {
				metadataURL = origin + tt.challengePath
			}

			manager, _ := newStatefulKVManager(t, nil, http.DefaultClient)

			prm, prmErr := manager.fetchProtectedResourceMetadata(context.Background(), serverURL, metadataURL)
			if tt.wantPRMSuccess {
				require.NoError(t, prmErr)
				require.Equal(t, tt.wantResource(origin), prm.Resource)
				require.Equal(t, []string{v2AS.URL}, prm.AuthorizationServers)
			} else {
				require.Error(t, prmErr)
			}

			authURL, err := manager.InitiateOAuthFlowForServerWithMetadata(context.Background(), "user-1", ServerConfig{
				ID:           "atlassian-id",
				Name:         "Atlassian",
				BaseURL:      serverURL,
				ClientID:     "static-client",
				ClientSecret: "static-secret",
			}, metadataURL, "")
			require.NoError(t, err)

			parsedAuthURL, err := url.Parse(authURL)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAuthPath, parsedAuthURL.Path)
			if tt.wantAuthOnV2Host {
				v2Parsed, parseErr := url.Parse(v2AS.URL)
				require.NoError(t, parseErr)
				assert.Equal(t, v2Parsed.Host, parsedAuthURL.Host, "authorization URL must target the PRM authorization server")
				assert.Equal(t, tt.wantResource(origin), parsedAuthURL.Query().Get("resource"))
			} else {
				originParsed, parseErr := url.Parse(origin)
				require.NoError(t, parseErr)
				assert.Equal(t, originParsed.Host, parsedAuthURL.Host, "rejected PRM must fall back to the origin AS")
			}
		})
	}
}

// TestOAuthFullFlowWithQueryStringServerURL verifies the complete OAuth + MCP
// flow when the configured server URL carries ?tools=all while PRM advertises
// the query-less URL as its resource and points to a separate v2 authorization
// server (with a legacy v1 AS at the resource origin). It confirms that:
//   - authorization, code exchange, stored grant, and refresh all use the
//     query-less PRM resource and the PRM authorization server;
//   - MCP HTTP requests themselves still carry ?tools=all.
func TestOAuthFullFlowWithQueryStringServerURL(t *testing.T) {
	const userID = "query-flow-user"
	const serverName = "Atlassian"
	const callbackURL = "http://localhost:3333/plugins/mattermost-ai/oauth/callback"

	ctx := context.Background()
	oauthServer := newFullFlowOAuthServer(t)

	// Separate resource server origin that serves v2 PRM pointing at oauthServer
	// (the v2 AS), plus a decoy v1 AS metadata document on its own origin so any
	// PRM failure falls back to v1 and fails the test.
	var mcpOrigin string
	var observedMCPQueries []string
	var queryMu sync.Mutex

	v2MCP := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "atlassian-v2", Version: "1.0"}, nil)
	type pingIn struct{}
	type pingOut struct {
		OK bool `json:"ok"`
	}
	sdkmcp.AddTool(v2MCP, &sdkmcp.Tool{Name: "getJiraIssue", Description: "Fetch Jira issue"},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, _ pingIn) (*sdkmcp.CallToolResult, pingOut, error) {
			return nil, pingOut{OK: true}, nil
		})
	streamable := sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return v2MCP },
		&sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)

	var v2ResourceURL string
	resourceMux := http.NewServeMux()
	resourceServer := httptest.NewServer(resourceMux)
	t.Cleanup(resourceServer.Close)
	mcpOrigin = resourceServer.URL
	v2ResourceURL = mcpOrigin + "/v2/mcp"

	// Override the fake AS's expected RFC 8707 resource to the query-less v2 URL.
	oauthServer.mu.Lock()
	oauthServer.expectedResource = v2ResourceURL
	oauthServer.mu.Unlock()

	verifier := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		oauthServer.mu.Lock()
		defer oauthServer.mu.Unlock()
		expiry, ok := oauthServer.accessTokens[token]
		if !ok {
			return nil, auth.ErrInvalidToken
		}
		oauthServer.lastAuthorizedToken = token
		return &auth.TokenInfo{Expiration: expiry, Extra: map[string]any{"token": token}}, nil
	}

	resourceMux.Handle("/v2/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryMu.Lock()
		observedMCPQueries = append(observedMCPQueries, r.URL.RawQuery)
		queryMu.Unlock()
		auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
			ResourceMetadataURL: mcpOrigin + "/.well-known/oauth-protected-resource/v2/mcp",
		})(streamable).ServeHTTP(w, r)
	}))
	resourceMux.HandleFunc("/.well-known/oauth-protected-resource/v2/mcp", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              v2ResourceURL,
			"authorization_servers": []string{oauthServer.baseURL},
		})
	})
	resourceMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
			Issuer:                        mcpOrigin,
			AuthorizationEndpoint:         mcpOrigin + "/v1/authorize",
			TokenEndpoint:                 mcpOrigin + "/v1/token",
			RegistrationEndpoint:          mcpOrigin + "/v1/register",
			CodeChallengeMethodsSupported: []string{"S256"},
		})
	})

	serverConfig := ServerConfig{
		ID:      "atlassian-srv-id",
		Name:    serverName,
		Enabled: true,
		BaseURL: v2ResourceURL + "?tools=all",
	}

	kv := newStatefulKVClient(t)
	httpClient := &http.Client{Transport: http.DefaultTransport}
	manager := NewOAuthManager(kv.mockClient(t), callbackURL, httpClient, func(id string) (ServerConfig, bool) {
		if id == serverName {
			return serverConfig, true
		}
		return ServerConfig{}, false
	})

	// Step 1: unauthenticated connect challenges with the v2 PRM URL.
	_, err := NewClient(ctx, userID, serverConfig, newTestLogService(), manager, httpClient, nil, false)
	require.Error(t, err)
	var oauthNeeded *OAuthNeededError
	require.ErrorAs(t, err, &oauthNeeded)

	// Step 2: initiate OAuth — must target the v2 AS (oauthServer), NOT the v1 fallback on mcpOrigin.
	authURL, err := manager.InitiateOAuthFlowForServerWithMetadata(ctx, userID, serverConfig, oauthNeeded.MetadataURL(), "")
	require.NoError(t, err)
	parsedAuth, err := url.Parse(authURL)
	require.NoError(t, err)
	v2ASURL, err := url.Parse(oauthServer.baseURL)
	require.NoError(t, err)
	require.Equal(t, v2ASURL.Host, parsedAuth.Host, "auth URL must point at PRM authorization server")
	require.Equal(t, "/authorize", parsedAuth.Path)
	require.Equal(t, v2ResourceURL, parsedAuth.Query().Get("resource"), "auth URL must carry query-less PRM resource")

	// Step 3: complete consent redirect and callback exchange.
	noRedirect := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noRedirect.Get(authURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	loc, err := resp.Location()
	require.NoError(t, err)

	_, err = manager.ProcessCallback(ctx, userID, loc.Query().Get("state"), loc.Query().Get("code"), loc.Query().Get("iss"))
	require.NoError(t, err)

	storedEnv := kv.storedEnvelope(t, userID, serverName)
	require.NotNil(t, storedEnv)
	require.Equal(t, v2ResourceURL, storedEnv.Resource, "stored grant Resource must be the query-less PRM resource")

	// Step 4: authenticated connect and tool call hit the full URL with ?tools=all.
	client, err := NewClient(ctx, userID, serverConfig, newTestLogService(), manager, httpClient, nil, false)
	require.NoError(t, err)
	result, err := client.CallTool(ctx, "getJiraIssue", map[string]any{})
	require.NoError(t, err)
	require.Contains(t, result, "true")
	require.NoError(t, client.Close())

	// Step 5: expire the access token and reconnect; refresh must send the
	// query-less PRM resource and preserve ?tools=all on MCP traffic.
	expired := *kv.storedToken(t, userID, serverName)
	expired.Expiry = time.Now().Add(-time.Hour)
	kv.overwriteToken(t, userID, serverName, &expired)

	client, err = NewClient(ctx, userID, serverConfig, newTestLogService(), manager, httpClient, nil, false)
	require.NoError(t, err)
	result, err = client.CallTool(ctx, "getJiraIssue", map[string]any{})
	require.NoError(t, err)
	require.Contains(t, result, "true")
	require.NoError(t, client.Close())

	_, refreshCalls, _, lastToken := oauthServer.counters()
	require.Equal(t, 1, refreshCalls)
	require.Equal(t, "access-token-2", lastToken)
	require.Equal(t, v2ResourceURL, kv.storedEnvelope(t, userID, serverName).Resource)

	queryMu.Lock()
	require.NotEmpty(t, observedMCPQueries)
	for _, q := range observedMCPQueries {
		require.Equal(t, "tools=all", q, "MCP traffic must carry the full configured query string")
	}
	queryMu.Unlock()
}
