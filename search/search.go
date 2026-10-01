// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/indexer"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

// ErrSearchUnavailable: deferred reindex owns the vector index (no ANN yet).
var ErrSearchUnavailable = errors.New("semantic search is temporarily unavailable during reindexing")

// RAGResult represents an enriched search result with metadata
type RAGResult struct {
	PostID      string  `json:"postId"`
	ChannelID   string  `json:"channelId"`
	ChannelName string  `json:"channelName"`
	TeamName    string  `json:"teamName"`
	UserID      string  `json:"userId"`
	Username    string  `json:"username"`
	Content     string  `json:"content"`
	Score       float32 `json:"score"`
	CreateAt    int64   `json:"createAt"` // Post creation timestamp (Unix millis)
}

// Options configures a search operation
type Options struct {
	Limit     int
	Offset    int
	TeamID    string
	ChannelID string
	UserID    string
}

type Search struct {
	getSearch      func() embeddings.EmbeddingSearch
	mmclient       mmapi.Client
	licenseChecker *enterprise.LicenseChecker
}

func New(
	getSearch func() embeddings.EmbeddingSearch,
	mmclient mmapi.Client,
	licenseChecker *enterprise.LicenseChecker,
) *Search {
	return &Search{
		getSearch:      getSearch,
		mmclient:       mmclient,
		licenseChecker: licenseChecker,
	}
}

// Enabled returns true if the search service is enabled, functional, and
// available at the current license level. Semantic AI search is available at
// Enterprise and above; a nil checker fails closed.
func (s *Search) Enabled() bool {
	if s == nil || s.getSearch == nil || s.getSearch() == nil {
		return false
	}
	return s.licenseChecker.Allows(enterprise.CapSemanticSearch)
}

func (s *Search) checkLicense() error {
	var checker *enterprise.LicenseChecker
	if s != nil {
		checker = s.licenseChecker
	}
	return checker.Check(enterprise.CapSemanticSearch)
}

// checkAvailability gates search while the ANN index is dropped/building.
func (s *Search) checkAvailability() error {
	if s.mmclient != nil && indexer.DeferredIndexRebuildActive(s.mmclient) {
		return ErrSearchUnavailable
	}
	return nil
}

// enrichResults converts raw search results to RAGResults with channel/user metadata.
func (s *Search) enrichResults(searchResults []embeddings.SearchResult) []RAGResult {
	var ragResults []RAGResult
	for _, result := range searchResults {
		// Get channel name and team name
		var channelName, teamName string
		channel, chErr := s.mmclient.GetChannel(result.Document.ChannelID)
		if chErr != nil {
			s.mmclient.LogWarn("Failed to get channel", "error", chErr, "channelID", result.Document.ChannelID)
			channelName = "Unknown Channel"
		} else {
			switch channel.Type {
			case model.ChannelTypeDirect:
				channelName = "Direct Message"
			case model.ChannelTypeGroup:
				channelName = "Group Message"
			default:
				channelName = channel.DisplayName
			}
			if channel.TeamId != "" {
				if team, err := s.mmclient.GetTeam(channel.TeamId); err == nil {
					teamName = team.Name
				}
			}
		}

		// Get username
		var username string
		user, userErr := s.mmclient.GetUser(result.Document.UserID)
		if userErr != nil {
			s.mmclient.LogWarn("Failed to get user", "error", userErr, "userID", result.Document.UserID)
			username = "Unknown User"
		} else {
			username = user.Username
		}

		// Determine the correct content to show
		content := result.Document.Content

		// Handle additional metadata for chunks
		var chunkInfo string
		if result.Document.IsChunk {
			chunkInfo = fmt.Sprintf(" (Chunk %d of %d)",
				result.Document.ChunkIndex+1,
				result.Document.TotalChunks)
		}

		ragResults = append(ragResults, RAGResult{
			PostID:      result.Document.PostID,
			ChannelID:   result.Document.ChannelID,
			ChannelName: channelName + chunkInfo,
			TeamName:    teamName,
			UserID:      result.Document.UserID,
			Username:    username,
			Content:     content,
			Score:       result.Score,
			CreateAt:    result.Document.CreateAt,
		})
	}

	return ragResults
}

// Search performs the embedding search and enriches results with channel/user
// metadata. This is the core search operation without any LLM concerns.
func (s *Search) Search(ctx context.Context, query string, opts Options) ([]RAGResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	if err := s.checkLicense(); err != nil {
		return nil, err
	}

	search := s.getSearch()
	if search == nil {
		return nil, fmt.Errorf("embedding search not configured")
	}

	if err := s.checkAvailability(); err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit == 0 {
		limit = 5
	}

	searchOpts := embeddings.SearchOptions{
		Limit:     limit,
		Offset:    opts.Offset,
		TeamID:    opts.TeamID,
		ChannelID: opts.ChannelID,
		UserID:    opts.UserID,
	}

	searchResults, err := search.Search(ctx, query, searchOpts)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	return s.enrichResults(searchResults), nil
}
