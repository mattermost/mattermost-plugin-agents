// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func searxngPayload(n int) searxngSearchResponse {
	payload := searxngSearchResponse{}
	items := []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	}{
		{Title: " Go Programming Language ", URL: "https://golang.org ", Content: " Official Go website "},
		{Title: "Go Tutorial", URL: "https://tour.golang.org", Content: "Interactive Go tutorial"},
		{Title: "Go Wiki", URL: "https://go.dev/wiki", Content: "Community wiki"},
	}
	payload.Results = items[:n]
	return payload
}

func TestSearXNGProvider(t *testing.T) {
	tests := []struct {
		name            string
		handler         http.HandlerFunc
		baseURLSuffix   string
		nilClient       bool
		limit           int
		wantErr         bool
		wantErrContains string
		wantLen         int
		checkFirst      *SearchResult
	}{
		{
			name: "successful search returns trimmed results",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(searxngPayload(2))
			},
			limit:   5,
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
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(searxngPayload(3))
			},
			limit:   2,
			wantLen: 2,
		},
		{
			name: "trailing slash in base URL is tolerated",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(searxngPayload(1))
			},
			baseURLSuffix: "/",
			limit:         5,
			wantLen:       1,
		},
		{
			name: "403 hints at the json format allowlist",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			},
			limit:           5,
			wantErr:         true,
			wantErrContains: "search.formats",
		},
		{
			name: "non-200 status returns error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			limit:   5,
			wantErr: true,
		},
		{
			name: "empty results are returned as empty slice",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(searxngSearchResponse{})
			},
			limit:   5,
			wantLen: 0,
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
			baseURL := "http://example.invalid"
			client := http.DefaultClient
			if tc.nilClient {
				client = nil
			} else {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "GET", r.Method)
					require.Equal(t, "/search", r.URL.Path)
					require.Equal(t, "golang programming", r.URL.Query().Get("q"))
					require.Equal(t, "json", r.URL.Query().Get("format"))
					tc.handler(w, r)
				}))
				defer server.Close()
				baseURL = server.URL + tc.baseURLSuffix
			}

			provider := NewSearXNGProvider(baseURL, client, &mockLogger{})
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
