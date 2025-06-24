// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	SearchResultsProp = "search_results"
	SearchQueryProp   = "search_query"
)

// Request represents a search query request
type Request struct {
	Query      string `json:"query"`
	TeamID     string `json:"teamId"`
	ChannelID  string `json:"channelId"`
	MaxResults int    `json:"maxResults"`
}

// Response represents a response to a search query
type Response struct {
	Answer    string      `json:"answer"`
	Results   []RAGResult `json:"results"`
	PostID    string      `json:"postid,omitempty"`
	ChannelID string      `json:"channelid,omitempty"`
}

// RAGResult represents an enriched search result with metadata
type RAGResult struct {
	PostID      string  `json:"postId"`
	ChannelID   string  `json:"channelId"`
	ChannelName string  `json:"channelName"`
	UserID      string  `json:"userId"`
	Username    string  `json:"username"`
	Content     string  `json:"content"`
	Score       float32 `json:"score"`
}

type Search struct {
	embeddings.EmbeddingSearch
	mmclient         mmapi.Client
	prompts          *llm.Prompts
	streamingService streaming.Service
	licenseChecker   *enterprise.LicenseChecker
}

func New(
	search embeddings.EmbeddingSearch,
	mmclient mmapi.Client,
	prompts *llm.Prompts,
	streamingService streaming.Service,
	licenseChecker *enterprise.LicenseChecker,
) *Search {
	return &Search{
		EmbeddingSearch:  search,
		mmclient:         mmclient,
		prompts:          prompts,
		streamingService: streamingService,
		licenseChecker:   licenseChecker,
	}
}

// convertToRAGResults converts embeddings.EmbeddingSearchResult to RAGResult with enriched metadata
func (s *Search) convertToRAGResults(searchResults []embeddings.SearchResult) []RAGResult {
	var ragResults []RAGResult
	for _, result := range searchResults {
		// Get channel name
		var channelName string
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
			UserID:      result.Document.UserID,
			Username:    username,
			Content:     content,
			Score:       result.Score,
		})
	}

	return ragResults
}

// RunSearch initiates a search and sends results to a DM
func (s *Search) RunSearch(ctx context.Context, userID string, bot *bots.Bot, query, teamID, channelID string, maxResults int) (map[string]string, error) {
	if s.EmbeddingSearch == nil {
		return nil, fmt.Errorf("search functionality is not configured")
	}

	if query == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	// Create the initial question post
	questionPost := &model.Post{
		UserId:  userID,
		Message: query,
	}
	questionPost.AddProp(SearchQueryProp, "true")
	if err := s.mmclient.DM(userID, bot.GetMMBot().UserId, questionPost); err != nil {
		return nil, fmt.Errorf("failed to create question post: %w", err)
	}

	// Start processing the search asynchronously
	go func(query, teamID, channelID string, maxResults int) {
		// Create response post as a reply
		responsePost := &model.Post{
			RootId: questionPost.Id,
		}
		responsePost.AddProp(streaming.NoRegen, "true")

		if err := s.botDMNonResponse(bot.GetMMBot().UserId, userID, responsePost); err != nil {
			// Not much point in retrying if this failed. (very unlikely beyond dev)
			s.mmclient.LogError("Error creating bot DM", "error", err)
			return
		}

		// Setup error handling to update the post on error
		var processingError error
		defer func() {
			if processingError != nil {
				responsePost.Message = "I encountered an error while searching. Please try again later. See server logs for details."
				if err := s.mmclient.UpdatePost(responsePost); err != nil {
					s.mmclient.LogError("Error updating post on error", "error", err)
				}
			}
		}()

		// Perform search
		if maxResults == 0 {
			maxResults = 5
		}

		searchResults, err := s.Search(context.Background(), query, embeddings.SearchOptions{
			Limit:     maxResults,
			TeamID:    teamID,
			ChannelID: channelID,
			UserID:    userID,
		})
		if err != nil {
			s.mmclient.LogError("Error performing search", "error", err)
			processingError = err
			return
		}

		ragResults := s.convertToRAGResults(searchResults)
		if len(ragResults) == 0 {
			responsePost.Message = "I couldn't find any relevant messages for your query. Please try a different search term."
			if updateErr := s.mmclient.UpdatePost(responsePost); updateErr != nil {
				s.mmclient.LogError("Error updating post on error", "error", updateErr)
			}
			return
		}

		// Create context for generating answer
		promptCtx := llm.NewContext()
		promptCtx.Parameters = map[string]interface{}{
			"Query":   query,
			"Results": ragResults,
		}

		systemMessage, err := s.prompts.Format("search_system", promptCtx)
		if err != nil {
			s.mmclient.LogError("Error formatting system message", "error", err)
			processingError = err
			return
		}

		prompt := llm.CompletionRequest{
			Posts: []llm.Post{
				{
					Role:    llm.PostRoleSystem,
					Message: systemMessage,
				},
				{
					Role:    llm.PostRoleUser,
					Message: query,
				},
			},
			Context: promptCtx,
		}

		resultStream, err := bot.LLM().ChatCompletion(prompt)
		if err != nil {
			s.mmclient.LogError("Error generating answer", "error", err)
			processingError = err
			return
		}

		resultsJSON, err := json.Marshal(ragResults)
		if err != nil {
			s.mmclient.LogError("Error marshaling results", "error", err)
			processingError = err
			return
		}

		// Update post to add sources
		responsePost.AddProp(SearchResultsProp, string(resultsJSON))
		if updateErr := s.mmclient.UpdatePost(responsePost); updateErr != nil {
			s.mmclient.LogError("Error updating post for search results", "error", updateErr)
			processingError = updateErr
			return
		}

		streamContext, err := s.streamingService.GetStreamingContext(context.Background(), responsePost.Id)
		if err != nil {
			s.mmclient.LogError("Error getting post streaming context", "error", err)
			processingError = err
			return
		}
		defer s.streamingService.FinishStreaming(responsePost.Id)
		s.streamingService.StreamToPost(streamContext, resultStream, responsePost, "")
	}(query, teamID, channelID, maxResults)

	return map[string]string{
		"postid":    questionPost.Id,
		"channelid": questionPost.ChannelId,
	}, nil
}

