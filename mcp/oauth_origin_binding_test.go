// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// A saved sign-in is used only while the server under its name keeps the
// origin it was issued for, whether or not the sweep that removes stale
// sign-ins ran. Sign-ins saved before the origin was recorded are bound to
// the current origin, so users stay signed in across the upgrade.
func TestStoredGrantIsUsedOnlyWithItsServerOrigin(t *testing.T) {
	const userID = "user123"
	const serverName = "Jira"
	bound := func(origin string) *storedTokenEnvelope {
		return &storedTokenEnvelope{
			Version:       tokenEnvelopeVersion,
			Token:         &oauth2.Token{AccessToken: "access", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)},
			TokenEndpoint: "https://auth.example.com/token",
			ClientID:      "client",
			ServerOrigin:  origin,
		}
	}

	tests := []struct {
		name       string
		stored     *storedTokenEnvelope
		legacy     bool
		serverURL  string
		wantUsed   bool
		wantOrigin string
	}{
		{name: "same origin", stored: bound("https://jira.example.com:443"), serverURL: "https://jira.example.com/mcp", wantUsed: true, wantOrigin: "https://jira.example.com:443"},
		{name: "path-only change", stored: bound("https://jira.example.com:443"), serverURL: "https://jira.example.com/v2/mcp", wantUsed: true, wantOrigin: "https://jira.example.com:443"},
		{name: "server moved to another host", stored: bound("https://jira.example.com:443"), serverURL: "https://other.example.com/mcp", wantOrigin: "https://jira.example.com:443"},
		{name: "server moved to another port", stored: bound("https://jira.example.com:443"), serverURL: "https://jira.example.com:8443/mcp", wantOrigin: "https://jira.example.com:443"},
		{name: "grant saved before origins were recorded is bound to the current origin", stored: bound(""), serverURL: "https://jira.example.com/mcp", wantUsed: true, wantOrigin: "https://jira.example.com:443"},
		{name: "pre-envelope grant is used as before", legacy: true, serverURL: "https://jira.example.com/mcp", wantUsed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, kv := newStatefulKVManager(t, nil, &http.Client{})
			if tt.legacy {
				kv.putLegacyToken(t, userID, serverName, &oauth2.Token{AccessToken: "access", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)})
			} else {
				kv.putEnvelope(t, userID, serverName, tt.stored)
			}
			server := ServerConfig{Name: serverName, BaseURL: tt.serverURL}

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
			if !tt.legacy {
				assert.Equal(t, tt.wantOrigin, kv.storedEnvelope(t, userID, serverName).ServerOrigin)
			}
		})
	}
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
