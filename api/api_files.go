// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost/server/public/model"
)

// RawFileContentRequest is the request body for the raw file content endpoint.
type RawFileContentRequest struct {
	FileID string `json:"file_id"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// RawFileContentResponse is the response body for the raw file content endpoint.
type RawFileContentResponse struct {
	Name       string `json:"name"`
	MimeType   string `json:"mime_type"`
	TotalRunes int    `json:"total_runes"`
	Offset     int    `json:"offset"`
	Returned   int    `json:"returned"`
	HasMore    bool   `json:"has_more"`
	HasText    bool   `json:"has_text"`
	Text       string `json:"text"`
}

// handleRawFileContent handles the POST /files/content endpoint.
// It returns a ranged slice of a file's text after verifying the requesting
// user's channel permission. Used by the MCP server for external read_file
// callbacks; the requesting user is taken from the authenticated header, never
// from the request body.
func (a *API) handleRawFileContent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	if a.fileService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "file content service is not available"})
		return
	}

	var req RawFileContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if !model.IsValidId(req.FileID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a valid file_id is required"})
		return
	}

	content, err := a.fileService.GetContent(c.Request.Context(), userID, req.FileID, req.Offset, req.Limit)
	if err != nil {
		if errors.Is(err, files.ErrForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have permission to access this file"})
			return
		}
		a.pluginAPI.Log.Error("Raw file content read failed", "error", err, "user_id", userID, "file_id", req.FileID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	c.JSON(http.StatusOK, RawFileContentResponse{
		Name:       content.Name,
		MimeType:   content.MimeType,
		TotalRunes: content.TotalRunes,
		Offset:     content.Offset,
		Returned:   content.Returned,
		HasMore:    content.HasMore,
		HasText:    content.HasText,
		Text:       content.Text,
	})
}
