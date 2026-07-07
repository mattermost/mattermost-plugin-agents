// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
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

// SearchResultsProp is the post prop key used to attach search results JSON to the response post.
const SearchResultsProp = "search_results"

type Search struct {
	getSearch           func() embeddings.EmbeddingSearch
	mmclient            mmapi.Client
	prompts             *llm.Prompts
	streamingService    streaming.Service
	licenseChecker      *enterprise.LicenseChecker
	conversationService *conversation.Service
}

func New(
	getSearch func() embeddings.EmbeddingSearch,
	mmclient mmapi.Client,
	prompts *llm.Prompts,
	streamingService streaming.Service,
	licenseChecker *enterprise.LicenseChecker,
	conversationService *conversation.Service,
) *Search {
	return &Search{
		getSearch:           getSearch,
		mmclient:            mmclient,
		prompts:             prompts,
		streamingService:    streamingService,
		licenseChecker:      licenseChecker,
		conversationService: conversationService,
	}
}

// SetConversationService sets the conversation service after construction to
// break circular initialisation order in the plugin wiring.
func (s *Search) SetConversationService(svc *conversation.Service) {
	s.conversationService = svc
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
	if err := s.mmclient.DM(userID, bot.GetMMBot().UserId, questionPost); err != nil {
		return nil, fmt.Errorf("failed to create question post: %w", err)
	}

	// Start processing the search asynchronously. processSearch owns the
	// "run search" span lifecycle since the work happens after RunSearch
	// returns; ending the span here would orphan any child spans created
	// inside the goroutine.
	go s.processSearch(telemetry.DetachContext(ctx), bot, userID, query, teamID, channelID, maxResults, questionPost)

	return map[string]string{
		"postid":    questionPost.Id,
		"channelid": questionPost.ChannelId,
	}, nil
}

