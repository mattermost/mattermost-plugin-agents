// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestCanonicalOrigin(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "https default port", in: "https://example.com/mcp", want: "https://example.com:443"},
		{name: "https explicit 443", in: "https://example.com:443/mcp", want: "https://example.com:443"},
		{name: "https custom port", in: "https://example.com:8443/mcp", want: "https://example.com:8443"},
		{name: "http default port", in: "http://localhost/mcp", want: "http://localhost:80"},
		{name: "http explicit port", in: "http://127.0.0.1:8065/x", want: "http://127.0.0.1:8065"},
		{name: "different host differs", in: "https://evil.com/mcp", want: "https://evil.com:443"},
		{name: "no scheme", in: "example.com/mcp", want: ""},
		{name: "unknown scheme", in: "ftp://example.com", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, canonicalOriginFromString(tt.in))
		})
	}
}

func TestBlockCrossOriginRedirect(t *testing.T) {
	pinned := "https://server.example.com:443"
	check := blockCrossOriginRedirect(pinned)

	sameOrigin, err := url.Parse("https://server.example.com/other")
	require.NoError(t, err)
	require.NoError(t, check(&http.Request{URL: sameOrigin}, nil), "same-origin redirect must be allowed")

	crossOrigin, err := url.Parse("https://evil.example.com/steal")
	require.NoError(t, err)
	require.Error(t, check(&http.Request{URL: crossOrigin}, nil), "cross-origin redirect must be refused")

	schemeDowngrade, err := url.Parse("http://server.example.com/other")
	require.NoError(t, err)
	require.Error(t, check(&http.Request{URL: schemeDowngrade}, nil), "scheme downgrade is a different origin and must be refused")
}

// TestHeaderTransportOriginPinning verifies custom headers are attached only
// for the pinned origin and never replayed to a different host.
func TestHeaderTransportOriginPinning(t *testing.T) {
	var gotOnPinned, gotOnOther atomic.Value
	gotOnPinned.Store("")
	gotOnOther.Store("")

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOnOther.Store(r.Header.Get("X-Api-Key"))
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	pinned := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOnPinned.Store(r.Header.Get("X-Api-Key"))
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer pinned.Close()

	c := &Client{httpClient: pinned.Client()}
	client := c.httpClientForMCP(pinned.URL, map[string]string{"X-Api-Key": "secret-key"})

	_, err := client.Get(pinned.URL)
	// The cross-origin redirect is refused, surfacing as an error.
	require.Error(t, err)
	require.Equal(t, "secret-key", gotOnPinned.Load(), "header must be sent to the pinned origin")
	require.Equal(t, "", gotOnOther.Load(), "custom header must not be replayed to another host")
}

// TestOAuthRoundTripperDoesNotLeakTokenOnCrossHostRedirect is the regression
// test for the reviewer's finding: the legacy SSE OAuth adapter must not send
// the user's bearer token to a host the MCP server redirects to.
func TestOAuthRoundTripperDoesNotLeakTokenOnCrossHostRedirect(t *testing.T) {
	const (
		userID   = "user-1"
		serverID = "redirector"
	)

	var gotOnOther atomic.Value
	gotOnOther.Store("")

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOnOther.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	var gotOnPinned atomic.Value
	gotOnPinned.Store("")
	pinned := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOnPinned.Store(r.Header.Get("Authorization"))
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer pinned.Close()

	manager, kv := newStatefulKVManager(t, nil, pinned.Client())
	kv.putEnvelope(t, userID, serverID, boundTestEnvelope(pinned.URL, &oauth2.Token{
		AccessToken:  "super-secret-access",
		RefreshToken: "refresh",
	}))
	handler := newUserOAuthHandler(userID, ServerConfig{Name: serverID, BaseURL: pinned.URL}, manager)

	c := &Client{httpClient: pinned.Client()}
	client := c.httpClientForLegacySSE(pinned.URL, handler, nil)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, pinned.URL, nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	require.Error(t, err, "cross-origin redirect must be refused")
	require.Equal(t, "Bearer super-secret-access", gotOnPinned.Load(), "token must be sent to the pinned server")
	require.Equal(t, "", gotOnOther.Load(), "bearer token must never reach the redirect target host")
}

// TestOAuthRoundTripperOriginPinSkipsTokenForOtherHost is a direct unit check
// that the adapter does not attach the token when the request URL is not the
// pinned origin (defense in depth behind CheckRedirect).
func TestOAuthRoundTripperOriginPinSkipsTokenForOtherHost(t *testing.T) {
	const (
		userID   = "user-1"
		serverID = "srv"
	)

	var gotAuth atomic.Value
	gotAuth.Store("")
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	manager, kv := newStatefulKVManager(t, nil, other.Client())
	kv.putEnvelope(t, userID, serverID, boundTestEnvelope("https://pinned.example.com", &oauth2.Token{AccessToken: "secret"}))
	handler := newUserOAuthHandler(userID, ServerConfig{Name: serverID, BaseURL: "https://pinned.example.com"}, manager)

	rt := &oauthRoundTripper{
		handler:        handler,
		base:           other.Client().Transport,
		expectedOrigin: canonicalOriginFromString("https://pinned.example.com"),
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, other.URL, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "", gotAuth.Load(), "token must not be attached for a non-pinned origin")
}
