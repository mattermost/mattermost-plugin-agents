// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	Index       int     `json:"index"` // 1-based index for citation mapping
	PostID      string  `json:"postId"`
	ChannelID   string  `json:"channelId"`
	ChannelName string  `json:"channelName"`
	TeamName    string  `json:"teamName"`
	UserID      string  `json:"userId"`
	Username    string  `json:"username"`
	Content     string  `json:"content"`
	Score       float32 `json:"score"`
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
	getSearch        func() embeddings.EmbeddingSearch
	mmclient         mmapi.Client
	prompts          *llm.Prompts
	streamingService streaming.Service
	licenseChecker   *enterprise.LicenseChecker
}

func New(
	getSearch func() embeddings.EmbeddingSearch,
	mmclient mmapi.Client,
	prompts *llm.Prompts,
	streamingService streaming.Service,
	licenseChecker *enterprise.LicenseChecker,
) *Search {
	return &Search{
		getSearch:        getSearch,
		mmclient:         mmclient,
		prompts:          prompts,
		streamingService: streamingService,
		licenseChecker:   licenseChecker,
	}
}

// Enabled returns true if the search service is enabled and functional
func (s *Search) Enabled() bool {
	return s != nil && s.getSearch != nil && s.getSearch() != nil
}

// Search performs a semantic search and returns enriched results with channel/user metadata.
func (s *Search) Search(ctx context.Context, query string, opts Options) ([]RAGResult, error) {
	return s.executeSearch(ctx, query, opts)
}

// enrichResults converts raw search results to RAGResults with channel/user metadata.
func (s *Search) enrichResults(searchResults []embeddings.SearchResult) []RAGResult {
	var ragResults []RAGResult
	for i, result := range searchResults {
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
			Index:       i + 1, // 1-based index for citation mapping
			PostID:      result.Document.PostID,
			ChannelID:   result.Document.ChannelID,
			ChannelName: channelName + chunkInfo,
			TeamName:    teamName,
			UserID:      result.Document.UserID,
			Username:    username,
			Content:     content,
			Score:       result.Score,
		})
	}

	return ragResults
}

// executeSearch performs the embedding search and enriches results with metadata.
// This is the core search operation without any LLM concerns.
func (s *Search) executeSearch(ctx context.Context, query string, opts Options) ([]RAGResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	search := s.getSearch()
	if search == nil {
		return nil, fmt.Errorf("embedding search not configured")
	}

	limit := opts.Limit
	if limit == 0 {
		limit = 5
	}

	searchResults, err := search.Search(ctx, query, embeddings.SearchOptions{
		Limit:     limit,
		Offset:    opts.Offset,
		TeamID:    opts.TeamID,
		ChannelID: opts.ChannelID,
		UserID:    opts.UserID,
	})
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	return s.enrichResults(searchResults), nil
}

func (s *Search) buildSearchPromptContext(userID string, bot *bots.Bot, query string, teamID, channelID string, ragResults []RAGResult) *llm.Context {
	promptCtx := llm.NewContext()
	promptCtx.RequestingUser = &model.User{Id: userID}
	if channelID != "" {
		promptCtx.Channel = &model.Channel{Id: channelID}
	}
	if s.mmclient != nil {
		if cfg := s.mmclient.GetConfig(); cfg != nil && cfg.ServiceSettings.SiteURL != nil {
			promptCtx.SiteURL = *cfg.ServiceSettings.SiteURL
		}
	}
	// Set Team from the first search result that has one so citation_format.tmpl
	// renders /teamname/pl/ URLs instead of /_redirect/pl/.
	for _, r := range ragResults {
		if r.TeamName != "" {
			promptCtx.Team = &model.Team{Name: r.TeamName}
			break
		}
	}
	if bot != nil {
		var botUserID string
		if mmBot := bot.GetMMBot(); mmBot != nil {
			botUserID = mmBot.UserId
		}
		promptCtx.SetBotFields(bot.GetConfig().DisplayName, bot.GetConfig().Name, botUserID, bot.GetService().DefaultModel, bot.GetService().Type, bot.GetConfig().CustomInstructions)
	}
	promptCtx.Parameters = map[string]interface{}{
		"Query":   query,
		"Results": ragResults,
	}

	return promptCtx
}

