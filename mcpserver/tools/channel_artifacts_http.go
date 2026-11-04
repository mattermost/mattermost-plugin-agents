// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
)

// HTTPChannelArtifactsService lists and reads channel library artifacts by
// calling back to the plugin API. External MCP servers (HTTP, Stdio) and the
// embedded server share this path so permission checks stay inside the plugin.
type HTTPChannelArtifactsService struct {
	pluginURL string
	client    *http.Client
}

// NewHTTPChannelArtifactsService creates an HTTP-backed channel artifacts service.
// pluginURL should be the base URL to the plugin, e.g. "https://mattermost.example.com/plugins/mattermost-ai"
func NewHTTPChannelArtifactsService(pluginURL string) *HTTPChannelArtifactsService {
	return &HTTPChannelArtifactsService{
		pluginURL: pluginURL,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

type httpChannelArtifactsListRequest struct {
	ChannelID string `json:"channel_id"`
}

type httpChannelArtifactsGetRequest struct {
	ArtifactID     string `json:"artifact_id"`
	IncludeContent *bool  `json:"include_content,omitempty"`
	ContentLimit   int    `json:"content_limit,omitempty"`
}

type httpChannelArtifactEntry struct {
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

type httpChannelArtifactsListResponse struct {
	Entries []httpChannelArtifactEntry `json:"entries"`
	Error   string                     `json:"error,omitempty"`
}

type httpChannelArtifactsGetResponse struct {
	Entry           httpChannelArtifactEntry `json:"entry"`
	ContentIncluded bool                     `json:"content_included"`
	Content         string                   `json:"content,omitempty"`
	TotalRunes      int                      `json:"total_runes,omitempty"`
	Returned        int                      `json:"returned,omitempty"`
	HasMore         bool                     `json:"has_more,omitempty"`
	Error           string                   `json:"error,omitempty"`
}

func entryFromHTTP(e httpChannelArtifactEntry) *channelartifacts.Entry {
	return &channelartifacts.Entry{
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

// List returns channel library artifact metadata for the given channel.
func (s *HTTPChannelArtifactsService) List(ctx context.Context, userID, channelID string) ([]*channelartifacts.Entry, error) {
	reqBody := httpChannelArtifactsListRequest{ChannelID: channelID}
	status, respBody, err := postPluginJSON(ctx, s.client, s.pluginURL+ChannelArtifactsListPath, reqBody, userID)
	if err != nil {
		return nil, err
	}

	if status == http.StatusNotFound {
		return nil, channelartifacts.ErrNotFound
	}
	if status == http.StatusServiceUnavailable {
		var errResp httpChannelArtifactsListResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("channel artifacts are not available")
	}
	if status != http.StatusOK {
		var errResp httpChannelArtifactsListResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("list channel artifacts failed: %s", errResp.Error)
		}
		return nil, fmt.Errorf("list channel artifacts failed with status %d: %s", status, string(respBody))
	}

	var out httpChannelArtifactsListResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	entries := make([]*channelartifacts.Entry, 0, len(out.Entries))
	for _, e := range out.Entries {
		entries = append(entries, entryFromHTTP(e))
	}
	return entries, nil
}

// Get returns metadata and optionally truncated HTML for one library artifact.
func (s *HTTPChannelArtifactsService) Get(ctx context.Context, userID, artifactID string, includeContent bool, contentLimit int) (ChannelArtifactContent, error) {
	include := includeContent
	reqBody := httpChannelArtifactsGetRequest{
		ArtifactID:     artifactID,
		IncludeContent: &include,
		ContentLimit:   contentLimit,
	}
	status, respBody, err := postPluginJSON(ctx, s.client, s.pluginURL+ChannelArtifactsGetPath, reqBody, userID)
	if err != nil {
		return ChannelArtifactContent{}, err
	}

	if status == http.StatusNotFound {
		return ChannelArtifactContent{}, channelartifacts.ErrNotFound
	}
	if status == http.StatusServiceUnavailable {
		var errResp httpChannelArtifactsGetResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return ChannelArtifactContent{}, fmt.Errorf("%s", errResp.Error)
		}
		return ChannelArtifactContent{}, fmt.Errorf("channel artifacts are not available")
	}
	if status != http.StatusOK {
		var errResp httpChannelArtifactsGetResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return ChannelArtifactContent{}, fmt.Errorf("get channel artifact failed: %s", errResp.Error)
		}
		return ChannelArtifactContent{}, fmt.Errorf("get channel artifact failed with status %d: %s", status, string(respBody))
	}

	var out httpChannelArtifactsGetResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return ChannelArtifactContent{}, fmt.Errorf("failed to parse response: %w", err)
	}

	return ChannelArtifactContent{
		Entry:           entryFromHTTP(out.Entry),
		ContentIncluded: out.ContentIncluded,
		Content:         out.Content,
		TotalRunes:      out.TotalRunes,
		Returned:        out.Returned,
		HasMore:         out.HasMore,
	}, nil
}

type httpCanSaveRequest struct {
	PostID string `json:"post_id"`
	FileID string `json:"file_id"`
}

type httpCanSaveResponse struct {
	Error string `json:"error,omitempty"`
}

// CanSave asks the plugin whether this file may be saved into the channel library.
// The plugin enforces HTML and the human-authored restriction; the tool does not.
func (s *HTTPChannelArtifactsService) CanSave(ctx context.Context, userID, postID, fileID string) error {
	status, respBody, err := postPluginJSON(ctx, s.client, s.pluginURL+ChannelArtifactsCanSavePath, httpCanSaveRequest{
		PostID: postID,
		FileID: fileID,
	}, userID)
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		return nil
	}
	var out httpCanSaveResponse
	if json.Unmarshal(respBody, &out) == nil && out.Error != "" {
		return fmt.Errorf("%s", out.Error)
	}
	return fmt.Errorf("this file cannot be saved to the channel library (status %d)", status)
}
