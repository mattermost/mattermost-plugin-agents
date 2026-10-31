// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// A saved sign-in is used only by the server it was issued for: one with the
// origin and, when both carry one, the configuration ID recorded in the
// grant, whether or not the sweep that removes stale sign-ins ran. Sign-ins
// saved before those were recorded are bound on first load, so users stay
// signed in across the upgrade.
func TestStoredGrantIsUsedOnlyByItsServer(t *testing.T) {
	const userID = "user123"
	const serverName = "Jira"
	grant := func(origin, resource, serverConfigID string) *storedTokenEnvelope {
		return &storedTokenEnvelope{
			Version:        tokenEnvelopeVersion,
			Token:          &oauth2.Token{AccessToken: "access", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)},
			TokenEndpoint:  "https://auth.example.com/token",
			ClientID:       "client",
			Resource:       resource,
			ServerOrigin:   origin,
			ServerConfigID: serverConfigID,
		}
	}
	const jira = "https://jira.example.com:443"

	tests := []struct {
		name       string
		stored     *storedTokenEnvelope
		server     ServerConfig
		wantUsed   bool
		wantOrigin string
		wantID     string
	}{
		{name: "same origin", stored: grant(jira, "", "id-1"), server: ServerConfig{ID: "id-1", BaseURL: "https://jira.example.com/mcp"}, wantUsed: true, wantOrigin: jira, wantID: "id-1"},
		{name: "path-only change", stored: grant(jira, "", "id-1"), server: ServerConfig{ID: "id-1", BaseURL: "https://jira.example.com/v2/mcp"}, wantUsed: true, wantOrigin: jira, wantID: "id-1"},
		{name: "server moved to another host", stored: grant(jira, "", "id-1"), server: ServerConfig{ID: "id-1", BaseURL: "https://other.example.com/mcp"}, wantOrigin: jira, wantID: "id-1"},
		{name: "server moved to another port", stored: grant(jira, "", "id-1"), server: ServerConfig{ID: "id-1", BaseURL: "https://jira.example.com:8443/mcp"}, wantOrigin: jira, wantID: "id-1"},
		{name: "another server with the same name and origin", stored: grant(jira, "", "id-1"), server: ServerConfig{ID: "id-2", BaseURL: "https://jira.example.com/other"}, wantOrigin: jira, wantID: "id-1"},
		{name: "server saved before IDs existed", stored: grant(jira, "", "id-1"), server: ServerConfig{BaseURL: "https://jira.example.com/mcp"}, wantUsed: true, wantOrigin: jira, wantID: "id-1"},
		{name: "legacy grant without resource is bound to the current server", stored: grant("", "", ""), server: ServerConfig{ID: "id-1", BaseURL: "https://jira.example.com/mcp"}, wantUsed: true, wantOrigin: jira, wantID: "id-1"},
		{name: "legacy grant is bound to the origin of its resource", stored: grant("", "https://jira.example.com/mcp", ""), server: ServerConfig{ID: "id-1", BaseURL: "https://jira.example.com/v2/mcp"}, wantUsed: true, wantOrigin: jira, wantID: "id-1"},
		{name: "legacy grant whose resource is the old origin is not used after a move", stored: grant("", "https://jira.example.com/mcp", ""), server: ServerConfig{ID: "id-1", BaseURL: "https://other.example.com/mcp"}},
		{name: "legacy grant without an ID is bound to the current ID", stored: grant(jira, "", ""), server: ServerConfig{ID: "id-2", BaseURL: "https://jira.example.com/mcp"}, wantUsed: true, wantOrigin: jira, wantID: "id-2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, kv := newStatefulKVManager(t, nil, &http.Client{})
			kv.putEnvelope(t, userID, serverName, tt.stored)
			server := tt.server
			server.Name = serverName

			source, err := newUserOAuthHandler(userID, server, manager).TokenSource(context.Background())
			require.NoError(t, err)
			hasToken, err := manager.HasStoredToken(userID, server)
			require.NoError(t, err)

			assert.Equal(t, tt.wantUsed, source != nil, "token source")
			assert.Equal(t, tt.wantUsed, hasToken, "stored token reported")
			if source != nil {
				token, err := source.Token()
				require.NoError(t, err)
				assert.Equal(t, "access", token.AccessToken)
			}
			stored := kv.storedEnvelope(t, userID, serverName)
			assert.Equal(t, tt.wantOrigin, stored.ServerOrigin, "stored origin")
			assert.Equal(t, tt.wantID, stored.ServerConfigID, "stored server configuration ID")
		})
	}
}

