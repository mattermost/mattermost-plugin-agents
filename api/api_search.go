// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/search"
)

// SearchRequest represents a search query request from the API
type SearchRequest struct {
	Query      string `json:"query"`
	TeamID     string `json:"teamId"`
	ChannelID  string `json:"channelId"`
	MaxResults int    `json:"maxResults"`
}

const (
	defaultMaxResults = 5
	maxMaxResults     = 100
)

func (a *API) handleRunSearch(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if !a.searchService.Enabled() {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("search functionality is not configured"))
		return
	}

	var req SearchRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return
	}

	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("query cannot be empty"))
		return
	}

	// Validate MaxResults
	if req.MaxResults <= 0 {
		req.MaxResults = defaultMaxResults
	} else if req.MaxResults > maxMaxResults {
		req.MaxResults = maxMaxResults
	}

	result, err := a.searchService.RunSearch(c.Request.Context(), userID, bot, req.Query, req.TeamID, req.ChannelID, req.MaxResults)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

func (a *API) handleSearchQuery(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if !a.searchService.Enabled() {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("search functionality is not configured"))
		return
	}

	var req SearchRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return
	}

	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("query cannot be empty"))
		return
	}

	// Validate MaxResults
	if req.MaxResults <= 0 {
		req.MaxResults = defaultMaxResults
	} else if req.MaxResults > maxMaxResults {
		req.MaxResults = maxMaxResults
	}

	response, err := a.searchService.SearchQuery(c.Request.Context(), userID, bot, req.Query, req.TeamID, req.ChannelID, req.MaxResults)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// RawSearchRequest represents the request body for the raw semantic search endpoint
type RawSearchRequest struct {
	Query     string `json:"query"`
	TeamID    string `json:"team_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

// RawSearchResult represents a single raw semantic search result
type RawSearchResult struct {
	PostID      string  `json:"post_id"`
	ChannelID   string  `json:"channel_id"`
	ChannelName string  `json:"channel_name"`
	UserID      string  `json:"user_id"`
	Username    string  `json:"username"`
	Content     string  `json:"content"`
	Score       float32 `json:"score"`
}

// RawSearchResponse represents the response body for the raw semantic search endpoint
type RawSearchResponse struct {
	Results []RawSearchResult `json:"results"`
}

const (
	defaultRawSearchLimit = 10
	maxRawSearchLimit     = 50
)

// handleRawSearch handles the POST /search/raw endpoint.
// Returns enriched semantic search results without LLM processing.
// Used by the MCP server for external search callbacks.
func (a *API) handleRawSearch(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	// Check if search is enabled
	if a.searchService == nil || !a.searchService.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search is not available"})
		return
	}

	var req RawSearchRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultRawSearchLimit
	}
	if limit > maxRawSearchLimit {
		limit = maxRawSearchLimit
	}

	offset := req.Offset
	if offset < 0 {
		offset = 0
	}

	results, err := a.searchService.Search(c.Request.Context(), req.Query, search.Options{
		Limit:     limit,
		Offset:    offset,
		TeamID:    req.TeamID,
		ChannelID: req.ChannelID,
		UserID:    userID,
	})
	if err != nil {
		a.pluginAPI.Log.Error("Raw search failed", "error", err, "user_id", userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
	}

	response := RawSearchResponse{
		Results: make([]RawSearchResult, 0, len(results)),
	}

	for _, r := range results {
		response.Results = append(response.Results, RawSearchResult{
			PostID:      r.PostID,
			ChannelID:   r.ChannelID,
			ChannelName: r.ChannelName,
			UserID:      r.UserID,
			Username:    r.Username,
			Content:     r.Content,
			Score:       r.Score,
		})
	}

	c.JSON(http.StatusOK, response)
}
