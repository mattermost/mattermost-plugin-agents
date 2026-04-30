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

func TestBraveProvider(t *testing.T) {
	t.Run("successful search with summarizer returns answer with remapped citations", func(t *testing.T) {
		// Mock Brave API server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/res/v1/web/search":
				// Initial web search response with summarizer key
				response := braveWebSearchResponse{
					Summarizer: struct {
						Key string `json:"key"`
					}{
						Key: `{"v":"2","query":"test"}`,
					},
					Web: struct {
						Results []struct {
							Title       string `json:"title"`
							URL         string `json:"url"`
							Description string `json:"description"`
						} `json:"results"`
					}{
						Results: []struct {
							Title       string `json:"title"`
							URL         string `json:"url"`
							Description string `json:"description"`
						}{
							{Title: "Result 1", URL: "https://example.com/1", Description: "Description 1"},
						},
					},
				}

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			case "/res/v1/summarizer/search":
				// Summarizer response
				response := braveSummarizerResponse{
					Type:   "summarizer",
					Status: "complete",
					Title:  "Test Result",
					Enrichments: struct {
						Raw     string             `json:"raw"`
						Context []braveContextItem `json:"context"`
					}{
						Raw: "This is a summary of the search results [1] with citations [2].",
						Context: []braveContextItem{
							{Title: "Context 1", URL: "https://example.com/1"},
							{Title: "Context 2", URL: "https://example.com/2"},
						},
					},
				}

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}
		}))
		defer server.Close()

		provider := NewBraveProvider("test-key", server.URL, 10, 250, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "test query", 5)

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotEmpty(t, resp.Answer, "Brave should provide pre-formatted answer with remapped citations")
		require.Contains(t, resp.Answer, "!!CITE1!!", "Should convert [1] to !!CITE1!!")
		require.Contains(t, resp.Answer, "!!CITE2!!", "Should convert [2] to !!CITE2!!")
		require.Len(t, resp.Results, 2, "Should return context results")
		require.Equal(t, "Context 1", resp.Results[0].Title)
	})

	t.Run("fallback to web results when no summarizer", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Web search response without summarizer key
			response := braveWebSearchResponse{
				Summarizer: struct {
					Key string `json:"key"`
				}{
					Key: "", // No summarizer key
				},
				Web: struct {
					Results []struct {
						Title       string `json:"title"`
						URL         string `json:"url"`
						Description string `json:"description"`
					} `json:"results"`
				}{
					Results: []struct {
						Title       string `json:"title"`
						URL         string `json:"url"`
						Description string `json:"description"`
					}{
						{Title: "Fallback Result", URL: "https://example.com", Description: "Fallback description"},
					},
				},
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		provider := NewBraveProvider("test-key", server.URL, 10, 250, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "test query", 5)

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Empty(t, resp.Answer, "Should not have answer when no summarizer")
		require.Len(t, resp.Results, 1, "Should return web search results as fallback")
		require.Equal(t, "Fallback Result", resp.Results[0].Title)
	})

	t.Run("handles API error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		provider := NewBraveProvider("invalid-key", server.URL, 10, 250, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "test", 5)

		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "status 401")
	})

	t.Run("converts citations directly", func(t *testing.T) {
		provider := NewBraveProvider("test-key", "", 10, 250, http.DefaultClient, &mockLogger{})

		testCases := []struct {
			name     string
			input    string
			expected string
		}{
			{
				name:     "single citation",
				input:    "This is text [1] with citation",
				expected: "This is text !!CITE1!! with citation",
			},
			{
				name:     "multiple citations",
				input:    "Text [1] and [2] and [3]",
				expected: "Text !!CITE1!! and !!CITE2!! and !!CITE3!!",
			},
			{
				name:     "sparse citations [3], [7], [9]",
				input:    "Price is [3] and [7] with [9]",
				expected: "Price is !!CITE3!! and !!CITE7!! with !!CITE9!!",
			},
			{
				name:     "no citations",
				input:    "Text without citations",
				expected: "Text without citations",
			},
			{
				name:     "adjacent citations",
				input:    "Text [1][2][3]",
				expected: "Text !!CITE1!!!!CITE2!!!!CITE3!!",
			},
			{
				name:     "duplicate citations",
				input:    "Text [5] and [5] again",
				expected: "Text !!CITE5!! and !!CITE5!! again",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := provider.convertBraveCitations(tc.input)
				require.Equal(t, tc.expected, result)
			})
		}
	})

	t.Run("configures polling interval and timeout correctly", func(t *testing.T) {
		provider := NewBraveProvider("test-key", "", 20, 500, http.DefaultClient, &mockLogger{})
		require.Equal(t, 20, int(provider.pollTimeout.Seconds()))
		require.Equal(t, 500, int(provider.pollInterval.Milliseconds()))

		// Default fallback
		providerDefault := NewBraveProvider("test-key", "", 0, 0, http.DefaultClient, &mockLogger{})
		require.Equal(t, 10, int(providerDefault.pollTimeout.Seconds()))
		require.Equal(t, 250, int(providerDefault.pollInterval.Milliseconds()))
	})
}
