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
	t.Run("successful search returns trimmed results", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/search", r.URL.Path)
			require.Equal(t, "golang programming", r.URL.Query().Get("q"))
			require.Equal(t, "json", r.URL.Query().Get("format"))

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(searxngPayload(2))
		}))
		defer server.Close()

		provider := NewSearXNGProvider(server.URL, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "golang programming", 5)

		require.NoError(t, err)
		require.Len(t, resp.Results, 2)
		require.Equal(t, "Go Programming Language", resp.Results[0].Title)
		require.Equal(t, "https://golang.org", resp.Results[0].URL)
		require.Equal(t, "Official Go website", resp.Results[0].Snippet)
		require.Empty(t, resp.Answer)
	})

	t.Run("truncates results to the requested limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(searxngPayload(3))
		}))
		defer server.Close()

		provider := NewSearXNGProvider(server.URL, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "golang", 2)

		require.NoError(t, err)
		require.Len(t, resp.Results, 2)
	})

	t.Run("trailing slash in base URL is tolerated", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/search", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(searxngPayload(1))
		}))
		defer server.Close()

		provider := NewSearXNGProvider(server.URL+"/", http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "golang", 5)

		require.NoError(t, err)
		require.Len(t, resp.Results, 1)
	})

	t.Run("403 hints at the json format allowlist", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		provider := NewSearXNGProvider(server.URL, http.DefaultClient, &mockLogger{})
		_, err := provider.Search(context.Background(), "golang", 5)

		require.Error(t, err)
		require.Contains(t, err.Error(), "search.formats")
	})

	t.Run("non-200 status returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		provider := NewSearXNGProvider(server.URL, http.DefaultClient, &mockLogger{})
		_, err := provider.Search(context.Background(), "golang", 5)

		require.Error(t, err)
	})

	t.Run("empty results are returned as empty slice", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(searxngSearchResponse{})
		}))
		defer server.Close()

		provider := NewSearXNGProvider(server.URL, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "golang", 5)

		require.NoError(t, err)
		require.Empty(t, resp.Results)
	})

	t.Run("nil http client returns error", func(t *testing.T) {
		provider := NewSearXNGProvider("http://example.invalid", nil, &mockLogger{})
		_, err := provider.Search(context.Background(), "golang", 5)

		require.Error(t, err)
	})
}
