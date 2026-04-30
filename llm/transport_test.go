// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomAuthTransport(t *testing.T) {
	tests := []struct {
		name            string
		useNilBase      bool
		removeHeaders   []string
		setHeaders      map[string]string
		initialHeaders  map[string]string
		expectedPresent map[string]string
		expectedAbsent  []string
	}{
		{
			name:           "sets custom headers",
			setHeaders:     map[string]string{"x-api-key": "my-key"},
			initialHeaders: map[string]string{},
			expectedPresent: map[string]string{
				"x-api-key": "my-key",
			},
		},
		{
			name:          "removes Authorization and sets custom headers (Scale pattern)",
			removeHeaders: []string{"Authorization"},
			setHeaders: map[string]string{
				"x-api-key":             "scale-key",
				"x-selected-account-id": "acct-123",
			},
			initialHeaders: map[string]string{
				"Authorization": "Bearer openai-placeholder",
			},
			expectedPresent: map[string]string{
				"x-api-key":             "scale-key",
				"x-selected-account-id": "acct-123",
			},
			expectedAbsent: []string{"Authorization"},
		},
		{
			name:          "preserves unrelated headers",
			removeHeaders: []string{"Authorization"},
			setHeaders:    map[string]string{"x-api-key": "key"},
			initialHeaders: map[string]string{
				"Authorization": "Bearer tok",
				"X-Request-Id":  "req-123",
				"Accept":        "application/json",
			},
			expectedPresent: map[string]string{
				"x-api-key":    "key",
				"X-Request-Id": "req-123",
				"Accept":       "application/json",
			},
			expectedAbsent: []string{"Authorization"},
		},
		{
			name:       "nil base falls back to default transport",
			useNilBase: true,
			removeHeaders: []string{
				"Authorization",
			},
			setHeaders: map[string]string{
				"x-api-key": "test-key",
			},
			initialHeaders: map[string]string{
				"Authorization": "Bearer placeholder",
			},
			expectedPresent: map[string]string{
				"x-api-key": "test-key",
			},
			expectedAbsent: []string{"Authorization"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headersCh := make(chan http.Header, 1)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headersCh <- r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			rt := &CustomAuthTransport{
				RemoveHeaders: tt.removeHeaders,
				SetHeaders:    tt.setHeaders,
			}
			if !tt.useNilBase {
				rt.Base = http.DefaultTransport
			}

			req, err := http.NewRequest(http.MethodGet, server.URL, nil)
			require.NoError(t, err)

			for k, v := range tt.initialHeaders {
				req.Header.Set(k, v)
			}

			originalHeaders := req.Header.Clone()

			resp, err := rt.RoundTrip(req)
			require.NoError(t, err)
			resp.Body.Close()

			capturedHeaders := <-headersCh

			for k, v := range tt.expectedPresent {
				assert.Equal(t, v, capturedHeaders.Get(k), "expected header %s=%s", k, v)
			}

			for _, k := range tt.expectedAbsent {
				assert.Empty(t, capturedHeaders.Get(k), "expected header %s to be absent", k)
			}

			// Original request must not be mutated
			assert.Equal(t, originalHeaders, req.Header, "original request headers should not be mutated")
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

func TestCloneHTTPClientWithTransport(t *testing.T) {
	tests := []struct {
		name          string
		client        *http.Client
		expectTimeout time.Duration
	}{
		{
			name:          "nil client returns new client with transport only",
			client:        nil,
			expectTimeout: 0,
		},
		{
			name: "preserves client settings",
			client: &http.Client{
				Timeout: 42 * time.Second,
				Jar:     http.DefaultClient.Jar,
			},
			expectTimeout: 42 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &CustomAuthTransport{
				SetHeaders: map[string]string{"x-test": "val"},
			}

			result := CloneHTTPClientWithTransport(tt.client, transport)
			require.NotNil(t, result)
			assert.Equal(t, transport, result.Transport)
			assert.Equal(t, tt.expectTimeout, result.Timeout)

			// Verify it's a different pointer than the original
			if tt.client != nil {
				assert.NotSame(t, tt.client, result)
				assert.Equal(t, tt.client.Jar, result.Jar)
				assert.Equal(t, tt.client.CheckRedirect == nil, result.CheckRedirect == nil)
			}
		})
	}
}
