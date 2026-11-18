// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
)

// HTTPChannelArtifactsService lists and reads channel library artifacts by
// calling back to the plugin API, for MCP servers that run outside the plugin
// (HTTP and stdio). Permission checks stay inside the plugin.
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

// ChannelArtifactListRequest is the body of POST /channel-artifacts/mcp/list.
type ChannelArtifactListRequest struct {
	ChannelID string `json:"channel_id"`
}

// ChannelArtifactListResponse is the response of POST /channel-artifacts/mcp/list.
type ChannelArtifactListResponse struct {
	Entries []*channelartifacts.Entry `json:"entries"`
	Error   string                    `json:"error,omitempty"`
}

// ChannelArtifactGetRequest is the body of POST /channel-artifacts/mcp/get.
type ChannelArtifactGetRequest struct {
	ArtifactID     string `json:"artifact_id"`
	IncludeContent *bool  `json:"include_content,omitempty"`
	Offset         int    `json:"offset,omitempty"`
	ContentLimit   int    `json:"content_limit,omitempty"`
	Version        int    `json:"version,omitempty"`
}

// ChannelArtifactGetResponse is the response of POST /channel-artifacts/mcp/get.
type ChannelArtifactGetResponse struct {
	ChannelArtifactContent
	Error string `json:"error,omitempty"`
}

// Enabled is always true: only the plugin knows whether the library is on, and
// it answers 503 when it is not.
func (s *HTTPChannelArtifactsService) Enabled() bool { return true }

// callbackError maps a non-200 callback response to the library's errors.
func callbackError(status int, errText string) error {
	switch status {
	case http.StatusNotFound:
		return channelartifacts.ErrNotFound
	case http.StatusGone:
		return channelartifacts.ErrUnavailable
	case http.StatusServiceUnavailable:
		return errors.New("channel artifacts are not available")
	}
	if errText != "" {
		return fmt.Errorf("channel artifact request failed: %s", errText)
	}
	return fmt.Errorf("channel artifact request failed with status %d", status)
}

// List returns channel library artifact metadata for the given channel.
func (s *HTTPChannelArtifactsService) List(ctx context.Context, userID, channelID string) ([]*channelartifacts.Entry, error) {
	status, body, err := postPluginJSON(ctx, s.client, s.pluginURL+ChannelArtifactsListPath, ChannelArtifactListRequest{ChannelID: channelID}, userID)
	if err != nil {
		return nil, err
	}
	var out ChannelArtifactListResponse
	parseErr := json.Unmarshal(body, &out)
	if status != http.StatusOK {
		return nil, callbackError(status, out.Error)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse response: %w", parseErr)
	}
	return out.Entries, nil
}

// Get returns metadata and optionally a window of the HTML for one artifact.
func (s *HTTPChannelArtifactsService) Get(ctx context.Context, userID, artifactID string, opts ChannelArtifactReadOptions) (ChannelArtifactContent, error) {
	include := opts.IncludeContent
	status, body, err := postPluginJSON(ctx, s.client, s.pluginURL+ChannelArtifactsGetPath, ChannelArtifactGetRequest{
		ArtifactID:     artifactID,
		IncludeContent: &include,
		Offset:         opts.Offset,
		ContentLimit:   opts.Limit,
		Version:        opts.Version,
	}, userID)
	if err != nil {
		return ChannelArtifactContent{}, err
	}
	var out ChannelArtifactGetResponse
	parseErr := json.Unmarshal(body, &out)
	if status != http.StatusOK {
		return ChannelArtifactContent{}, callbackError(status, out.Error)
	}
	if parseErr != nil {
		return ChannelArtifactContent{}, fmt.Errorf("failed to parse response: %w", parseErr)
	}
	return out.ChannelArtifactContent, nil
}
