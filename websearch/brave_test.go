// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// braveContextServer spins up a stub Brave LLM Context endpoint returning the
// supplied payload, and captures the request the provider made.
func braveContextServer(t *testing.T, payload braveLLMContextResponse, captured *http.Request) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			*captured = *r
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func TestBraveProviderSearch(t *testing.T) {
	t.Run("queries the llm context endpoint and returns extracted content", func(t *testing.T) {
		var got http.Request
		server := braveContextServer(t, braveLLMContextResponse{
			Grounding: braveGrounding{
				Generic: []braveGroundingItem{
					{
						URL:      "https://golang.org",
						Title:    "Go Programming Language",
						Snippets: []string{"Go is an open source language.", "Built at Google."},
					},
					{
						URL:      "https://tour.golang.org",
						Title:    "Go Tutorial",
						Snippets: []string{"Interactive Go tutorial"},
					},
				},
			},
		}, &got)
		defer server.Close()

		provider := NewBraveProvider("test-key", server.URL, 0, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "golang programming", 5)

		require.NoError(t, err)
		require.NotNil(t, resp)

		require.Equal(t, http.MethodGet, got.Method)
		require.Equal(t, "/res/v1/llm/context", got.URL.Path)
		require.Equal(t, "test-key", got.Header.Get("X-Subscription-Token"))
		require.Equal(t, "golang programming", got.URL.Query().Get("q"))
		require.Equal(t, "5", got.URL.Query().Get("maximum_number_of_urls"))

		require.Empty(t, resp.Answer, "LLM Context does not synthesize an answer")
		require.Len(t, resp.Results, 2)
		require.Equal(t, "Go Programming Language", resp.Results[0].Title)
		require.Equal(t, "https://golang.org", resp.Results[0].URL)
		require.Equal(t, "Go is an open source language.\n\nBuilt at Google.", resp.Results[0].Snippet,
			"all extracted chunks for a page should reach the model")
		require.Equal(t, "Interactive Go tutorial", resp.Results[1].Snippet)
	})

	t.Run("truncates results to the requested limit", func(t *testing.T) {
		generic := make([]braveGroundingItem, 0, 10)
		for i := range 10 {
			generic = append(generic, braveGroundingItem{
				URL:      "https://example.com/" + strconv.Itoa(i),
				Title:    "Result " + strconv.Itoa(i),
				Snippets: []string{"content"},
			})
		}
		server := braveContextServer(t, braveLLMContextResponse{
			Grounding: braveGrounding{Generic: generic},
		}, nil)
		defer server.Close()

		provider := NewBraveProvider("test-key", server.URL, 0, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "test", 3)

		require.NoError(t, err)
		require.Len(t, resp.Results, 3, "a provider returning more URLs than asked must not inflate the prompt")
	})

	t.Run("skips grounding entries without a URL", func(t *testing.T) {
		server := braveContextServer(t, braveLLMContextResponse{
			Grounding: braveGrounding{
				Generic: []braveGroundingItem{
					{URL: "  ", Title: "No URL", Snippets: []string{"orphan content"}},
					{URL: "https://example.com", Title: "Real", Snippets: []string{"content"}},
				},
			},
		}, nil)
		defer server.Close()

		provider := NewBraveProvider("test-key", server.URL, 0, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "test", 5)

		require.NoError(t, err)
		require.Len(t, resp.Results, 1, "results without a URL cannot be cited or fetched")
		require.Equal(t, "https://example.com", resp.Results[0].URL)
	})

	t.Run("handles empty grounding", func(t *testing.T) {
		server := braveContextServer(t, braveLLMContextResponse{}, nil)
		defer server.Close()

		provider := NewBraveProvider("test-key", server.URL, 0, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "nonexistent query", 5)

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Empty(t, resp.Results)
	})

	t.Run("handles API error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()

		provider := NewBraveProvider("test-key", server.URL, 0, http.DefaultClient, &mockLogger{})
		resp, err := provider.Search(context.Background(), "test query", 5)

		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "429")
	})

	t.Run("errors when no http client is configured", func(t *testing.T) {
		provider := NewBraveProvider("test-key", "", 0, nil, &mockLogger{})
		resp, err := provider.Search(context.Background(), "test query", 5)

		require.Error(t, err)
		require.Nil(t, resp)
	})
}

