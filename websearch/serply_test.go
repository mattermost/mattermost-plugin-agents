// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type serplyTestItem = struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	Description string `json:"description"`
}

func serplyPayload(items ...serplyTestItem) serplySearchResponse {
	return serplySearchResponse{Results: items}
}

var serplyGoItems = []serplyTestItem{
	{Title: " Go Programming Language ", Link: "https://golang.org ", Description: " Official Go website "},
	{Title: "Go Tutorial", Link: "https://tour.golang.org", Description: "Interactive Go tutorial"},
	{Title: "Go Wiki", Link: "https://go.dev/wiki", Description: "Community wiki"},
}

func TestSerplyProvider(t *testing.T) {
	tests := []struct {
		name            string
		handler         http.HandlerFunc
		nilClient       bool
		limit           int
		wantNum         string
		wantErr         bool
		wantErrContains string
		wantLen         int
		checkFirst      *SearchResult
	}{
		{
			name: "successful search returns trimmed results",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(serplyPayload(serplyGoItems[:2]...))
			},
			limit:   5,
			wantNum: "5",
			wantLen: 2,
			checkFirst: &SearchResult{
				Title:   "Go Programming Language",
				URL:     "https://golang.org",
				Snippet: "Official Go website",
			},
		},
		{
			name: "truncates results to the requested limit",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(serplyPayload(serplyGoItems...))
			},
			limit:   2,
			wantNum: "2",
			wantLen: 2,
		},
		{
			name: "limit above 10 is clamped",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(serplyPayload())
			},
			limit:   50,
			wantNum: "10",
			wantLen: 0,
		},
		{
			name: "zero limit falls back to the default",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(serplyPayload())
			},
			limit:   0,
			wantNum: "5",
			wantLen: 0,
		},
		{
			name: "results without an http link are skipped",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(serplyPayload(
					serplyTestItem{Title: "No link"},
					serplyTestItem{Title: "Script", Link: "javascript:alert(1)"},
					serplyGoItems[1],
				))
			},
			limit:      5,
			wantNum:    "5",
			wantLen:    1,
			checkFirst: &SearchResult{Title: "Go Tutorial", URL: "https://tour.golang.org", Snippet: "Interactive Go tutorial"},
		},
		{
			name: "invalid API key returns status error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"detail":"Invalid API key"}`))
			},
			limit:           5,
			wantNum:         "5",
			wantErr:         true,
			wantErrContains: "status 401",
		},
		{
			name: "malformed JSON returns decode error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{not json`))
			},
			limit:           5,
			wantNum:         "5",
			wantErr:         true,
			wantErrContains: "decode",
		},
		{
			name:      "nil http client returns error",
			nilClient: true,
			limit:     5,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			apiURL := "https://example.invalid"
			var client *http.Client
			if !tc.nilClient {
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "GET", r.Method)
					assert.Equal(t, "test-key", r.Header.Get("X-Api-Key"))
					assert.Equal(t, "golang programming", r.URL.Query().Get("q"))
					assert.Equal(t, tc.wantNum, r.URL.Query().Get("num"))
					assert.Empty(t, r.URL.Query().Get("key"), "API key must not be sent in the query string")
					w.Header().Set("Content-Type", "application/json")
					tc.handler(w, r)
				}))
				defer server.Close()
				apiURL = server.URL
				client = server.Client()
			}

			provider := NewSerplyProvider("test-key", apiURL, client, &mockLogger{})
			resp, err := provider.Search(context.Background(), "golang programming", tc.limit)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrContains != "" {
					require.Contains(t, err.Error(), tc.wantErrContains)
				}
				return
			}

			require.NoError(t, err)
			require.Len(t, resp.Results, tc.wantLen)
			require.Empty(t, resp.Answer)
			if tc.checkFirst != nil {
				require.Equal(t, *tc.checkFirst, resp.Results[0])
			}
		})
	}
}

func TestSerplyProviderEndpointSafety(t *testing.T) {
	tests := []struct {
		name            string
		setup           func(t *testing.T, keySeen *bool) (apiURL string, client *http.Client)
		wantErrContains string
	}{
		{
			name: "plaintext endpoint is refused before any request",
			setup: func(t *testing.T, keySeen *bool) (string, *http.Client) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					*keySeen = true
				}))
				t.Cleanup(server.Close)
				return server.URL, server.Client()
			},
			wantErrContains: "must be an absolute https URL",
		},
		{
			name: "redirect to another host is not followed",
			setup: func(t *testing.T, keySeen *bool) (string, *http.Client) {
				other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					*keySeen = r.Header.Get("X-Api-Key") != ""
				}))
				t.Cleanup(other.Close)
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, other.URL+"/v1/search", http.StatusFound)
				}))
				t.Cleanup(server.Close)
				return server.URL, server.Client()
			},
			wantErrContains: "refusing redirect",
		},
		{
			name: "same-host redirect loop stops at the hop limit",
			setup: func(t *testing.T, _ *bool) (string, *http.Client) {
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "/loop", http.StatusFound)
				}))
				t.Cleanup(server.Close)
				return server.URL, server.Client()
			},
			wantErrContains: "stopped after 10 redirects",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keySeen := false
			apiURL, client := tc.setup(t, &keySeen)

			provider := NewSerplyProvider("test-key", apiURL, client, &mockLogger{})
			_, err := provider.Search(context.Background(), "golang programming", 5)

			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErrContains)
			require.False(t, keySeen, "the API key must not reach a host other than the configured endpoint")
		})
	}
}
