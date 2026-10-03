// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// revocationProbe records what the fake revocation endpoint received.
type revocationProbe struct {
	hit          atomic.Bool
	token        atomic.Value // string
	hint         atomic.Value // string
	clientID     atomic.Value // string
	clientSecret atomic.Value // string
	authHeader   atomic.Value // string
	status       int
}

func newRevocationServer(t *testing.T, probe *revocationProbe) *httptest.Server {
	t.Helper()
	if probe.status == 0 {
		probe.status = http.StatusOK
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		probe.hit.Store(true)
		probe.token.Store(r.Form.Get("token"))
		probe.hint.Store(r.Form.Get("token_type_hint"))
		probe.clientID.Store(r.Form.Get("client_id"))
		probe.clientSecret.Store(r.Form.Get("client_secret"))
		probe.authHeader.Store(r.Header.Get("Authorization"))
		w.WriteHeader(probe.status)
	}))
}

func revocationEnvelope(revocationURL, authMethod string) *storedTokenEnvelope {
	return &storedTokenEnvelope{
		Version:            tokenEnvelopeVersion,
		Token:              &oauth2.Token{AccessToken: "access", RefreshToken: "refresh-token-value"},
		Issuer:             "https://issuer.example.com",
		TokenEndpoint:      "https://issuer.example.com/token",
		AuthServerURL:      "https://issuer.example.com",
		ClientID:           "client-abc",
		AuthMethod:         authMethod,
		Resource:           "https://mcp.example.com/mcp",
		RevocationEndpoint: revocationURL,
	}
}

// TestDeleteUserTokenRevokesGrant verifies disconnect revokes the refresh
// token at the authorization server using the grant's pinned client-auth
// method, then deletes the local grant.
func TestDeleteUserTokenRevokesGrant(t *testing.T) {
	const userID, serverID = "user-1", "srv"

	tests := []struct {
		name       string
		authMethod string
		secret     string
		wantBasic  bool
		wantPost   bool
	}{
		{name: "public client (none)", authMethod: authMethodNone},
		{name: "client_secret_post", authMethod: authMethodPost, secret: "shhh", wantPost: true},
		{name: "client_secret_basic", authMethod: authMethodBasic, secret: "shhh", wantBasic: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := &revocationProbe{}
			revSrv := newRevocationServer(t, probe)
			defer revSrv.Close()

			manager, kv := newStatefulKVManager(t, nil, revSrv.Client())
			env := revocationEnvelope(revSrv.URL+"/revoke", tt.authMethod)
			kv.putEnvelope(t, userID, serverID, env)

			// Seed matching client credentials so a confidential-client
			// revocation can authenticate.
			if tt.secret != "" {
				require.NoError(t, manager.storeClientCredentials(&ClientCredentials{
					ClientID:                env.ClientID,
					ClientSecret:            tt.secret,
					ServerURL:               env.AuthServerURL,
					TokenEndpointAuthMethod: tt.authMethod,
				}))
			}

			require.NoError(t, manager.DeleteUserToken(context.Background(), userID, serverID))

			require.True(t, probe.hit.Load(), "revocation endpoint must be called")
			require.Equal(t, "refresh-token-value", probe.token.Load(), "the refresh token must be revoked")
			require.Equal(t, "refresh_token", probe.hint.Load())
			require.False(t, kv.exists(userID, serverID), "local grant must be deleted after revocation")

			switch {
			case tt.wantBasic:
				require.NotEmpty(t, probe.authHeader.Load(), "basic auth must use the Authorization header")
				require.Empty(t, probe.clientSecret.Load(), "basic auth must not put the secret in the form")
			case tt.wantPost:
				require.Equal(t, env.ClientID, probe.clientID.Load())
				require.Equal(t, "shhh", probe.clientSecret.Load())
			default: // public
				require.Equal(t, env.ClientID, probe.clientID.Load())
				require.Empty(t, probe.clientSecret.Load())
				require.Empty(t, probe.authHeader.Load())
			}
		})
	}
}

// TestDeleteUserTokenRevocationFailureStillDeletes verifies a failed
// revocation never blocks the local disconnect.
func TestDeleteUserTokenRevocationFailureStillDeletes(t *testing.T) {
	const userID, serverID = "user-1", "srv"

	probe := &revocationProbe{status: http.StatusInternalServerError}
	revSrv := newRevocationServer(t, probe)
	defer revSrv.Close()

	manager, kv := newStatefulKVManager(t, nil, revSrv.Client())
	kv.putEnvelope(t, userID, serverID, revocationEnvelope(revSrv.URL+"/revoke", authMethodNone))

	require.NoError(t, manager.DeleteUserToken(context.Background(), userID, serverID),
		"local delete must succeed even when revocation fails")
	require.True(t, probe.hit.Load())
	require.False(t, kv.exists(userID, serverID), "grant must be deleted despite revocation failure")
}

// TestDeleteUserTokenNoRevocationEndpointSkips verifies grants without a pinned
// revocation endpoint (e.g. stored before the field existed, or an AS that
// advertises none) are simply deleted locally with no HTTP call.
func TestDeleteUserTokenNoRevocationEndpointSkips(t *testing.T) {
	const userID, serverID = "user-1", "srv"

	probe := &revocationProbe{}
	revSrv := newRevocationServer(t, probe)
	defer revSrv.Close()

	manager, kv := newStatefulKVManager(t, nil, revSrv.Client())
	env := revocationEnvelope("", authMethodNone) // no revocation endpoint
	kv.putEnvelope(t, userID, serverID, env)

	require.NoError(t, manager.DeleteUserToken(context.Background(), userID, serverID))
	require.False(t, probe.hit.Load(), "no revocation request without a pinned endpoint")
	require.False(t, kv.exists(userID, serverID))
}

// TestRevokeGrantRefusesNonLoopbackHTTPEndpoint verifies a server-supplied
// revocation endpoint with an unsafe scheme is refused before any token is
// sent.
func TestRevokeGrantRefusesNonLoopbackHTTPEndpoint(t *testing.T) {
	manager, _ := newStatefulKVManager(t, nil, http.DefaultClient)
	env := revocationEnvelope("http://evil.example.com/revoke", authMethodNone)

	err := manager.revokeGrant(context.Background(), env, &ClientCredentials{ClientID: env.ClientID})
	require.Error(t, err)
	require.Contains(t, err.Error(), "refusing to use revocation endpoint")
}

// TestRefreshRefusesTokenEndpointRedirect verifies the refresh never follows a
// redirect away from the pinned token endpoint (which would replay the refresh
// token and client secret to the redirect target).
func TestRefreshRefusesTokenEndpointRedirect(t *testing.T) {
	var otherHit atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHit.Store(true)
		_, _ = w.Write([]byte(`{"access_token":"leaked","token_type":"Bearer"}`))
	}))
	defer other.Close()

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer tokenSrv.Close()

	manager, _ := newStatefulKVManager(t, nil, tokenSrv.Client())
	envelope := &storedTokenEnvelope{
		Version:       tokenEnvelopeVersion,
		Token:         &oauth2.Token{RefreshToken: "refresh"},
		TokenEndpoint: tokenSrv.URL + "/token",
		AuthMethod:    authMethodNone,
		Resource:      "https://mcp.example.com/mcp",
	}
	creds := &ClientCredentials{ClientID: "client-abc", TokenEndpointAuthMethod: authMethodNone}

	_, _, err := manager.refreshGrant(context.Background(), envelope, creds)
	require.Error(t, err, "a redirecting token endpoint must fail the refresh")
	require.False(t, otherHit.Load(), "refresh must not replay credentials to the redirect target")
}
