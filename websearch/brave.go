// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const defaultBraveSearchEndpoint = "https://api.search.brave.com"

// BraveProvider implements the Provider interface for Brave Search API.
type BraveProvider struct {
	apiKey       string
	apiURL       string
	httpClient   *http.Client
	logger       Logger
	pollTimeout  time.Duration
	pollInterval time.Duration
}

// NewBraveProvider creates a new BraveProvider instance.
func NewBraveProvider(apiKey, apiURL string, pollTimeout, pollInterval int, httpClient *http.Client, logger Logger) *BraveProvider {
	if apiURL == "" {
		apiURL = defaultBraveSearchEndpoint
	}
	timeout := time.Duration(pollTimeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	interval := time.Duration(pollInterval) * time.Millisecond
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	return &BraveProvider{
		apiKey:       apiKey,
		apiURL:       apiURL,
		httpClient:   httpClient,
		logger:       logger,
		pollTimeout:  timeout,
		pollInterval: interval,
	}
}

// Search performs a Brave Search and returns the results with optional pre-formatted answer.
func (b *BraveProvider) Search(ctx context.Context, query string, limit int) (*SearchResponse, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	// Step 1: Initial web search with summary request
	webSearchURL := fmt.Sprintf("%s/res/v1/web/search", strings.TrimSuffix(b.apiURL, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webSearchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create web search request: %w", err)
	}

	values := url.Values{}
	values.Set("q", query)
	values.Set("summary", "1")
	req.URL.RawQuery = values.Encode()
	req.Header.Set("X-Subscription-Token", b.apiKey)
	req.Header.Set("Accept", "application/json")

	client := b.httpClient
	if client == nil {
		if b.logger != nil {
			b.logger.Error("web search http client is not configured")
		}
		return nil, fmt.Errorf("web search http client is not configured")
	}

	resp, err := client.Do(req)
	if err != nil {
		if b.logger != nil {
			b.logger.Error("brave web search request failed", "error", err)
		}
		return nil, fmt.Errorf("brave web search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave web search request failed: status %s", resp.Status)
	}

	var webSearchResp braveWebSearchResponse
	err = json.NewDecoder(resp.Body).Decode(&webSearchResp)
	if err != nil {
		return nil, fmt.Errorf("failed to decode brave web search response: %w", err)
	}

	// Build fallback results from web search response
	fallbackResults := b.extractWebResults(webSearchResp, limit)

	// Step 2: Check for summarizer key and fetch summary if available
	if webSearchResp.Summarizer.Key == "" {
		if b.logger != nil {
			b.logger.Debug("no summarizer key found, returning web results only")
		}
		return &SearchResponse{
			Answer:  "",
			Results: fallbackResults,
		}, nil
	}

	// Fetch the summary using the key
	summarizerURL := fmt.Sprintf("%s/res/v1/summarizer/search", strings.TrimSuffix(b.apiURL, "/"))
	summaryReq, err := http.NewRequestWithContext(ctx, http.MethodGet, summarizerURL, nil)
	if err != nil {
		if b.logger != nil {
			b.logger.Warn("failed to create summarizer request, using fallback", "error", err)
		}
		return &SearchResponse{
			Answer:  "",
			Results: fallbackResults,
		}, nil
	}

	summaryValues := url.Values{}
	summaryValues.Set("key", webSearchResp.Summarizer.Key)
	summaryValues.Set("entity_info", "1")
	summaryReq.URL.RawQuery = summaryValues.Encode()
	summaryReq.Header.Set("X-Subscription-Token", b.apiKey)
	summaryReq.Header.Set("Accept", "application/json")

	// Poll for completion
	summary, err := b.pollSummarizer(ctx, client, summaryReq)
	if err != nil {
		if b.logger != nil {
			b.logger.Warn("failed to get summary, using fallback", "error", err)
		}
		return &SearchResponse{
			Answer:  "",
			Results: fallbackResults,
		}, nil
	}

	// Extract all context results - keep the full array so citations map directly
	// Brave's [1] will point to our result [1], [7] to result [7], etc.
	allContextResults := b.extractContextResults(summary.Enrichments.Context, len(summary.Enrichments.Context))

	// If no context results, use fallback
	if len(allContextResults) == 0 {
		return &SearchResponse{
			Answer:  "",
			Results: fallbackResults,
		}, nil
	}

	// Simple citation conversion: [N] → !!CITEN!!
	// No remapping needed since we're returning all results in order
	answer := b.convertBraveCitations(summary.Enrichments.Raw)

	// Return ALL context results - this keeps citation indices aligned
	// and gives the LLM access to all sources for additional context
	return &SearchResponse{
		Answer:  answer,
		Results: allContextResults,
	}, nil
}

// pollSummarizer polls the summarizer endpoint until status is complete or timeout.
func (b *BraveProvider) pollSummarizer(ctx context.Context, client *http.Client, req *http.Request) (*braveSummarizerResponse, error) {
	deadline := time.Now().Add(b.pollTimeout)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("summarizer polling timed out after %v", b.pollTimeout)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("summarizer request failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("summarizer request failed: status %s", resp.Status)
		}

		var summaryResp braveSummarizerResponse
		if err := json.NewDecoder(resp.Body).Decode(&summaryResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode summarizer response: %w", err)
		}
		resp.Body.Close()

		if summaryResp.Status == "complete" {
			return &summaryResp, nil
		}

		if b.logger != nil {
			b.logger.Debug("summarizer not ready, polling again", "status", summaryResp.Status)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(b.pollInterval):
			// Continue polling
		}
	}
}

// extractWebResults extracts search results from the web search response.
func (b *BraveProvider) extractWebResults(resp braveWebSearchResponse, limit int) []SearchResult {
	results := make([]SearchResult, 0, limit)
	for i, item := range resp.Web.Results {
		if i >= limit {
			break
		}
		results = append(results, SearchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     strings.TrimSpace(item.URL),
			Snippet: strings.TrimSpace(item.Description),
		})
	}
	return results
}

// extractContextResults extracts search results from the summarizer context array.
func (b *BraveProvider) extractContextResults(contexts []braveContextItem, limit int) []SearchResult {
	results := make([]SearchResult, 0, limit)
	for i, item := range contexts {
		if i >= limit {
			break
		}
		results = append(results, SearchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     strings.TrimSpace(item.URL),
			Snippet: "", // Context items don't have snippets
		})
	}
	return results
}

// convertBraveCitations converts Brave's [1], [2] citation format to !!CITE1!!, !!CITE2!! format.
// Since we return all context results in order, the indices map directly.
func (b *BraveProvider) convertBraveCitations(text string) string {
	// Replace [digit] with !!CITEdigit!!
	re := regexp.MustCompile(`\[(\d+)\]`)
	return re.ReplaceAllString(text, "!!CITE$1!!")
}

type braveWebSearchResponse struct {
	Summarizer struct {
		Key string `json:"key"`
	} `json:"summarizer"`
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

type braveSummarizerResponse struct {
	Type        string `json:"type"`
	Status      string `json:"status"`
	Title       string `json:"title"`
	Enrichments struct {
		Raw     string             `json:"raw"`
		Context []braveContextItem `json:"context"`
	} `json:"enrichments"`
}

type braveContextItem struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}
