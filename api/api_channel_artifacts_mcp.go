// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost/server/public/model"
)

// MCPListChannelArtifactsRequest is the body for POST /channel-artifacts/mcp/list.
type MCPListChannelArtifactsRequest struct {
	ChannelID string `json:"channel_id"`
}

// MCPGetChannelArtifactRequest is the body for POST /channel-artifacts/mcp/get.
type MCPGetChannelArtifactRequest struct {
	ArtifactID     string `json:"artifact_id"`
	IncludeContent *bool  `json:"include_content,omitempty"`
	ContentLimit   int    `json:"content_limit,omitempty"`
}

// MCPChannelArtifactEntry is catalog metadata without HTML body.
type MCPChannelArtifactEntry struct {
	ID                     string `json:"id"`
	ChannelID              string `json:"channel_id"`
	TeamID                 string `json:"team_id"`
	Title                  string `json:"title"`
	Description            string `json:"description,omitempty"`
	Version                int    `json:"version"`
	SHA256                 string `json:"sha256"`
	Size                   int64  `json:"size"`
	SourcePostID           string `json:"source_post_id"`
	SourceFileID           string `json:"source_file_id"`
	SourceFileName         string `json:"source_file_name,omitempty"`
	SourceBotUserID        string `json:"source_bot_user_id,omitempty"`
	SourceAgentDisplayName string `json:"source_agent_display_name,omitempty"`
	CreateAt               int64  `json:"create_at"`
	UpdateAt               int64  `json:"update_at"`
	UpdatedBy              string `json:"updated_by,omitempty"`
	CreatedBy              string `json:"created_by,omitempty"`
}

// MCPListChannelArtifactsResponse is returned by the MCP list callback.
type MCPListChannelArtifactsResponse struct {
	Entries []MCPChannelArtifactEntry `json:"entries"`
	Error   string                    `json:"error,omitempty"`
}

// MCPGetChannelArtifactResponse is returned by the MCP get callback.
type MCPGetChannelArtifactResponse struct {
	Entry           MCPChannelArtifactEntry `json:"entry"`
	ContentIncluded bool                    `json:"content_included"`
	Content         string                  `json:"content,omitempty"`
	TotalRunes      int                     `json:"total_runes,omitempty"`
	Returned        int                     `json:"returned,omitempty"`
	HasMore         bool                    `json:"has_more,omitempty"`
	Error           string                  `json:"error,omitempty"`
}

func mcpEntryFrom(e *channelartifacts.Entry) MCPChannelArtifactEntry {
	if e == nil {
		return MCPChannelArtifactEntry{}
	}
	return MCPChannelArtifactEntry{
		ID:                     e.ID,
		ChannelID:              e.ChannelID,
		TeamID:                 e.TeamID,
		Title:                  e.Title,
		Description:            e.Description,
		Version:                e.Version,
		SHA256:                 e.SHA256,
		Size:                   e.Size,
		SourcePostID:           e.SourcePostID,
		SourceFileID:           e.SourceFileID,
		SourceFileName:         e.SourceFileName,
		SourceBotUserID:        e.SourceBotUserID,
		SourceAgentDisplayName: e.SourceAgentDisplayName,
		CreateAt:               e.CreateAt,
		UpdateAt:               e.UpdateAt,
		UpdatedBy:              e.UpdatedBy,
		CreatedBy:              e.CreatedBy,
	}
}

// handleMCPListChannelArtifacts handles POST /channel-artifacts/mcp/list.
// Used by MCP servers (embedded and external) to list channel library artifacts.
func (a *API) handleMCPListChannelArtifacts(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")

	userID := c.GetHeader("Mattermost-User-Id")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.JSON(http.StatusServiceUnavailable, MCPListChannelArtifactsResponse{Error: "channel artifacts are not available"})
		return
	}

	var req MCPListChannelArtifactsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, MCPListChannelArtifactsResponse{Error: "invalid request body"})
		return
	}
	if !model.IsValidId(req.ChannelID) {
		c.JSON(http.StatusBadRequest, MCPListChannelArtifactsResponse{Error: "a valid channel_id is required"})
		return
	}

	list, err := a.mcpChannelArtifacts().List(c.Request.Context(), userID, req.ChannelID)
	if err != nil {
		a.writeMCPChannelArtifactError(c, err)
		return
	}

	entries := make([]MCPChannelArtifactEntry, 0, len(list))
	for _, e := range list {
		entries = append(entries, mcpEntryFrom(e))
	}
	c.JSON(http.StatusOK, MCPListChannelArtifactsResponse{Entries: entries})
}

// handleMCPGetChannelArtifact handles POST /channel-artifacts/mcp/get.
// Returns metadata and optionally truncated HTML text for LLM context.
func (a *API) handleMCPGetChannelArtifact(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")

	userID := c.GetHeader("Mattermost-User-Id")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.JSON(http.StatusServiceUnavailable, MCPGetChannelArtifactResponse{Error: "channel artifacts are not available"})
		return
	}

	var req MCPGetChannelArtifactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, MCPGetChannelArtifactResponse{Error: "invalid request body"})
		return
	}
	if !model.IsValidId(req.ArtifactID) {
		c.JSON(http.StatusBadRequest, MCPGetChannelArtifactResponse{Error: "a valid artifact_id is required"})
		return
	}

	includeContent := true
	if req.IncludeContent != nil {
		includeContent = *req.IncludeContent
	}

	got, err := a.mcpChannelArtifacts().Get(c.Request.Context(), userID, req.ArtifactID, includeContent, req.ContentLimit)
	if err != nil {
		a.writeMCPChannelArtifactError(c, err)
		return
	}
	c.JSON(http.StatusOK, MCPGetChannelArtifactResponse{
		Entry:           mcpEntryFrom(got.Entry),
		ContentIncluded: got.ContentIncluded,
		Content:         got.Content,
		TotalRunes:      got.TotalRunes,
		Returned:        got.Returned,
		HasMore:         got.HasMore,
	})
}

// MCPCanSaveChannelArtifactRequest is the body for POST /channel-artifacts/mcp/can-save.
type MCPCanSaveChannelArtifactRequest struct {
	PostID string `json:"post_id"`
	FileID string `json:"file_id"`
}

// handleMCPCanSaveChannelArtifact reports whether the file may be saved into the
// channel library. The propose tool calls this before posting so the human-authored
// restriction and the HTML check live in the plugin, next to the save route.
func (a *API) handleMCPCanSaveChannelArtifact(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")

	userID := c.GetHeader("Mattermost-User-Id")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil || a.mmClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "channel artifacts are not available"})
		return
	}

	var req MCPCanSaveChannelArtifactRequest
	if err := c.ShouldBindJSON(&req); err != nil || !model.IsValidId(req.PostID) || !model.IsValidId(req.FileID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a valid post_id and file_id are required"})
		return
	}

	if err := a.mcpChannelArtifacts().CanSave(c.Request.Context(), userID, req.PostID, req.FileID); err != nil {
		a.writeMCPChannelArtifactError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *API) writeMCPChannelArtifactError(c *gin.Context, err error) {
	if statusErr, ok := errors.AsType[*mcpStatusError](err); ok {
		c.JSON(statusErr.status, gin.H{"error": statusErr.message})
		return
	}
	switch {
	case errors.Is(err, errChannelArtifactsUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	case errors.Is(err, channelartifacts.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "channel artifact not found"})
	case errors.Is(err, channelartifacts.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
	default:
		if a.pluginAPI != nil {
			a.pluginAPI.Log.Error("channel artifact MCP operation failed", "error", err)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
