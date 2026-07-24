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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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

// failSpan records an error on the span with a stage label so every early
// return is visible in traces. Only safe metadata is attached: the failure
// stage and HTTP status code, never the query.
func failSpan(span trace.Span, stage string, statusCode int, err error) {
	span.SetAttributes(attribute.String("agents.websearch.fail_stage", stage))
	if statusCode > 0 {
		span.SetAttributes(attribute.Int("http.response.status_code", statusCode))
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
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
		err = fmt.Errorf("failed to create web search request: %w", err)
		failSpan(span, "create_request", 0, err)
		return nil, err
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
		err = fmt.Errorf("web search http client is not configured")
		failSpan(span, "nil_http_client", 0, err)
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("web search request failed", "error", err)
		}
		err = fmt.Errorf("web search request failed: %w", err)
		failSpan(span, "http_request", 0, err)
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden {
		// SearXNG returns 403 for formats not listed in search.formats;
		// json is not enabled by default, so give operators the pointer.
		err = fmt.Errorf("web search request failed: status %s (is 'json' enabled under search.formats in the SearXNG settings?)", resp.Status)
		failSpan(span, "http_status", resp.StatusCode, err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("web search request failed: status %s", resp.Status)
		failSpan(span, "http_status", resp.StatusCode, err)
		return nil, err
	}

	var payload searxngSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		err = fmt.Errorf("failed to decode web search response: %w", err)
		failSpan(span, "decode_response", resp.StatusCode, err)
		return nil, err
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
