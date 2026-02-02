// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/search"
)

// MCPSemanticSearchRequest represents the request body for the MCP semantic search endpoint
type MCPSemanticSearchRequest struct {
	Query     string `json:"query"`
	TeamID    string `json:"team_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

// MCPSemanticSearchResult represents a single semantic search result
type MCPSemanticSearchResult struct {
	PostID      string  `json:"post_id"`
	ChannelID   string  `json:"channel_id"`
	ChannelName string  `json:"channel_name"`
	UserID      string  `json:"user_id"`
	Username    string  `json:"username"`
	Content     string  `json:"content"`
	Score       float32 `json:"score"`
}

// MCPSemanticSearchResponse represents the response body for the MCP semantic search endpoint
type MCPSemanticSearchResponse struct {
	Results []MCPSemanticSearchResult `json:"results"`
}

// handleMCPSemanticSearch handles the POST /mcp/semantic-search endpoint
// This endpoint allows external MCP servers to perform semantic searches via HTTP callback
func (a *API) handleMCPSemanticSearch(c *gin.Context) {
	userID := c.GetString("userID")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req MCPSemanticSearchRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.Query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}

	// Check if search is enabled
	if a.searchService == nil || !a.searchService.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search is not available"})
		return
	}

	// Set default limit
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	// Execute search with user permissions
	results, err := a.searchService.ExecuteSearchForMCP(c.Request.Context(), req.Query, search.Options{
		Limit:     limit,
		Offset:    req.Offset,
		TeamID:    req.TeamID,
		ChannelID: req.ChannelID,
		UserID:    userID,
	})
	if err != nil {
		a.pluginAPI.Log.Error("MCP semantic search failed", "error", err, "user_id", userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
	}

	// Convert to response format
	response := MCPSemanticSearchResponse{
		Results: make([]MCPSemanticSearchResult, 0, len(results)),
	}

	for _, r := range results {
		response.Results = append(response.Results, MCPSemanticSearchResult{
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
