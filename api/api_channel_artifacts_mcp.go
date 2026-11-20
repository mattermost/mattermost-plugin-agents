// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost/server/public/model"
)

// handleMCPListChannelArtifacts handles POST /channel-artifacts/mcp/list.
// External MCP servers (HTTP and stdio) call it; the plugin's own servers use
// MCPChannelArtifacts directly.
func (a *API) handleMCPListChannelArtifacts(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")

	var req tools.ChannelArtifactListRequest
	if !bindChannelArtifactJSON(c, &req) {
		return
	}
	if !model.IsValidId(req.ChannelID) {
		c.JSON(http.StatusBadRequest, tools.ChannelArtifactListResponse{Error: "a valid channel_id is required"})
		return
	}
	entries, err := a.mcpChannelArtifacts().List(c.Request.Context(), c.GetHeader("Mattermost-User-Id"), req.ChannelID)
	if err != nil {
		a.writeMCPChannelArtifactError(c, err)
		return
	}
	if entries == nil {
		entries = []*channelartifacts.Entry{}
	}
	c.JSON(http.StatusOK, tools.ChannelArtifactListResponse{Entries: entries})
}

// handleMCPGetChannelArtifact handles POST /channel-artifacts/mcp/get. It
// returns metadata and, unless include_content is false, a window of the HTML.
func (a *API) handleMCPGetChannelArtifact(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")

	var req tools.ChannelArtifactGetRequest
	if !bindChannelArtifactJSON(c, &req) {
		return
	}
	if !model.IsValidId(req.ArtifactID) {
		c.JSON(http.StatusBadRequest, tools.ChannelArtifactGetResponse{Error: "a valid artifact_id is required"})
		return
	}
	got, err := a.mcpChannelArtifacts().Get(c.Request.Context(), c.GetHeader("Mattermost-User-Id"), req.ArtifactID, tools.ChannelArtifactReadOptions{
		IncludeContent: req.IncludeContent == nil || *req.IncludeContent,
		Offset:         req.Offset,
		Limit:          req.ContentLimit,
		Version:        req.Version,
	})
	if err != nil {
		a.writeMCPChannelArtifactError(c, err)
		return
	}
	c.JSON(http.StatusOK, tools.ChannelArtifactGetResponse{ChannelArtifactContent: got})
}

func (a *API) writeMCPChannelArtifactError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errChannelArtifactsUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	case errors.Is(err, channelartifacts.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "channel artifact not found"})
	case errors.Is(err, channelartifacts.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "you can't read this channel artifact"})
	case errors.Is(err, channelartifacts.ErrSessionRequired):
		c.JSON(http.StatusForbidden, gin.H{"error": "a session is required to read the artifact's content"})
	case errors.Is(err, channelartifacts.ErrUnavailable):
		c.JSON(http.StatusGone, gin.H{"error": "the source message or file is no longer available"})
	case errors.Is(err, channelartifacts.ErrTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "artifact too large"})
	case errors.Is(err, channelartifacts.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
	default:
		if a.pluginAPI != nil {
			a.pluginAPI.Log.Error("channel artifact MCP operation failed", "error", err)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
