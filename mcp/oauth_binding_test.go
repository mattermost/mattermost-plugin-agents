// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// mutableAuthServer is an httptest authorization server whose advertised
// metadata can be swapped between flow initiation and callback, simulating a
// compromised MCP server trying to redirect the code exchange.
type mutableAuthServer struct {
	mu      sync.Mutex
	baseURL string
	// tokenPathAdvertised is what the metadata currently advertises.
	tokenPathAdvertised string
	// exchangesByPath records which token endpoint paths received exchanges.
	exchangesByPath map[string]int
}

func (s *mutableAuthServer) swapTokenEndpoint(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenPathAdvertised = path
}

func (s *mutableAuthServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		advertised := s.tokenPathAdvertised
		s.mu.Unlock()

		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           s.baseURL,
				"authorization_endpoint":           s.baseURL + "/authorize",
				"token_endpoint":                   s.baseURL + advertised,
				"code_challenge_methods_supported": []string{"S256"},
			}))
		case "/token-original", "/token-attacker":
			s.mu.Lock()
			s.exchangesByPath[r.URL.Path]++
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-from-" + r.URL.Path,
				"token_type":   "Bearer",
				"expires_in":   3600,
			}))
		default:
			http.NotFound(w, r)
		}
	}
}

// TestProcessCallbackUsesSessionBoundEndpoints verifies the session binding:
// the code exchange goes to the token endpoint discovered at initiation time
// even when the server swaps its advertised metadata mid-flow.
func TestProcessCallbackUsesSessionBoundEndpoints(t *testing.T) {
	const userID = "user123"
	const serverID = "binding-server"

	authServer := &mutableAuthServer{
		tokenPathAdvertised: "/token-original",
		exchangesByPath:     map[string]int{},
	}
	server := httptest.NewServer(authServer.handler(t))
	t.Cleanup(server.Close)
	authServer.baseURL = server.URL

	// The lookup lets ProcessCallback re-derive the static credentials, like
	// production does from the live plugin config.
	lookup := func(id string) (ServerConfig, bool) {
		if id != serverID {
			return ServerConfig{}, false
		}
		return ServerConfig{
			Name:         serverID,
			BaseURL:      authServer.baseURL,
			ClientID:     "static-client",
			ClientSecret: "static-secret",
		}, true
	}
	manager, mockClient := setupTestOAuthManagerFull(t, lookup, server.Client())
	mockClient.On("LogDebug", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()
	// Trust-on-first-use issuer pin for the static credentials.
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.staticIssuerPin")).
		Return(mmapi.ErrKVNotFound)
	mockClient.On("KVCompareAndSet", mock.AnythingOfType("string"), nil, mock.AnythingOfType("*mcp.staticIssuerPin")).
		Return(true, nil)

	var storedSession *OAuthSession
	mockClient.On("KVSetWithExpiry", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession"), mock.Anything).
		Run(func(args mock.Arguments) { storedSession = args.Get(1).(*OAuthSession) }).
		Return(nil)
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession")).
		Run(func(args mock.Arguments) { *(args.Get(1).(*OAuthSession)) = *storedSession }).
		Return(nil)
	mockClient.On("KVDelete", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("KVSet", buildTokenKey(userID, serverID), mock.AnythingOfType("*mcp.storedTokenEnvelope")).Return(nil)

	_, err := manager.InitiateOAuthFlow(context.Background(), userID, serverID, server.URL, "", "", &StaticOAuthCredentials{
		ClientID:     "static-client",
		ClientSecret: "static-secret",
	})
	require.NoError(t, err)
	require.NotNil(t, storedSession)
	require.Equal(t, server.URL+"/token-original", storedSession.TokenEndpoint,
		"session must be bound to the token endpoint discovered at initiation")

	// The server swaps its advertised metadata before the callback arrives.
	authServer.swapTokenEndpoint("/token-attacker")

	_, err = manager.ProcessCallback(context.Background(), userID, storedSession.State, "auth-code", "")
	require.NoError(t, err)
	require.Equal(t, 1, authServer.exchangesByPath["/token-original"],
		"the exchange must use the session-bound token endpoint")
	require.Zero(t, authServer.exchangesByPath["/token-attacker"],
		"the swapped-in endpoint must never receive the code")
}

// TestDCRResponseBodyIsSizeLimited verifies that a hostile registration
// endpoint streaming an oversized body cannot exhaust memory: the response is
// capped, so registration fails cleanly rather than reading unbounded.
func TestDCRResponseBodyIsSizeLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/register" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		// Stream far more than the cap; valid JSON prefix then endless filler.
		_, _ = w.Write([]byte(`{"client_id":"c","padding":"`))
		chunk := bytes.Repeat([]byte("A"), 64*1024)
		for i := 0; i < (maxOAuthResponseBytes/len(chunk))+8; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.ClientCredentials")).Return(nil).Once()

	_, err := manager.loadOrCreateClientCredentials(context.Background(), server.URL, nil, server.URL+"/register", nil)
	// The truncated body is invalid JSON, so registration fails — crucially
	// without reading the unbounded stream into memory.
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to register OAuth client")
}

// TestRefreshSerializedByLease verifies that concurrent refreshes of the same
// grant are serialized by the per-grant lease: the token endpoint is hit
// exactly once and every caller ends up with the rotated token.
func TestRefreshSerializedByLease(t *testing.T) {
	const userID = "user123"
	const serverID = "lease-server"

	var refreshCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		refreshCalls.Add(1)
		time.Sleep(150 * time.Millisecond) // widen the concurrency window
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "rotated-access",
			"refresh_token": "rotated-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(srv.Close)

	manager, kv := newStatefulKVManager(t, nil, srv.Client())
	kv.putEnvelope(t, userID, serverID, &storedTokenEnvelope{
		Version:       tokenEnvelopeVersion,
		Token:         &oauth2.Token{AccessToken: "old", RefreshToken: "old-refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Hour)},
		Issuer:        srv.URL,
		TokenEndpoint: srv.URL + "/token",
		AuthServerURL: srv.URL,
		ClientID:      "static-client",
	})

	newSource := func() oauth2.TokenSource {
		h := newUserOAuthHandler(userID, ServerConfig{
			Name: serverID, BaseURL: srv.URL, ClientID: "static-client", ClientSecret: "static-secret",
		}, manager)
		ts, err := h.TokenSource(context.Background())
		require.NoError(t, err)
		return ts
	}

	const goroutines = 5
	var wg sync.WaitGroup
	results := make([]string, goroutines)
	errs := make([]error, goroutines)
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tok, err := newSource().Token()
			if err != nil {
				errs[idx] = err
				return
			}
			results[idx] = tok.AccessToken
		}(i)
	}
	wg.Wait()

	require.Equal(t, int32(1), refreshCalls.Load(), "the lease must serialize the refresh to a single token-endpoint call")
	for i := range goroutines {
		require.NoError(t, errs[i])
		require.Equal(t, "rotated-access", results[i])
	}
}

// TestProcessCallbackRejectsUnboundSession verifies that sessions written
// before the security-binding fields existed (an in-flight flow across a
// plugin upgrade) fail closed instead of re-running discovery.
func TestProcessCallbackRejectsUnboundSession(t *testing.T) {
	manager, mockClient := setupTestOAuthManagerFull(t, nil, &http.Client{})

	unbound := &OAuthSession{
		UserID:       "user123",
		ServerID:     "server456",
		ServerURL:    "https://api.example.com",
		CodeVerifier: "test-verifier",
		State:        "test-state",
		CreatedAt:    time.Now(),
		// No Issuer/TokenEndpoint/ClientID: pre-upgrade layout.
	}
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession")).
		Run(func(args mock.Arguments) { *(args.Get(1).(*OAuthSession)) = *unbound }).
		Return(nil)
	mockClient.On("KVDelete", mock.AnythingOfType("string")).Return(nil)

	_, err := manager.ProcessCallback(context.Background(), "user123", "test-state", "auth-code", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "restart authorization")
}

// TestProcessCallbackRejectsNormalizedIss pins the RFC 9207 comparison to
// exact string equality: the MCP specification forbids normalization, so even
// a trailing-slash variant of the bound issuer must be rejected.
func TestProcessCallbackRejectsNormalizedIss(t *testing.T) {
	manager, mockClient := setupTestOAuthManagerFull(t, nil, &http.Client{})

	bound := &OAuthSession{
		UserID:        "user123",
		ServerID:      "server456",
		ServerURL:     "https://api.example.com",
		CodeVerifier:  "test-verifier",
		State:         "test-state",
		CreatedAt:     time.Now(),
		Issuer:        "https://as.example.com",
		AuthServerURL: "https://as.example.com",
		TokenEndpoint: "https://as.example.com/token",
		ClientID:      "client-1",
	}
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession")).
		Run(func(args mock.Arguments) { *(args.Get(1).(*OAuthSession)) = *bound }).
		Return(nil)
	mockClient.On("KVDelete", mock.AnythingOfType("string")).Return(nil)

	_, err := manager.ProcessCallback(context.Background(), "user123", "test-state", "auth-code", bound.Issuer+"/")
	require.Error(t, err)
	require.Contains(t, err.Error(), "issuer mismatch")
}

// TestInitiateOAuthFlowStaticIssuerPinMismatch verifies trust-on-first-use
// issuer pinning for static credentials: once pinned, a flow resolving a
// different authorization server must fail before the secret can be sent
// anywhere.
func TestInitiateOAuthFlowStaticIssuerPinMismatch(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           serverURL,
			"authorization_endpoint":           serverURL + "/authorize",
			"token_endpoint":                   serverURL + "/token",
			"code_challenge_methods_supported": []string{"S256"},
		}))
	}))
	t.Cleanup(server.Close)
	serverURL = server.URL

	manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
	// The pin was created when the credentials were first used with a
	// different authorization server.
	mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.staticIssuerPin")).
		Run(func(args mock.Arguments) {
			*(args.Get(1).(*staticIssuerPin)) = staticIssuerPin{Issuer: "https://original-as.example.com"}
		}).
		Return(nil)

	_, err := manager.InitiateOAuthFlow(context.Background(), "user123", "pinned-server", server.URL, "", "", &StaticOAuthCredentials{
		ClientID:     "static-client",
		ClientSecret: "static-secret",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match the issuer")
}

// TestDCRPublicClientCredentials verifies RFC 7591 public-client handling:
// a registration returning token_endpoint_auth_method "none" with no secret
// yields usable, persisted credentials that are reused instead of triggering
// a second registration.
func TestDCRPublicClientCredentials(t *testing.T) {
	var registerCalls int
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           serverURL,
				"authorization_endpoint":           serverURL + "/authorize",
				"token_endpoint":                   serverURL + "/token",
				"registration_endpoint":            serverURL + "/register",
				"code_challenge_methods_supported": []string{"S256"},
			}))
		case "/register":
			registerCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"client_id":                  "public-client-1",
				"token_endpoint_auth_method": "none",
				"redirect_uris":              []string{"http://localhost:3333/plugins/mattermost-ai/oauth/callback"},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	serverURL = server.URL

	manager, mockClient := setupTestOAuthManagerFull(t, nil, server.Client())
	mockClient.On("LogDebug", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()

	var storedCreds []byte
	mockClient.On("KVGet", buildClientCredentialsKey(serverURL), mock.AnythingOfType("*mcp.ClientCredentials")).
		Run(func(args mock.Arguments) {
			if storedCreds != nil {
				require.NoError(t, json.Unmarshal(storedCreds, args.Get(1)))
			}
		}).
		Return(nil)
	mockClient.On("KVSet", buildClientCredentialsKey(serverURL), mock.Anything).
		Run(func(args mock.Arguments) { storedCreds = args.Get(1).([]byte) }).
		Return(nil)

	first, err := manager.createOAuthConfig(context.Background(), serverURL, "", nil)
	require.NoError(t, err)
	require.Equal(t, "public-client-1", first.ClientID)
	require.Empty(t, first.ClientSecret, "public clients have no secret")
	require.Equal(t, 1, registerCalls)

	// A second resolution must reuse the stored public-client credentials
	// instead of re-registering (which would change the client_id and break
	// any in-flight exchange).
	second, err := manager.createOAuthConfig(context.Background(), serverURL, "", nil)
	require.NoError(t, err)
	require.Equal(t, "public-client-1", second.ClientID)
	require.Equal(t, 1, registerCalls, "stored public-client credentials must be reused, not re-registered")
}