func TestBraveProviderTitleFallback(t *testing.T) {
	const pageURL = "https://example.com/page"

	for _, tc := range []struct {
		name     string
		item     braveGroundingItem
		sources  map[string]braveSource
		expected string
	}{
		{
			name:     "uses the grounding title",
			item:     braveGroundingItem{URL: pageURL, Title: "Grounding Title"},
			sources:  map[string]braveSource{pageURL: {Title: "Source Title", Hostname: "example.com"}},
			expected: "Grounding Title",
		},
		{
			name:     "falls back to the source title",
			item:     braveGroundingItem{URL: pageURL},
			sources:  map[string]braveSource{pageURL: {Title: "Source Title", Hostname: "example.com"}},
			expected: "Source Title",
		},
		{
			name:     "falls back to the hostname",
			item:     braveGroundingItem{URL: pageURL},
			sources:  map[string]braveSource{pageURL: {Hostname: "example.com"}},
			expected: "example.com",
		},
		{
			name:     "tolerates a missing sources entry",
			item:     braveGroundingItem{URL: pageURL},
			sources:  nil,
			expected: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := braveContextServer(t, braveLLMContextResponse{
				Grounding: braveGrounding{Generic: []braveGroundingItem{tc.item}},
				Sources:   tc.sources,
			}, nil)
			defer server.Close()

			provider := NewBraveProvider("test-key", server.URL, 0, http.DefaultClient, &mockLogger{})
			resp, err := provider.Search(context.Background(), "test", 5)

			require.NoError(t, err)
			require.Len(t, resp.Results, 1)
			require.Equal(t, tc.expected, resp.Results[0].Title)
		})
	}
}

func TestBraveProviderResultLimitBounds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		limit    int
		expected string
	}{
		{name: "zero falls back to the default", limit: 0, expected: "5"},
		{name: "negative falls back to the default", limit: -3, expected: "5"},
		{name: "in-range limit is passed through", limit: 12, expected: "12"},
		{name: "above the API maximum is clamped", limit: 500, expected: "50"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got http.Request
			server := braveContextServer(t, braveLLMContextResponse{}, &got)
			defer server.Close()

			provider := NewBraveProvider("test-key", server.URL, 0, http.DefaultClient, &mockLogger{})
			_, err := provider.Search(context.Background(), "test", tc.limit)

			require.NoError(t, err)
			require.Equal(t, tc.expected, got.URL.Query().Get("maximum_number_of_urls"))
		})
	}
}

func TestNewBraveProviderDefaultEndpoint(t *testing.T) {
	provider := NewBraveProvider("test-key", "", 0, http.DefaultClient, &mockLogger{})
	require.Equal(t, defaultBraveSearchEndpoint, provider.apiURL)
}

func TestBraveProviderMaxTokensBounds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		maxTokens int
		expected  string
	}{
		{name: "unset falls back to the default", maxTokens: 0, expected: "4096"},
		{name: "negative falls back to the default", maxTokens: -1, expected: "4096"},
		{name: "below the API minimum is clamped", maxTokens: 10, expected: "1024"},
		{name: "in-range budget is passed through", maxTokens: 16384, expected: "16384"},
		{name: "above the API maximum is clamped", maxTokens: 100000, expected: "32768"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got http.Request
			server := braveContextServer(t, braveLLMContextResponse{}, &got)
			defer server.Close()

			provider := NewBraveProvider("test-key", server.URL, tc.maxTokens, http.DefaultClient, &mockLogger{})
			_, err := provider.Search(context.Background(), "test", 5)

			require.NoError(t, err)
			require.Equal(t, tc.expected, got.URL.Query().Get("maximum_number_of_tokens"))
		})
	}
}
