// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/files"
)

// HTTPFileContentService reads file contents by calling back to the plugin API.
// This allows external MCP servers (HTTP, Stdio) to read attachments while the
// permission check and admin-API extraction stay inside the plugin.
type HTTPFileContentService struct {
	pluginURL string
	client    *http.Client
}

// NewHTTPFileContentService creates a new HTTP-based file content service.
// pluginURL should be the base URL to the plugin, e.g., "https://mattermost.example.com/plugins/mattermost-ai"
func NewHTTPFileContentService(pluginURL string) *HTTPFileContentService {
	return &HTTPFileContentService{
		pluginURL: pluginURL,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// httpFileContentRequest is the request body sent to the plugin endpoint.
type httpFileContentRequest struct {
	FileID string `json:"file_id"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// httpFileContentResponse is the response from the plugin endpoint.
type httpFileContentResponse struct {
	Name       string `json:"name"`
	MimeType   string `json:"mime_type"`
	TotalRunes int    `json:"total_runes"`
	Offset     int    `json:"offset"`
	Returned   int    `json:"returned"`
	HasMore    bool   `json:"has_more"`
	HasText    bool   `json:"has_text"`
	Text       string `json:"text"`
	Error      string `json:"error,omitempty"`
}

// GetContent reads a ranged slice of a file's text via the plugin's endpoint.
func (s *HTTPFileContentService) GetContent(ctx context.Context, userID, fileID string, offset, limit int) (files.Content, error) {
	reqBody := httpFileContentRequest{FileID: fileID, Offset: offset, Limit: limit}
	status, respBody, err := postPluginJSON(ctx, s.client, s.pluginURL+"/api/v1/files/content", reqBody, userID)
	if err != nil {
		return files.Content{}, err
	}

	if status == http.StatusForbidden {
		return files.Content{}, files.ErrForbidden
	}
	if status != http.StatusOK {
		var errResp httpFileContentResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return files.Content{}, fmt.Errorf("read file failed: %s", errResp.Error)
		}
		return files.Content{}, fmt.Errorf("read file failed with status %d: %s", status, string(respBody))
	}

	var out httpFileContentResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return files.Content{}, fmt.Errorf("failed to parse response: %w", err)
	}

	return files.Content{
		Name:       out.Name,
		MimeType:   out.MimeType,
		TotalRunes: out.TotalRunes,
		Offset:     out.Offset,
		Returned:   out.Returned,
		HasMore:    out.HasMore,
		HasText:    out.HasText,
		Text:       out.Text,
	}, nil
}
