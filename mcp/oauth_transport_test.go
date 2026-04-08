// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type stubOAuthAuthManager struct {
	token *oauth2.Token
	err   error
}

func (s stubOAuthAuthManager) loadToken(string, string) (*oauth2.Token, error) {
	return s.token, s.err
}

func (stubOAuthAuthManager) deleteToken(string, string) error {
	return nil
}

func (stubOAuthAuthManager) createOAuthConfig(context.Context, string, string, *StaticOAuthCredentials) (*oauth2.Config, error) {
	return nil, errors.New("stubOAuthAuthManager: createOAuthConfig should not be called in this test")
}

func TestAuthenticationTransport_AutomatedInvokerFallbackAuth(t *testing.T) {
	t.Parallel()

	t.Run("sends fallback headers when there is no OAuth token", func(t *testing.T) {
		t.Parallel()

		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		t.Cleanup(srv.Close)

		rt := &authenticationTransport{
			userID:              "user-1",
			serverName:          "remote",
			serverURL:           srv.URL,
			manager:             stubOAuthAuthManager{},
			base:                http.DefaultTransport,
			fallbackAuthHeaders: map[string]string{"Authorization": "Bearer mcp-fallback-token"},
			isAutomatedInvoker:  true,
		}

		req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp", http.NoBody)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()

		require.Equal(t, "Bearer mcp-fallback-token", gotAuth)
	})

	t.Run("errors when automated and no token and no fallback headers", func(t *testing.T) {
		t.Parallel()

		rt := &authenticationTransport{
			userID:              "user-1",
			serverName:          "remote",
			serverURL:           "http://unused.example",
			manager:             stubOAuthAuthManager{},
			base:                http.DefaultTransport,
			fallbackAuthHeaders: nil,
			isAutomatedInvoker:  true,
		}

		req, err := http.NewRequest(http.MethodGet, "http://unused.example/mcp", http.NoBody)
		require.NoError(t, err)

		_, err = rt.RoundTrip(req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no fallback authentication headers")
	})
}
