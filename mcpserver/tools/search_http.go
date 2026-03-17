// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-ai/search"
)

// HTTPSemanticSearchService provides semantic search by calling back to the plugin API.
// This allows external MCP servers (HTTP, Stdio) to access semantic search capabilities.
type HTTPSemanticSearchService struct {
	pluginURL string
	client    *http.Client
}

// NewHTTPSemanticSearchService creates a new HTTP-based semantic search service.
// pluginURL should be the base URL to the plugin, e.g., "https://mattermost.example.com/plugins/mattermost-ai"
func NewHTTPSemanticSearchService(pluginURL string) *HTTPSemanticSearchService {
	return &HTTPSemanticSearchService{
		pluginURL: pluginURL,
		client: &http.Client{
			Timeout: 30_000_000_000, // 30 seconds in nanoseconds
		},
	}
}

// Enabled returns true since this service is always available when created.
// The actual availability check happens at the plugin endpoint.
func (s *HTTPSemanticSearchService) Enabled() bool {
	return true
}

// httpSearchRequest represents the request body sent to the plugin endpoint
type httpSearchRequest struct {
	Query     string `json:"query"`
	TeamID    string `json:"team_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

// httpSearchResult represents a single result from the plugin endpoint
type httpSearchResult struct {
	PostID      string  `json:"post_id"`
	ChannelID   string  `json:"channel_id"`
	ChannelName string  `json:"channel_name"`
	UserID      string  `json:"user_id"`
	Username    string  `json:"username"`
	Content     string  `json:"content"`
	Score       float32 `json:"score"`
}

// httpSearchResponse represents the response from the plugin endpoint
type httpSearchResponse struct {
	Results []httpSearchResult `json:"results"`
	Error   string             `json:"error,omitempty"`
}

// Search performs a semantic search by calling the plugin's MCP semantic search endpoint
func (s *HTTPSemanticSearchService) Search(ctx context.Context, query string, opts search.Options) ([]search.RAGResult, error) {
	// Build request body
	reqBody := httpSearchRequest{
		Query:     query,
		TeamID:    opts.TeamID,
		ChannelID: opts.ChannelID,
		Limit:     opts.Limit,
		Offset:    opts.Offset,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := s.pluginURL + "/api/v1/search/raw"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Extract auth token from context and add to request
	if token, ok := ctx.Value(auth.AuthTokenContextKey).(string); ok && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Extract user ID from context (for session-based auth)
	if userID, ok := ctx.Value(auth.UserIDContextKey).(string); ok && userID != "" {
		req.Header.Set("Mattermost-User-Id", userID)
	}

	// Execute request
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle non-200 responses
	if resp.StatusCode != http.StatusOK {
		var errResp httpSearchResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("search failed: %s", errResp.Error)
		}
		return nil, fmt.Errorf("search failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse successful response
	var searchResp httpSearchResponse
	if err := json.Unmarshal(respBody, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert HTTP DTOs to search.RAGResult
	results := make([]search.RAGResult, 0, len(searchResp.Results))
	for _, r := range searchResp.Results {
		results = append(results, search.RAGResult{
			PostID:      r.PostID,
			ChannelID:   r.ChannelID,
			ChannelName: r.ChannelName,
			UserID:      r.UserID,
			Username:    r.Username,
			Content:     r.Content,
			Score:       r.Score,
		})
	}

	return results, nil
}
