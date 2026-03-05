// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package websearch

import (
	"context"
)

// SearchResponse represents the response from a search provider.
type SearchResponse struct {
	Answer  string         // Optional pre-formatted answer with citations (e.g., Brave)
	Results []SearchResult // List of search results
}

// SearchResult represents a single search result.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// Provider defines the interface for web search providers.
type Provider interface {
	Search(ctx context.Context, query string, limit int) (*SearchResponse, error)
}

// Logger abstracts the logging interface used by providers.
type Logger interface {
	Debug(message string, keyValuePairs ...any)
	Info(message string, keyValuePairs ...any)
	Warn(message string, keyValuePairs ...any)
	Error(message string, keyValuePairs ...any)
}
