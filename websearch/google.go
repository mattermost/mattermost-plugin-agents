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
)

const defaultGoogleSearchEndpoint = "https://www.googleapis.com/customsearch/v1"

// GoogleProvider implements the Provider interface for Google Custom Search API.
type GoogleProvider struct {
	apiKey         string
	searchEngineID string
	apiURL         string
	httpClient     *http.Client
	logger         Logger
}

// NewGoogleProvider creates a new GoogleProvider instance.
func NewGoogleProvider(apiKey, searchEngineID, apiURL string, httpClient *http.Client, logger Logger) *GoogleProvider {
	if apiURL == "" {
		apiURL = defaultGoogleSearchEndpoint
	}
	return &GoogleProvider{
		apiKey:         apiKey,
		searchEngineID: searchEngineID,
		apiURL:         apiURL,
		httpClient:     httpClient,
		logger:         logger,
	}
}

// Search performs a Google Custom Search and returns the results.
func (g *GoogleProvider) Search(ctx context.Context, query string, limit int) (*SearchResponse, error) {
	endpoint := strings.TrimSpace(g.apiURL)
	if endpoint == "" {
		endpoint = defaultGoogleSearchEndpoint
	}

	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create web search request: %w", err)
	}

	values := url.Values{}
	values.Set("key", g.apiKey)
	values.Set("cx", g.searchEngineID)
	values.Set("q", query)
	values.Set("num", strconv.Itoa(limit))
	req.URL.RawQuery = values.Encode()
	req.Header.Set("Accept", "application/json")

	client := g.httpClient
	if client == nil {
		if g.logger != nil {
			g.logger.Error("web search http client is not configured")
		}
		return nil, fmt.Errorf("web search http client is not configured")
	}

	resp, err := client.Do(req)
	if err != nil {
		if g.logger != nil {
			g.logger.Error("web search request failed", "error", err)
		}
		return nil, fmt.Errorf("web search request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("web search request failed: status %s", resp.Status)
	}

	var payload googleSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode web search response: %w", err)
	}

	results := make([]SearchResult, 0, len(payload.Items))
	for _, item := range payload.Items {
		results = append(results, SearchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     strings.TrimSpace(item.Link),
			Snippet: strings.TrimSpace(item.Snippet),
		})
	}

	return &SearchResponse{
		Answer:  "", // Google doesn't provide pre-formatted answers
		Results: results,
	}, nil
}

type googleSearchResponse struct {
	Items []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
	} `json:"items"`
}
