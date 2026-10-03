// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/search"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
)

// SearchRequest is a search started from the search bar or /ask-channel.
type SearchRequest struct {
	Query string `json:"query"`
	// TeamID is the user's current team, used for citation links.
	TeamID string `json:"teamId"`
	// ChannelID is the channel the agent searches first.
	ChannelID string `json:"channelId"`
}

const maxSearchQueryLength = 4000

// handleRunSearch posts the query to the user's DM with the agent and starts
// a conversation that answers it by searching Mattermost with tools.
func (a *API) handleRunSearch(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	// Search runs a full agent conversation, so the agent+service usage gate
	// applies (user-level only: the conversation happens in the agent DM).
	if err := a.bots.CheckUsageRestrictionsForUser(c.Request.Context(), bot, userID); err != nil {
		c.AbortWithError(http.StatusForbidden, err)
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
	if len(req.Query) > maxSearchQueryLength {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("query exceeds maximum length of %d characters", maxSearchQueryLength))
		return
	}

	searchReq := conversations.SearchRequest{Query: req.Query}

	if req.TeamID != "" {
		if !model.IsValidId(req.TeamID) {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid team ID"))
			return
		}
		if !a.pluginAPI.User.HasPermissionToTeam(userID, req.TeamID, model.PermissionViewTeam) {
			c.AbortWithError(http.StatusForbidden, fmt.Errorf("user doesn't have permission to view the team"))
			return
		}
		team, err := a.pluginAPI.Team.Get(req.TeamID)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get team: %w", err))
			return
		}
		searchReq.Team = team
	}

	if req.ChannelID != "" {
		if !model.IsValidId(req.ChannelID) {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid channel ID"))
			return
		}
		if !a.pluginAPI.User.HasPermissionToChannel(userID, req.ChannelID, model.PermissionReadChannel) {
			c.AbortWithError(http.StatusForbidden, fmt.Errorf("user doesn't have permission to read the channel"))
			return
		}
		channel, err := a.pluginAPI.Channel.Get(req.ChannelID)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get channel: %w", err))
			return
		}
		searchReq.Channel = channel
	}

	user, err := a.pluginAPI.User.Get(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("unable to get user: %w", err))
		return
	}

	// The answer is generated after this request returns.
	ctx := auth.WithSessionID(
		telemetry.DetachContext(c.Request.Context()),
		auth.SessionIDFromContext(c.Request.Context()),
	)
	questionPost, err := a.conversationsService.HandleSearch(ctx, bot, user, searchReq)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, map[string]string{
		"postid":    questionPost.Id,
		"channelid": questionPost.ChannelId,
	})
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
	CreateAt    int64   `json:"create_at"` // Post creation timestamp (Unix millis)
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
	if len(req.Query) > maxSearchQueryLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("query exceeds maximum length of %d characters", maxSearchQueryLength)})
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultRawSearchLimit
	}
	if limit > maxRawSearchLimit {
		limit = maxRawSearchLimit
	}

	offset := max(req.Offset, 0)

	results, err := a.searchService.Search(c.Request.Context(), req.Query, search.Options{
		Limit:     limit,
		Offset:    offset,
		TeamID:    req.TeamID,
		ChannelID: req.ChannelID,
		UserID:    userID,
	})
	if err != nil {
		var licErr *enterprise.LicenseError
		if errors.As(err, &licErr) {
			abortNotLicensed(c, err)
			return
		}
		if errors.Is(err, search.ErrSearchUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
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
			CreateAt:    r.CreateAt,
		})
	}

	c.JSON(http.StatusOK, response)
}
