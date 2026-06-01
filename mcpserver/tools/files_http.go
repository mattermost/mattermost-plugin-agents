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
	"time"

	"github.com/mattermost/mattermost-plugin-agents/files"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
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
	bodyBytes, err := json.Marshal(httpFileContentRequest{FileID: fileID, Offset: offset, Limit: limit})
	if err != nil {
		return files.Content{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := s.pluginURL + "/api/v1/files/content"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return files.Content{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token, ok := ctx.Value(auth.AuthTokenContextKey).(string); ok && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ctxUserID, ok := ctx.Value(auth.UserIDContextKey).(string); ok && ctxUserID != "" {
		req.Header.Set("Mattermost-User-Id", ctxUserID)
	} else if userID != "" {
		req.Header.Set("Mattermost-User-Id", userID)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return files.Content{}, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return files.Content{}, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusForbidden {
		return files.Content{}, files.ErrForbidden
	}
	if resp.StatusCode != http.StatusOK {
		var errResp httpFileContentResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return files.Content{}, fmt.Errorf("read file failed: %s", errResp.Error)
		}
		return files.Content{}, fmt.Errorf("read file failed with status %d: %s", resp.StatusCode, string(respBody))
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