// buildPrompt creates an LLM completion request for answering a search query.
func (s *Search) buildPrompt(userID string, bot *bots.Bot, query, teamID, channelID string, results []RAGResult, operationSubType string) (llm.CompletionRequest, error) {
	if s.prompts == nil {
		return llm.CompletionRequest{}, fmt.Errorf("failed to format prompt: prompts not configured")
	}

	promptCtx := s.buildSearchPromptContext(userID, bot, query, teamID, channelID, results)

	systemMessage, err := s.prompts.Format("search_system", promptCtx)
	if err != nil {
		return llm.CompletionRequest{}, fmt.Errorf("failed to format prompt: %w", err)
	}

	return llm.CompletionRequest{
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
		Context:          promptCtx,
		Operation:        llm.OperationSearch,
		OperationSubType: operationSubType,
	}, nil
}

// RunSearch initiates a search and sends results to a DM
func (s *Search) RunSearch(ctx context.Context, userID string, bot *bots.Bot, query, teamID, channelID string, maxResults int) (map[string]string, error) {
	// Validate early (before creating posts)
	if !s.Enabled() {
		return nil, fmt.Errorf("search functionality is not configured")
	}

	query = strings.TrimSpace(query)
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
	go s.processSearch(bot, userID, query, teamID, channelID, maxResults, questionPost)

	return map[string]string{
		"postid":    questionPost.Id,
		"channelid": questionPost.ChannelId,
	}, nil
}

// processSearch handles the async portion of RunSearch
func (s *Search) processSearch(bot *bots.Bot, userID, query, teamID, channelID string, maxResults int, questionPost *model.Post) {
	// Create response post as a reply
	responsePost := &model.Post{
		RootId: questionPost.Id,
	}
	responsePost.AddProp(streaming.NoRegen, "true")

	if err := s.botDMNonResponse(bot.GetMMBot().UserId, userID, responsePost); err != nil {
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

	// Execute search
	results, err := s.executeSearch(context.Background(), query, Options{
		Limit:     maxResults,
		TeamID:    teamID,
		ChannelID: channelID,
		UserID:    userID,
	})
	if err != nil {
		s.mmclient.LogError("Error executing search", "error", err)
		processingError = err
		return
	}

	if len(results) == 0 {
		responsePost.Message = "I couldn't find any relevant messages for your query. Please try a different search term."
		if updateErr := s.mmclient.UpdatePost(responsePost); updateErr != nil {
			s.mmclient.LogError("Error updating post on error", "error", updateErr)
		}
		return
	}

	prompt, err := s.buildPrompt(userID, bot, query, teamID, channelID, results, llm.SubTypeStreaming)
	if err != nil {
		s.mmclient.LogError("Error building prompt", "error", err)
		processingError = err
		return
	}

	resultStream, err := bot.LLM().ChatCompletion(prompt)
	if err != nil {
		s.mmclient.LogError("Error generating answer", "error", err)
		processingError = err
		return
	}

	resultsJSON, err := json.Marshal(results)
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
}

// SearchQuery performs a search and returns results immediately
func (s *Search) SearchQuery(ctx context.Context, userID string, bot *bots.Bot, query, teamID, channelID string, maxResults int) (Response, error) {
	results, err := s.executeSearch(ctx, query, Options{
		Limit:     maxResults,
		TeamID:    teamID,
		ChannelID: channelID,
		UserID:    userID,
	})
	if err != nil {
		return Response{}, err
	}

	if len(results) == 0 {
		return Response{
			Answer:  "I couldn't find any relevant messages for your query. Please try a different search term.",
			Results: []RAGResult{},
		}, nil
	}

	prompt, err := s.buildPrompt(userID, bot, query, teamID, channelID, results, llm.SubTypeNoStream)
	if err != nil {
		return Response{}, err
	}

	answer, err := bot.LLM().ChatCompletionNoStream(prompt)
	if err != nil {
		return Response{}, fmt.Errorf("failed to generate answer: %w", err)
	}

	return Response{
		Answer:  answer,
		Results: results,
	}, nil
}

func (s *Search) botDMNonResponse(botid string, userID string, post *model.Post) error {
	streaming.ModifyPostForBot(botid, userID, post, "")

	if err := s.mmclient.DM(botid, userID, post); err != nil {
		return fmt.Errorf("failed to post DM: %w", err)
	}

	return nil
}