// SearchQuery performs a search and returns results immediately
func (s *Search) SearchQuery(ctx context.Context, userID string, bot *bots.Bot, query, teamID, channelID string, maxResults int) (Response, error) {
	if s.EmbeddingSearch == nil {
		return Response{}, fmt.Errorf("search functionality is not configured")
	}

	if maxResults == 0 {
		maxResults = 5
	}

	// Search for relevant posts using embeddings
	searchResults, err := s.Search(ctx, query, embeddings.SearchOptions{
		Limit:     maxResults,
		TeamID:    teamID,
		ChannelID: channelID,
		UserID:    userID,
	})
	if err != nil {
		return Response{}, fmt.Errorf("search failed: %w", err)
	}

	ragResults := s.convertToRAGResults(searchResults)
	if len(ragResults) == 0 {
		return Response{
			Answer:  "I couldn't find any relevant messages for your query. Please try a different search term.",
			Results: []RAGResult{},
		}, nil
	}

	promptCtx := llm.NewContext()
	promptCtx.Parameters = map[string]interface{}{
		"Query":   query,
		"Results": ragResults,
	}

	systemMessage, err := s.prompts.Format("search_system", promptCtx)
	if err != nil {
		return Response{}, fmt.Errorf("failed to format system message: %w", err)
	}

	prompt := llm.CompletionRequest{
		Posts: []llm.Post{
			{
				Role:    llm.PostRoleSystem,
				Message: systemMessage,
			},
			{
				Role:    llm.PostRoleUser,
				Message: query,
			},
		},
		Context: promptCtx,
	}

	answer, err := bot.LLM().ChatCompletionNoStream(prompt)
	if err != nil {
		return Response{}, fmt.Errorf("failed to generate answer: %w", err)
	}

	return Response{
		Answer:  answer,
		Results: ragResults,
	}, nil
}

func (s *Search) botDMNonResponse(botid string, userID string, post *model.Post) error {
	streaming.ModifyPostForBot(botid, userID, post, "")

	if err := s.mmclient.DM(botid, userID, post); err != nil {
		return fmt.Errorf("failed to post DM: %w", err)
	}

	return nil
}
