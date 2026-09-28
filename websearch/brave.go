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
	"go.opentelemetry.io/otel/codes"
)

const (
	defaultBraveSearchEndpoint = "https://api.search.brave.com"

	// defaultBraveResultLimit is used when the caller does not specify a limit.
	defaultBraveResultLimit = 5
	// maxBraveResultLimit is the ceiling Brave enforces on maximum_number_of_urls.
	maxBraveResultLimit = 50
	// defaultBraveMaxTokens is the approximate token budget Brave applies
	// across all extracted page content in a single response, used when the
	// admin has not set one. Deliberately below Brave's own 8192 default:
	// agents may run several searches per turn, and oversized tool results
	// push older conversation posts out of the model's context window.
	defaultBraveMaxTokens = 4096
	// minBraveMaxTokens and maxBraveMaxTokens are the bounds Brave enforces on
	// maximum_number_of_tokens.
	minBraveMaxTokens = 1024
	maxBraveMaxTokens = 32768
)

// BraveProvider implements the Provider interface for the Brave LLM Context
// API, which returns page content already extracted for LLM grounding.
type BraveProvider struct {
	apiKey     string
	apiURL     string
	maxTokens  int
	httpClient *http.Client
	logger     Logger
}

// NewBraveProvider creates a new BraveProvider instance. maxTokens is the
// approximate budget for extracted page content; zero selects the default and
// out-of-range values are clamped to what Brave accepts.
func NewBraveProvider(apiKey, apiURL string, maxTokens int, httpClient *http.Client, logger Logger) *BraveProvider {
	if apiURL == "" {
		apiURL = defaultBraveSearchEndpoint
	}
	switch {
	case maxTokens <= 0:
		maxTokens = defaultBraveMaxTokens
	case maxTokens < minBraveMaxTokens:
		maxTokens = minBraveMaxTokens
	case maxTokens > maxBraveMaxTokens:
		maxTokens = maxBraveMaxTokens
	}
	return &BraveProvider{
		apiKey:     apiKey,
		apiURL:     apiURL,
		maxTokens:  maxTokens,
		httpClient: httpClient,
		logger:     logger,
	}
}

// Search performs a Brave LLM Context search and returns the extracted content
// for each source. Brave does not synthesize an answer on this endpoint, so
// SearchResponse.Answer is always empty and the calling agent writes its own
// answer from the results.
func (b *BraveProvider) Search(ctx context.Context, query string, limit int) (*SearchResponse, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "brave web search")
	defer span.End()

	if limit <= 0 {
		limit = defaultBraveResultLimit
	}
	if limit > maxBraveResultLimit {
		limit = maxBraveResultLimit
	}

	client := b.httpClient
	if client == nil {
		if b.logger != nil {
			b.logger.Error("web search http client is not configured")
		}
		return nil, fmt.Errorf("web search http client is not configured")
	}

	contextURL := fmt.Sprintf("%s/res/v1/llm/context", strings.TrimSuffix(b.apiURL, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, contextURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create brave llm context request: %w", err)
	}

	values := url.Values{}
	values.Set("q", query)
	values.Set("maximum_number_of_urls", strconv.Itoa(limit))
	values.Set("maximum_number_of_tokens", strconv.Itoa(b.maxTokens))
	req.URL.RawQuery = values.Encode()
	req.Header.Set("X-Subscription-Token", b.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		if b.logger != nil {
			b.logger.Error("brave llm context request failed", "error", err)
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("brave llm context request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("brave llm context request failed: status %s", resp.Status)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	var contextResp braveLLMContextResponse
	if err := json.NewDecoder(resp.Body).Decode(&contextResp); err != nil {
		return nil, fmt.Errorf("failed to decode brave llm context response: %w", err)
	}

	return &SearchResponse{
		Answer:  "",
		Results: b.extractResults(contextResp, limit),
	}, nil
}

// extractResults flattens the grounding payload into search results, using the
// sources map to fill in a title when the grounding entry does not carry one.
func (b *BraveProvider) extractResults(resp braveLLMContextResponse, limit int) []SearchResult {
	results := make([]SearchResult, 0, limit)
	for _, item := range resp.Grounding.Generic {
		if len(results) >= limit {
			break
		}

		itemURL := strings.TrimSpace(item.URL)
		if itemURL == "" {
			continue
		}

		title := strings.TrimSpace(item.Title)
		if title == "" {
			source := resp.Sources[item.URL]
			title = strings.TrimSpace(source.Title)
			if title == "" {
				title = strings.TrimSpace(source.Hostname)
			}
		}

		results = append(results, SearchResult{
			Title:   title,
			URL:     itemURL,
			Snippet: joinSnippets(item.Snippets),
		})
	}

	return results
}

// joinSnippets concatenates the extracted chunks for a single page, dropping
// blank ones. Chunks may be plain text or JSON-serialized structured data
// (tables, code blocks); both are passed through to the model unchanged.
func joinSnippets(snippets []string) string {
	parts := make([]string, 0, len(snippets))
	for _, snippet := range snippets {
		if trimmed := strings.TrimSpace(snippet); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n\n")
}

type braveLLMContextResponse struct {
	Grounding braveGrounding         `json:"grounding"`
	Sources   map[string]braveSource `json:"sources"`
}

type braveGrounding struct {
	Generic []braveGroundingItem `json:"generic"`
}

type braveGroundingItem struct {
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	Snippets []string `json:"snippets"`
}

type braveSource struct {
	Title    string `json:"title"`
	Hostname string `json:"hostname"`
}