// processSearch handles the async portion of RunSearch.
func (s *Search) processSearch(ctx context.Context, bot *bots.Bot, userID, query, teamID, channelID string, maxResults int, questionPost *model.Post) {
	ctx, span := telemetry.Tracer().Start(ctx, "run search")
	defer span.End()
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
	results, err := s.executeSearch(ctx, query, Options{
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

	// Marshal results early; conversation_id is added alongside search_results
	// in a single UpdatePost below so the requester's Stop button works as
	// soon as streaming begins (Redux needs conversation_id to derive ownership).
	resultsJSON, err := json.Marshal(results)
	if err != nil {
		s.mmclient.LogError("Error marshaling search results", "error", err)
		processingError = err
		return
	}

	// Build system prompt from template (contains RAG results)
	prompt, err := s.buildPrompt(userID, bot, query, teamID, channelID, results, llm.SubTypeStreaming)
	if err != nil {
		s.mmclient.LogError("Error building prompt", "error", err)
		processingError = err
		return
	}

	// Create conversation entity if service is available
	var completionReq llm.CompletionRequest
	if s.conversationService != nil {
		systemPrompt := prompt.Posts[0].Message
		botID := bot.GetMMBot().UserId
		questionPostID := questionPost.Id

		createResult, convErr := s.conversationService.CreateConversation(conversation.CreateConversationParams{
			UserID:       userID,
			BotID:        botID,
			ChannelID:    &questionPost.ChannelId,
			RootPostID:   &questionPostID,
			Operation:    llm.OperationSearch,
			SystemPrompt: systemPrompt,
			UserMessage:  query,
			UserPostID:   &questionPostID,
		})
		if convErr != nil {
			s.mmclient.LogError("Error creating search conversation", "error", convErr)
			processingError = convErr
			return
		}

		// Set ConversationIDProp on response post so streaming turn persistence picks it up
		responsePost.AddProp(streaming.ConversationIDProp, createResult.ConversationID)

		promptCtx := s.buildSearchPromptContext(userID, bot, query, teamID, channelID, results)
		conv, convErr := s.conversationService.GetConversation(createResult.ConversationID)
		if convErr != nil {
			s.mmclient.LogError("Error getting search conversation", "error", convErr)
			processingError = convErr
			return
		}

		req, convErr := s.conversationService.BuildCompletionRequest(conv, promptCtx)
		if convErr != nil {
			s.mmclient.LogError("Error building completion request", "error", convErr)
			processingError = convErr
			return
		}
		req.OperationSubType = llm.SubTypeStreaming
		completionReq = *req
	} else {
		completionReq = prompt
	}

	// Attach search results and persist in one round trip, so by the time
	// streaming starts the DB post has both props.
	responsePost.AddProp(SearchResultsProp, string(resultsJSON))
	if updateErr := s.mmclient.UpdatePost(responsePost); updateErr != nil {
		s.mmclient.LogError("Error updating post with search results", "error", updateErr)
	}

	resultStream, err := bot.LLM().ChatCompletion(ctx, completionReq, llm.WithToolsDisabled())
	if err != nil {
		s.mmclient.LogError("Error generating answer", "error", err)
		processingError = err
		return
	}

	streamContext, err := s.streamingService.GetStreamingContext(ctx, responsePost.Id)
	if err != nil {
		s.mmclient.LogError("Error getting post streaming context", "error", err)
		processingError = err
		return
	}
	defer s.streamingService.FinishStreaming(responsePost.Id)
	s.streamingService.StreamToPost(streamContext, resultStream, responsePost, "", userID)
}

// SearchQuery performs a search and returns results immediately
func (s *Search) SearchQuery(ctx context.Context, userID string, bot *bots.Bot, query, teamID, channelID string, maxResults int) (Response, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "search query")
	defer span.End()

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

	// Build system prompt from template (contains RAG results)
	prompt, err := s.buildPrompt(userID, bot, query, teamID, channelID, results, llm.SubTypeNoStream)
	if err != nil {
		return Response{}, err
	}

	// If conversation service is available, create a conversation entity
	if s.conversationService != nil {
		systemPrompt := prompt.Posts[0].Message
		botID := bot.GetMMBot().UserId

		createResult, convErr := s.conversationService.CreateConversation(conversation.CreateConversationParams{
			UserID:       userID,
			BotID:        botID,
			Operation:    llm.OperationSearch,
			SystemPrompt: systemPrompt,
			UserMessage:  query,
		})
		if convErr != nil {
			return Response{}, fmt.Errorf("failed to create search conversation: %w", convErr)
		}

		promptCtx := s.buildSearchPromptContext(userID, bot, query, teamID, channelID, results)
		conv, convErr := s.conversationService.GetConversation(createResult.ConversationID)
		if convErr != nil {
			return Response{}, fmt.Errorf("failed to get search conversation: %w", convErr)
		}

		req, convErr := s.conversationService.BuildCompletionRequest(conv, promptCtx)
		if convErr != nil {
			return Response{}, fmt.Errorf("failed to build completion request: %w", convErr)
		}
		req.OperationSubType = llm.SubTypeNoStream

		answer, llmErr := bot.LLM().ChatCompletionNoStream(ctx, *req, llm.WithToolsDisabled())
		if llmErr != nil {
			return Response{}, fmt.Errorf("failed to generate answer: %w", llmErr)
		}

		// Persist assistant turn
		turnID, turnErr := s.conversationService.CreatePlaceholderAssistantTurn(createResult.ConversationID, nil)
		if turnErr != nil {
			return Response{}, fmt.Errorf("failed to create assistant turn: %w", turnErr)
		}

		blocks := []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: answer}}
		if finalizeErr := s.conversationService.FinalizeAssistantTurn(turnID, blocks, 0, 0); finalizeErr != nil {
			return Response{}, fmt.Errorf("failed to finalize assistant turn: %w", finalizeErr)
		}

		return Response{
			Answer:  answer,
			Results: results,
		}, nil
	}

	// Fallback: direct LLM call without conversation tracking
	answer, err := bot.LLM().ChatCompletionNoStream(ctx, prompt)
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
