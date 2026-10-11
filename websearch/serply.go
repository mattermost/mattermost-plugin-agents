// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"go.opentelemetry.io/otel/trace"
)

const defaultSerplySearchEndpoint = "https://api.serply.io/v1/search"

// SerplyProvider implements the Provider interface for the Serply Google
// search API (GET /v1/search?q=...&num=..., key in the X-Api-Key header).
type SerplyProvider struct {
	apiKey     string
	apiURL     string
	httpClient *http.Client
	logger     Logger
}

// NewSerplyProvider creates a new SerplyProvider instance.
func NewSerplyProvider(apiKey, apiURL string, httpClient *http.Client, logger Logger) *SerplyProvider {
	return &SerplyProvider{
		apiKey:     apiKey,
		apiURL:     strings.TrimSpace(apiURL),
		httpClient: httpClient,
		logger:     logger,
	}
}

// Search performs a Serply search and returns the results.
func (s *SerplyProvider) Search(ctx context.Context, query string, limit int) (*SearchResponse, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	ctx, span := telemetry.Tracer().Start(ctx, "serply web search",
		trace.WithAttributes(
			telemetry.WebSearchProvider.String("serply"),
			telemetry.WebSearchResultLimit.Int(limit),
		),
	)
	defer span.End()

	endpoint := s.apiURL
	if endpoint == "" {
		endpoint = defaultSerplySearchEndpoint
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		err = fmt.Errorf("failed to create web search request: %w", err)
		failSpan(span, "create_request", 0, err)
		return nil, err
	}
	// The API key travels in a header, so refuse a plaintext endpoint before
	// the header is ever set.
	if req.URL.Scheme != "https" || req.URL.Host == "" {
		err = fmt.Errorf("serply API URL must be an absolute https URL")
		failSpan(span, "invalid_endpoint", 0, err)
		return nil, err
	}

	values := url.Values{}
	values.Set("q", query)
	values.Set("num", strconv.Itoa(limit))
	req.URL.RawQuery = values.Encode()
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Api-Key", s.apiKey)
	req.Header.Set("User-Agent", "mattermost-plugin-agents")

	client := s.httpClient
	if client == nil {
		if s.logger != nil {
			s.logger.Error("web search http client is not configured")
		}
		err = fmt.Errorf("web search http client is not configured")
		failSpan(span, "nil_http_client", 0, err)
		return nil, err
	}

	resp, err := serplyPinnedClient(client, req.URL).Do(req)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("web search request failed", "error", err)
		}
		err = fmt.Errorf("web search request failed: %w", err)
		failSpan(span, "http_request", 0, err)
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("web search request failed: status %s", resp.Status)
		failSpan(span, "http_status", resp.StatusCode, err)
		return nil, err
	}

	var payload serplySearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		err = fmt.Errorf("failed to decode web search response: %w", err)
		failSpan(span, "decode_response", resp.StatusCode, err)
		return nil, err
	}

	results := make([]SearchResult, 0, limit)
	for _, item := range payload.Results {
		link := strings.TrimSpace(item.Link)
		if !strings.HasPrefix(link, "https://") && !strings.HasPrefix(link, "http://") {
			continue
		}
		results = append(results, SearchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     link,
			Snippet: strings.TrimSpace(item.Description),
		})
		if len(results) == limit {
			break
		}
	}

	return &SearchResponse{
		Answer:  "", // Serply returns ranked results, not a pre-formatted answer
		Results: results,
	}, nil
}

type serplySearchResponse struct {
	Results []struct {
		Title       string `json:"title"`
		Link        string `json:"link"`
		Description string `json:"description"`
	} `json:"results"`
}

// serplyPinnedClient returns a shallow copy of client that only follows
// redirects to the endpoint's own https origin. Go forwards custom headers
// such as X-Api-Key across redirects, so a redirect to another host or to
// plain http must not be followed. The client's own redirect policy, or Go's
// default 10-hop limit when it has none, still applies.
func serplyPinnedClient(client *http.Client, endpoint *url.URL) *http.Client {
	pinned := *client
	next := client.CheckRedirect
	pinned.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, endpoint.Host) {
			return fmt.Errorf("refusing redirect from the Serply endpoint to %s://%s", req.URL.Scheme, req.URL.Host)
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &pinned
}
