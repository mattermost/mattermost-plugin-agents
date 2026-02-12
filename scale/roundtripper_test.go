// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scale

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoundTripper(t *testing.T) {
	tests := []struct {
		name              string
		apiKey            string
		accountID         string
		initialHeaders    map[string]string
		expectAPIKey      string
		expectAccountID   string
		expectNoAccountID bool
		expectNoAuth      bool
	}{
		{
			name:   "sets x-api-key and removes Authorization header",
			apiKey: "my-scale-key",
			initialHeaders: map[string]string{
				"Authorization": "Bearer openai-placeholder",
			},
			expectAPIKey:      "my-scale-key",
			expectNoAccountID: true,
			expectNoAuth:      true,
		},
		{
			name:      "sets x-api-key and x-selected-account-id when accountID is provided",
			apiKey:    "my-scale-key",
			accountID: "acct-123",
			initialHeaders: map[string]string{
				"Authorization": "Bearer openai-placeholder",
			},
			expectAPIKey:    "my-scale-key",
			expectAccountID: "acct-123",
			expectNoAuth:    true,
		},
		{
			name:   "omits x-selected-account-id when accountID is empty",
			apiKey: "key-456",
			initialHeaders: map[string]string{
				"Authorization": "Bearer something",
			},
			expectAPIKey:      "key-456",
			expectNoAccountID: true,
			expectNoAuth:      true,
		},
		{
			name:              "works with no initial Authorization header",
			apiKey:            "key-789",
			accountID:         "acct-456",
			initialHeaders:    map[string]string{},
			expectAPIKey:      "key-789",
			expectAccountID:   "acct-456",
			expectNoAccountID: false,
			expectNoAuth:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedHeaders http.Header

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			rt := &RoundTripper{
				Base:      http.DefaultTransport,
				APIKey:    tt.apiKey,
				AccountID: tt.accountID,
			}

			req, err := http.NewRequest(http.MethodGet, server.URL, nil)
			require.NoError(t, err)

			for k, v := range tt.initialHeaders {
				req.Header.Set(k, v)
			}

			// Save original headers to verify no mutation
			originalHeaders := req.Header.Clone()

			resp, err := rt.RoundTrip(req)
			require.NoError(t, err)
			resp.Body.Close()

			// Verify x-api-key is set correctly
			assert.Equal(t, tt.expectAPIKey, capturedHeaders.Get("x-api-key"))

			// Verify Authorization header is removed
			if tt.expectNoAuth {
				assert.Empty(t, capturedHeaders.Get("Authorization"))
			}

			// Verify x-selected-account-id
			if tt.expectNoAccountID {
				assert.Empty(t, capturedHeaders.Get("x-selected-account-id"))
			} else {
				assert.Equal(t, tt.expectAccountID, capturedHeaders.Get("x-selected-account-id"))
			}

			// Verify original request headers are not mutated
			assert.Equal(t, originalHeaders, req.Header, "original request headers should not be mutated")
		})
	}
}

func TestRoundTripper_NilBaseTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.Empty(t, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rt := &RoundTripper{
		Base:   nil, // Should fall back to http.DefaultTransport
		APIKey: "test-key",
	}

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer placeholder")

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
