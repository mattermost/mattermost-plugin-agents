// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"go.opentelemetry.io/otel/codes"
)

// SearXNGProvider implements the Provider interface for a self-hosted
// SearXNG metasearch instance (JSON API: GET /search?q=...&format=json).
// There is no API key: access control is the deployment's own concern
// (network placement, reverse-proxy auth via custom headers, etc.).
type SearXNGProvider struct {
	baseURL    string
	httpClient *http.Client
	logger     Logger
}

// NewSearXNGProvider creates a new SearXNGProvider instance. baseURL is the
// root of the SearXNG instance (e.g. "https://searx.example.com"); the
// provider appends /search itself.
func NewSearXNGProvider(baseURL string, httpClient *http.Client, logger Logger) *SearXNGProvider {
	return &SearXNGProvider{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
		logger:     logger,
	}
}

// Search performs a SearXNG search and returns the results.
func (s *SearXNGProvider) Search(ctx context.Context, query string, limit int) (*SearchResponse, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "searxng web search")
	defer span.End()

	if limit <= 0 {
		limit = 5
	}

	endpoint := s.baseURL + "/search"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create web search request: %w", err)
	}

	values := url.Values{}
	values.Set("q", query)
	values.Set("format", "json")
	req.URL.RawQuery = values.Encode()
	req.Header.Set("Accept", "application/json")

	client := s.httpClient
	if client == nil {
		if s.logger != nil {
			s.logger.Error("web search http client is not configured")
		}
		return nil, fmt.Errorf("web search http client is not configured")
	}

	resp, err := client.Do(req)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("web search request failed", "error", err)
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("web search request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden {
		// SearXNG returns 403 for formats not listed in search.formats;
		// json is not enabled by default, so give operators the pointer.
		return nil, fmt.Errorf("web search request failed: status %s (is 'json' enabled under search.formats in the SearXNG settings?)", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("web search request failed: status %s", resp.Status)
	}

	var payload searxngSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode web search response: %w", err)
	}

	if len(payload.Results) > limit {
		payload.Results = payload.Results[:limit]
	}

	results := make([]SearchResult, 0, len(payload.Results))
	for _, item := range payload.Results {
		results = append(results, SearchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     strings.TrimSpace(item.URL),
			Snippet: strings.TrimSpace(item.Content),
		})
	}

	return &SearchResponse{
		Answer:  "", // SearXNG doesn't provide pre-formatted answers
		Results: results,
	}, nil
}

type searxngSearchResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}
