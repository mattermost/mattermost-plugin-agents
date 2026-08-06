// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// captureRoundTripper records client-side requests leaving a transport chain.
// Each test owns its instance and sends requests sequentially, so no locking.
type captureRoundTripper struct {
	requests []*http.Request
}

func (c *captureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.requests = append(c.requests, req.Clone(req.Context()))
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func (c *captureRoundTripper) last() *http.Request {
	if len(c.requests) == 0 {
		return nil
	}
	return c.requests[len(c.requests)-1]
}

func TestHeaderTransportOriginGating(t *testing.T) {
	t.Parallel()

	origin, err := url.Parse("https://mcp.example.com/v1")
	require.NoError(t, err)
	originKey := originComparableKey(origin)

	tests := []struct {
		name         string
		originKey    string
		requestURL   string
		wantInjected bool
	}{
		{
			name:         "same origin injects when origin set",
			originKey:    originKey,
			requestURL:   "https://mcp.example.com/tools",
			wantInjected: true,
		},
		{
			name:         "cross origin skips injection when origin set",
			originKey:    originKey,
			requestURL:   "https://evil.example.com/tools",
			wantInjected: false,
		},
		{
			name:         "empty origin key always injects (plugin transports)",
			originKey:    "",
			requestURL:   "https://evil.example.com/tools",
			wantInjected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base := &captureRoundTripper{}
			transport := &headerTransport{
				base:      base,
				headers:   map[string]string{"X-Secret": "cred"},
				originKey: tt.originKey,
			}

			req, err := http.NewRequest(http.MethodGet, tt.requestURL, nil)
			require.NoError(t, err)

			resp, err := transport.RoundTrip(req)
			require.NoError(t, err)
			require.NotNil(t, resp)
			_ = resp.Body.Close()

			captured := base.last()
			require.NotNil(t, captured)
			if tt.wantInjected {
				require.Equal(t, "cred", captured.Header.Get("X-Secret"))
			} else {
				require.Empty(t, captured.Header.Get("X-Secret"))
			}
		})
	}
}

func TestHTTPClientForMCPRedirectPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		crossOrigin     bool
		wantErrContains string
	}{
		{
			name:        "same-origin redirect is followed and keeps credential headers",
			crossOrigin: false,
		},
		{
			name:            "cross-origin redirect is rejected before any request reaches the target",
			crossOrigin:     true,
			wantErrContains: "different origin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			otherRecorder := &requestHeaderRecorder{}
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				otherRecorder.record(r.Header.Clone())
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(other.Close)

			finalRecorder := &requestHeaderRecorder{}
			mux := http.NewServeMux()
			mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
				if tt.crossOrigin {
					http.Redirect(w, r, other.URL+"/final", http.StatusFound)
					return
				}
				http.Redirect(w, r, "/final", http.StatusFound)
			})
			mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
				finalRecorder.record(r.Header.Clone())
				w.WriteHeader(http.StatusOK)
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)

			client := &Client{
				config:     ServerConfig{Name: "redirect-server", BaseURL: server.URL},
				httpClient: &http.Client{},
			}
			httpClient := client.httpClientForMCP(testServiceAccountHeaders())

			req, err := http.NewRequest(http.MethodGet, server.URL+"/start", nil)
			require.NoError(t, err)

			resp, err := httpClient.Do(req)
			if tt.wantErrContains != "" {
				// On a CheckRedirect error, Do returns the prior response with its body already closed.
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrContains)
				require.Empty(t, otherRecorder.snapshot(), "cross-origin redirect target must not receive the follow-up request")
				return
			}

			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			_ = resp.Body.Close()

			finals := finalRecorder.snapshot()
			require.Len(t, finals, 1)
			require.Equal(t, testSAHeaderValue, finals[0].Get(testSAHeaderName))
		})
	}
}

func TestAuthenticationTransportOriginGating(t *testing.T) {
	t.Parallel()

	serverOrigin, err := url.Parse("https://mcp.example.com/mcp")
	require.NoError(t, err)

	failDiscovery := &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("discovery disabled in test")
		}),
	}

	tests := []struct {
		name         string
		serverOrigin *url.URL
		requestURL   string
		wantAuthHdr  bool
	}{
		{
			name:         "same origin injects bearer token",
			serverOrigin: serverOrigin,
			requestURL:   "https://mcp.example.com/tools/list",
			wantAuthHdr:  true,
		},
		{
			name:         "cross origin skips bearer token",
			serverOrigin: serverOrigin,
			requestURL:   "https://evil.example.com/tools/list",
			wantAuthHdr:  false,
		},
		{
			name:         "nil server origin never injects (fail closed)",
			serverOrigin: nil,
			requestURL:   "https://mcp.example.com/tools/list",
			wantAuthHdr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manager, mockClient := setupTestOAuthManagerFull(t, nil, failDiscovery)
			mockClient.On("KVGet", buildTokenKey("user-1", "oauth-server"), mock.AnythingOfType("*oauth2.Token")).
				Run(func(args mock.Arguments) {
					tok := args.Get(1).(*oauth2.Token)
					*tok = oauth2.Token{
						AccessToken: "leak-me-token",
						TokenType:   "Bearer",
						Expiry:      time.Now().Add(time.Hour),
					}
				}).
				Return(nil).
				Once()

			base := &captureRoundTripper{}
			transport := &authenticationTransport{
				userID:       "user-1",
				serverName:   "oauth-server",
				serverURL:    "https://mcp.example.com/mcp",
				serverOrigin: tt.serverOrigin,
				manager:      manager,
				staticCreds: &StaticOAuthCredentials{
					ClientID:     "test-client",
					ClientSecret: "test-secret",
				},
				base: base,
			}

			req, err := http.NewRequest(http.MethodGet, tt.requestURL, nil)
			require.NoError(t, err)

			resp, err := transport.RoundTrip(req)
			require.NoError(t, err)
			require.NotNil(t, resp)
			_ = resp.Body.Close()

			captured := base.last()
			require.NotNil(t, captured)
			auth := captured.Header.Get("Authorization")
			if tt.wantAuthHdr {
				require.Equal(t, "Bearer leak-me-token", auth)
			} else {
				require.Empty(t, auth)
			}
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
