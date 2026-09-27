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
			apiURL := "http://example.invalid"
			client := http.DefaultClient
			if tc.nilClient {
				client = nil
			} else {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "GET", r.Method)
					require.Equal(t, "test-key", r.Header.Get("X-Api-Key"))
					require.Equal(t, "golang programming", r.URL.Query().Get("q"))
					require.Equal(t, tc.wantNum, r.URL.Query().Get("num"))
					require.Empty(t, r.URL.Query().Get("key"), "API key must not be sent in the query string")
					w.Header().Set("Content-Type", "application/json")
					tc.handler(w, r)
				}))
				defer server.Close()
				apiURL = server.URL
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