// Reloading the stored grant while waiting for another node's refresh only
// adopts a grant issued for this server.
func TestAdoptLatestGrantRejectsAnotherServersGrant(t *testing.T) {
	const userID = "user123"
	const serverName = "Jira"
	server := ServerConfig{ID: "id-1", Name: serverName, BaseURL: "https://jira.example.com/mcp"}

	tests := []struct {
		name     string
		origin   string
		configID string
		wantUsed bool
	}{
		{name: "grant for this server", origin: "https://jira.example.com:443", configID: "id-1", wantUsed: true},
		{name: "grant for another origin", origin: "https://other.example.com:443", configID: "id-1"},
		{name: "grant for another server configuration", origin: "https://jira.example.com:443", configID: "id-2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, kv := newStatefulKVManager(t, nil, &http.Client{})
			kv.putEnvelope(t, userID, serverName, &storedTokenEnvelope{
				Version:        tokenEnvelopeVersion,
				Token:          &oauth2.Token{AccessToken: "latest", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)},
				TokenEndpoint:  "https://auth.example.com/token",
				ClientID:       "client",
				ServerOrigin:   tt.origin,
				ServerConfigID: tt.configID,
			})
			source := &persistingTokenSource{
				ctx:        context.Background(),
				envelope:   &storedTokenEnvelope{Version: tokenEnvelopeVersion, Token: &oauth2.Token{AccessToken: "stale"}},
				manager:    manager,
				userID:     userID,
				serverName: serverName,
				server:     server,
			}

			adopted := source.adoptLatestGrant()

			if tt.wantUsed {
				require.NotNil(t, adopted)
				assert.Equal(t, "latest", adopted.AccessToken)
				return
			}
			assert.Nil(t, adopted)
			assert.Equal(t, "stale", source.envelope.Token.AccessToken)
		})
	}
}

// A failure to bind a legacy grant fails closed: the grant is not used.
func TestBindingLegacyGrantFailsClosed(t *testing.T) {
	const userID = "user123"
	const serverName = "Jira"
	raw, err := json.Marshal(&storedTokenEnvelope{
		Version:       tokenEnvelopeVersion,
		Token:         &oauth2.Token{AccessToken: "access", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)},
		TokenEndpoint: "https://auth.example.com/token",
		ClientID:      "client",
	})
	require.NoError(t, err)

	client := mocks.NewMockClient(t)
	client.On("KVGet", buildTokenKey(userID, serverName), mock.AnythingOfType("*[]uint8")).
		Run(func(args mock.Arguments) { *args.Get(1).(*[]byte) = raw }).
		Return(nil)
	client.On("KVCompareAndSet", buildTokenKey(userID, serverName), mock.Anything, mock.Anything).
		Return(false, errors.New("kv unavailable"))
	manager := NewOAuthManager(client, "http://test.com/callback", &http.Client{}, nil)
	server := ServerConfig{ID: "id-1", Name: serverName, BaseURL: "https://jira.example.com/mcp"}

	source, err := newUserOAuthHandler(userID, server, manager).TokenSource(context.Background())
	require.Error(t, err)
	assert.Nil(t, source)
	hasToken, err := manager.HasStoredToken(userID, server)
	require.Error(t, err)
	assert.False(t, hasToken)
}

// A real HTTP client for a server that moved to another origin never sends
// the grant issued for the old origin, even though the grant is still stored
// under the server's name.
func TestClientForMovedServerSendsNoOldGrant(t *testing.T) {
	const userID = "user123"
	const serverName = "Jira"

	var gotAuth atomic.Value
	gotAuth.Store("")
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(moved.Close)

	manager, kv := newStatefulKVManager(t, nil, moved.Client())
	kv.putEnvelope(t, userID, serverName, &storedTokenEnvelope{
		Version:       tokenEnvelopeVersion,
		Token:         &oauth2.Token{AccessToken: "issued-for-old-origin", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)},
		TokenEndpoint: "https://auth.example.com/token",
		ClientID:      "client",
		ServerOrigin:  "https://jira.example.com:443",
	})
	handler := newUserOAuthHandler(userID, ServerConfig{Name: serverName, BaseURL: moved.URL}, manager)

	c := &Client{httpClient: moved.Client()}
	httpClient := c.httpClientForLegacySSE(moved.URL, handler, nil)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, moved.URL, nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}

	assert.Empty(t, gotAuth.Load(), "the moved server must not receive the old origin's grant")
}

