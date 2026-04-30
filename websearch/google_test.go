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

// mockLogger implements Logger for testing
type mockLogger struct{}

func (m *mockLogger) Debug(message string, keyValuePairs ...any) {}
func (m *mockLogger) Info(message string, keyValuePairs ...any)  {}
func (m *mockLogger) Warn(message string, keyValuePairs ...any)  {}
func (m *mockLogger) Error(message string, keyValuePairs ...any) {}

func TestGoogleProvider(t *testing.T) {
	t.Run("successful search returns results", func(t *testing.T) {
		// Mock Google API server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "test-key", r.URL.Query().Get("key"))
			require.Equal(t, "test-cx", r.URL.Query().Get("cx"))
			require.Equal(t, "golang programming", r.URL.Query().Get("q"))

			response := googleSearchResponse{
				Items: []struct {
					Title   string `json:"title"`
					Link    string `json:"link"`
					Snippet string `json:"snippet"`
				}{
					{
						Title:   "Go Programming Language",
						Link:    "https://golang.org",
						Snippet: "Official Go website",
					},
					{
						Title:   "Go Tutorial",
						Link:    "https://tour.golang.org",
						Snippet: "Interactive Go tutorial",
					},
				},
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		provider := NewGoogleProvider("test-key", "test-cx", server.URL, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "golang programming", 5)

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Empty(t, resp.Answer, "Google should not provide pre-formatted answers")
		require.Len(t, resp.Results, 2)
		require.Equal(t, "Go Programming Language", resp.Results[0].Title)
		require.Equal(t, "https://golang.org", resp.Results[0].URL)
		require.Equal(t, "Official Go website", resp.Results[0].Snippet)
	})

	t.Run("handles empty results", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := googleSearchResponse{Items: []struct {
				Title   string `json:"title"`
				Link    string `json:"link"`
				Snippet string `json:"snippet"`
			}{}}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		provider := NewGoogleProvider("test-key", "test-cx", server.URL, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "nonexistent query", 5)

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Empty(t, resp.Results)
	})

	t.Run("handles API error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		provider := NewGoogleProvider("test-key", "test-cx", server.URL, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "test query", 5)

		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "status 500")
	})

	t.Run("respects result limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := r.URL.Query().Get("num")
			require.Equal(t, "3", limit, "Should send correct limit to API")

			response := googleSearchResponse{
				Items: []struct {
					Title   string `json:"title"`
					Link    string `json:"link"`
					Snippet string `json:"snippet"`
				}{
					{Title: "Result 1", Link: "https://example.com/1", Snippet: "Snippet 1"},
					{Title: "Result 2", Link: "https://example.com/2", Snippet: "Snippet 2"},
					{Title: "Result 3", Link: "https://example.com/3", Snippet: "Snippet 3"},
				},
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		provider := NewGoogleProvider("test-key", "test-cx", server.URL, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "test", 3)

		require.NoError(t, err)
		require.Len(t, resp.Results, 3)
	})
}