// A sign-in that completes after its server moved to another origin, while
// this node still ran the old configuration, saves a grant that is never
// used with the server's new origin.
func TestSignInCompletedAfterServerMovedIsNotUsed(t *testing.T) {
	const userID = "user123"
	const serverName = "Jira"
	const state = "state-1"

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token": "issued-for-old-server",
			"token_type":   "Bearer",
			"expires_in":   3600,
		}))
	}))
	t.Cleanup(tokenServer.Close)

	oldServer := ServerConfig{Name: serverName, BaseURL: "https://jira.example.com/mcp", ClientID: "client", ClientSecret: "secret"}
	// The lookup still returns the old configuration, as on a node that has
	// not reloaded yet.
	lookup := func(name string) (ServerConfig, bool) { return oldServer, name == serverName }
	manager, kv := newStatefulKVManager(t, lookup, tokenServer.Client())

	session := &OAuthSession{
		UserID:         userID,
		ServerID:       serverName,
		ServerURL:      oldServer.BaseURL,
		CodeVerifier:   "verifier",
		State:          state,
		StaticClientID: "client",
		CreatedAt:      time.Now(),
		Issuer:         tokenServer.URL,
		AuthServerURL:  tokenServer.URL,
		TokenEndpoint:  tokenServer.URL + "/token",
		ClientID:       "client",
	}
	raw, err := json.Marshal(session)
	require.NoError(t, err)
	kv.mu.Lock()
	kv.data[buildSessionKey(userID, state)] = raw
	kv.mu.Unlock()

	_, err = manager.ProcessCallback(context.Background(), userID, state, "code", "")
	require.NoError(t, err)
	require.Equal(t, "https://jira.example.com:443", kv.storedEnvelope(t, userID, serverName).ServerOrigin)

	movedServer := ServerConfig{Name: serverName, BaseURL: "https://other.example.com/mcp", ClientID: "client", ClientSecret: "secret"}
	source, err := newUserOAuthHandler(userID, movedServer, manager).TokenSource(context.Background())
	require.NoError(t, err)
	assert.Nil(t, source, "a grant issued for the old origin must not be sent to the new one")
	hasToken, err := manager.HasStoredToken(userID, movedServer)
	require.NoError(t, err)
	assert.False(t, hasToken)
}

// A sign-in started for one server is rejected, and stores nothing, when a
// different server (another configuration ID) holds its name at the same
// origin by the time the callback arrives; one that still matches is bound
// to its server's configuration ID.
func TestSignInCallbackIsBoundToItsServerConfiguration(t *testing.T) {
	const userID = "user123"
	const serverName = "Jira"
	const state = "state-1"

	tests := []struct {
		name     string
		liveID   string
		wantErr  bool
		wantBind string
	}{
		{name: "same server", liveID: "id-1", wantBind: "id-1"},
		{name: "another server under the same name and origin", liveID: "id-2", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"access_token": "issued", "token_type": "Bearer", "expires_in": 3600}))
			}))
			t.Cleanup(tokenServer.Close)

			live := ServerConfig{ID: tt.liveID, Name: serverName, BaseURL: "https://jira.example.com/mcp", ClientID: "client", ClientSecret: "secret"}
			lookup := func(name string) (ServerConfig, bool) { return live, name == serverName }
			manager, kv := newStatefulKVManager(t, lookup, tokenServer.Client())

			raw, err := json.Marshal(&OAuthSession{
				UserID:         userID,
				ServerID:       serverName,
				ServerURL:      "https://jira.example.com/mcp",
				ServerConfigID: "id-1",
				CodeVerifier:   "verifier",
				State:          state,
				StaticClientID: "client",
				CreatedAt:      time.Now(),
				Issuer:         tokenServer.URL,
				AuthServerURL:  tokenServer.URL,
				TokenEndpoint:  tokenServer.URL + "/token",
				ClientID:       "client",
			})
			require.NoError(t, err)
			kv.mu.Lock()
			kv.data[buildSessionKey(userID, state)] = raw
			kv.mu.Unlock()

			_, err = manager.ProcessCallback(context.Background(), userID, state, "code", "")

			if tt.wantErr {
				require.Error(t, err)
				assert.False(t, kv.exists(userID, serverName), "no grant may be stored for another server")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBind, kv.storedEnvelope(t, userID, serverName).ServerConfigID)
		})
	}
}
