// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/format"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/store"
	"github.com/mattermost/mattermost-plugin-ai/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

// Store is the subset of store.Store that the conversation service needs.
type Store interface {
	CreateConversation(conv *store.Conversation) error
	GetConversation(id string) (*store.Conversation, error)
	GetConversationByThreadAndBot(rootPostID, botID string) (*store.Conversation, error)
	UpdateConversationTitle(id, title string) error
	UpdateConversationRootPostID(id string, rootPostID string) error
	CreateTurn(turn *store.Turn) error
	GetTurnsForConversation(conversationID string) ([]store.Turn, error)
	UpdateTurnContent(id string, content json.RawMessage) error
	UpdateTurnTokens(id string, tokensIn, tokensOut int64) error
	GetMaxSequenceForConversation(conversationID string) (int, error)
}

// BotLookup checks whether a user ID belongs to an AI bot.
type BotLookup interface {
	IsAnyBot(userID string) bool
}

// Service manages conversation entities: creation, continuation,
// CompletionRequest building, turn writing, and title generation.
type Service struct {
	store    Store
	prompts  *llm.Prompts
	mmClient mmapi.Client
	bots     BotLookup
}

// NewService creates a new conversation Service.
func NewService(
	s Store,
	prompts *llm.Prompts,
	mmClient mmapi.Client,
	bots BotLookup,
) *Service {
	return &Service{
		store:    s,
		prompts:  prompts,
		mmClient: mmClient,
		bots:     bots,
	}
}

// CreateConversationParams contains parameters for creating a new conversation.
type CreateConversationParams struct {
	UserID       string
	BotID        string
	ChannelID    *string // nullable for non-channel conversations
	RootPostID   *string // nullable for non-thread conversations
	Operation    string  // e.g., "conversation", "thread_analysis", "search"
	SystemPrompt string  // already-formatted system prompt text
	UserMessage  string  // the first user message content
	UserPostID   *string // nullable: post ID for the user turn, if a post exists
}

// CreateConversationResult is the return value of CreateConversation.
type CreateConversationResult struct {
	ConversationID string
	UserTurnID     string
}

// CreateConversation creates a new conversation and its initial user turn.
func (s *Service) CreateConversation(params CreateConversationParams) (*CreateConversationResult, error) {
	now := model.GetMillis()
	convID := model.NewId()

	conv := &store.Conversation{
		ID:           convID,
		UserID:       params.UserID,
		BotID:        params.BotID,
		ChannelID:    params.ChannelID,
		RootPostID:   params.RootPostID,
		Title:        "",
		SystemPrompt: params.SystemPrompt,
		Operation:    params.Operation,
		CreatedAt:    now,
		UpdatedAt:    now,
		DeleteAt:     0,
	}

	if err := s.store.CreateConversation(conv); err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	turnID := model.NewId()
	content, err := marshalBlocks(textBlocks(params.UserMessage))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal user message: %w", err)
	}

	turn := &store.Turn{
		ID:             turnID,
		ConversationID: convID,
		PostID:         params.UserPostID,
		Role:           "user",
		Content:        content,
		Sequence:       1,
		CreatedAt:      now,
	}

	if err := s.store.CreateTurn(turn); err != nil {
		return nil, fmt.Errorf("failed to create user turn: %w", err)
	}

	return &CreateConversationResult{
		ConversationID: convID,
		UserTurnID:     turnID,
	}, nil
}

// GetConversation retrieves a conversation by ID. Returns an error if not found.
func (s *Service) GetConversation(id string) (*store.Conversation, error) {
	return s.store.GetConversation(id)
}

// UpdateConversationRootPostID sets the RootPostID on a conversation.
// Used when the post ID is only known after post creation (e.g., thread analysis DM posts).
func (s *Service) UpdateConversationRootPostID(id string, rootPostID string) error {
	return s.store.UpdateConversationRootPostID(id, rootPostID)
}

// UpdateConversationTitle updates the title of a conversation.
func (s *Service) UpdateConversationTitle(id, title string) error {
	return s.store.UpdateConversationTitle(id, title)
}

// GetOrCreateParams contains parameters for GetOrCreateConversation.
type GetOrCreateParams struct {
	UserID       string
	BotID        string
	ChannelID    string
	RootPostID   string // the thread root post ID
	Operation    string
	SystemPrompt string  // formatted system prompt (used only if creating)
	UserMessage  string  // new user message
	UserPostID   *string // post ID for the new user turn
}

// GetOrCreateResult is the return value of GetOrCreateConversation.
type GetOrCreateResult struct {
	Conversation *store.Conversation
	IsNew        bool
	UserTurnID   string // the newly created user turn
}

// GetOrCreateConversation looks up an existing conversation by (RootPostID, BotID),
// or creates a new one if none exists.
func (s *Service) GetOrCreateConversation(params GetOrCreateParams) (*GetOrCreateResult, error) {
	existing, err := s.store.GetConversationByThreadAndBot(params.RootPostID, params.BotID)
	if err != nil {
		return nil, fmt.Errorf("failed to look up conversation: %w", err)
	}

	if existing != nil {
		turnID, appendErr := s.appendUserTurn(existing.ID, params.UserMessage, params.UserPostID)
		if appendErr != nil {
			return nil, appendErr
		}

		return &GetOrCreateResult{
			Conversation: existing,
			IsNew:        false,
			UserTurnID:   turnID,
		}, nil
	}

	// No existing conversation: create a new one.
	channelID := params.ChannelID
	rootPostID := params.RootPostID
	createResult, err := s.CreateConversation(CreateConversationParams{
		UserID:       params.UserID,
		BotID:        params.BotID,
		ChannelID:    &channelID,
		RootPostID:   &rootPostID,
		Operation:    params.Operation,
		SystemPrompt: params.SystemPrompt,
		UserMessage:  params.UserMessage,
		UserPostID:   params.UserPostID,
	})
	if err != nil {
		return nil, err
	}

	conv, err := s.store.GetConversation(createResult.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get newly created conversation: %w", err)
	}

	return &GetOrCreateResult{
		Conversation: conv,
		IsNew:        true,
		UserTurnID:   createResult.UserTurnID,
	}, nil
}

// appendUserTurn creates a new user turn at the next available sequence number.
func (s *Service) appendUserTurn(conversationID, message string, postID *string) (string, error) {
	maxSeq, err := s.store.GetMaxSequenceForConversation(conversationID)
	if err != nil {
		return "", fmt.Errorf("failed to get max sequence: %w", err)
	}

	content, err := marshalBlocks(textBlocks(message))
	if err != nil {
		return "", fmt.Errorf("failed to marshal user message: %w", err)
	}

	turnID := model.NewId()
	turn := &store.Turn{
		ID:             turnID,
		ConversationID: conversationID,
		PostID:         postID,
		Role:           "user",
		Content:        content,
		Sequence:       maxSeq + 1,
		CreatedAt:      model.GetMillis(),
	}

	if err := s.store.CreateTurn(turn); err != nil {
		return "", fmt.Errorf("failed to create user turn: %w", err)
	}

	return turnID, nil
}

// BuildCompletionRequest builds an llm.CompletionRequest from the conversation's
// system prompt and all its turns.
func (s *Service) BuildCompletionRequest(
	conv *store.Conversation,
	context *llm.Context,
) (*llm.CompletionRequest, error) {
	turns, err := s.store.GetTurnsForConversation(conv.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get turns: %w", err)
	}

	posts := make([]llm.Post, 0, len(turns)+1)

	// System prompt is always first.
	posts = append(posts, llm.Post{
		Role:    llm.PostRoleSystem,
		Message: conv.SystemPrompt,
	})

	for _, turn := range turns {
		blocks, err := unmarshalBlocks(turn.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal turn %s content: %w", turn.ID, err)
		}
		posts = append(posts, BlocksToPost(blocks, turn.Role))
	}

	return &llm.CompletionRequest{
		Posts:     posts,
		Context:   context,
		Operation: conv.Operation,
	}, nil
}

// CreatePlaceholderAssistantTurn creates an empty assistant turn linked to the response post.
// Returns the turn ID. Called at stream start.
func (s *Service) CreatePlaceholderAssistantTurn(
	conversationID string,
	postID *string,
) (string, error) {
	maxSeq, err := s.store.GetMaxSequenceForConversation(conversationID)
	if err != nil {
		return "", fmt.Errorf("failed to get max sequence: %w", err)
	}

	turnID := model.NewId()
	turn := &store.Turn{
		ID:             turnID,
		ConversationID: conversationID,
		PostID:         postID,
		Role:           "assistant",
		Content:        json.RawMessage("[]"),
		Sequence:       maxSeq + 1,
		CreatedAt:      model.GetMillis(),
	}

	if err := s.store.CreateTurn(turn); err != nil {
		return "", fmt.Errorf("failed to create placeholder turn: %w", err)
	}

	return turnID, nil
}

// FinalizeAssistantTurn updates the placeholder turn with final content blocks and token counts.
// Called at stream end.
func (s *Service) FinalizeAssistantTurn(
	turnID string,
	content []ContentBlock,
	tokensIn, tokensOut int64,
) error {
	contentJSON, err := marshalBlocks(content)
	if err != nil {
		return fmt.Errorf("failed to marshal content: %w", err)
	}

	if err := s.store.UpdateTurnContent(turnID, contentJSON); err != nil {
		return fmt.Errorf("failed to update turn content: %w", err)
	}

	if err := s.store.UpdateTurnTokens(turnID, tokensIn, tokensOut); err != nil {
		return fmt.Errorf("failed to update turn tokens: %w", err)
	}

	return nil
}

// WriteToolTurns persists tool execution rounds from the ToolRunner.
// The shared flag controls the `shared` field on tool content blocks:
//   - true in DMs (everything visible to requester)
//   - false in channels (non-requester sees redacted content until shared)
func (s *Service) WriteToolTurns(
	conversationID string,
	toolTurns []toolrunner.ToolTurn,
	shared bool,
) error {
	maxSeq, err := s.store.GetMaxSequenceForConversation(conversationID)
	if err != nil {
		return fmt.Errorf("failed to get max sequence: %w", err)
	}
	nextSeq := maxSeq + 1

	for _, tt := range toolTurns {
		if writeErr := s.writeToolRound(conversationID, tt, shared, nextSeq); writeErr != nil {
			return writeErr
		}
		nextSeq += 2 // assistant + tool_result
	}

	return nil
}

// writeToolRound writes one assistant + tool_result turn pair for a single tool round.
func (s *Service) writeToolRound(conversationID string, tt toolrunner.ToolTurn, shared bool, seq int) error {
	assistantBlocks := toolUseBlocks(
		tt.AssistantMessage,
		tt.AssistantReasoning,
		tt.AssistantToolCalls,
		tt.ToolResults,
		shared,
	)
	assistantContent, err := marshalBlocks(assistantBlocks)
	if err != nil {
		return fmt.Errorf("failed to marshal assistant tool blocks: %w", err)
	}

	assistantTurn := &store.Turn{
		ID:             model.NewId(),
		ConversationID: conversationID,
		Role:           "assistant",
		Content:        assistantContent,
		TokensIn:       tt.TokensIn,
		TokensOut:      tt.TokensOut,
		Sequence:       seq,
		CreatedAt:      model.GetMillis(),
	}
	err = s.store.CreateTurn(assistantTurn)
	if err != nil {
		return fmt.Errorf("failed to create assistant tool turn: %w", err)
	}

	resultBlockList := toolResultBlocks(tt.ToolResults, shared)
	resultContent, err := marshalBlocks(resultBlockList)
	if err != nil {
		return fmt.Errorf("failed to marshal tool result blocks: %w", err)
	}

	resultTurn := &store.Turn{
		ID:             model.NewId(),
		ConversationID: conversationID,
		Role:           "tool_result",
		Content:        resultContent,
		Sequence:       seq + 1,
		CreatedAt:      model.GetMillis(),
	}
	err = s.store.CreateTurn(resultTurn)
	if err != nil {
		return fmt.Errorf("failed to create tool result turn: %w", err)
	}

	return nil
}

// GenerateTitle generates a short title for the conversation and saves it.
// This should be called asynchronously (in a goroutine) after conversation creation.
// The lm parameter provides the language model for title generation.
// The context parameter provides bot/user context for the LLM call.
func (s *Service) GenerateTitle(
	conversationID string,
	lm llm.LanguageModel,
	userMessage string,
	context *llm.Context,
) error {
	request := "Write a short title for the following request. Include only the title and nothing else, no quotations. Request:\n" + userMessage

	req := llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleUser, Message: request},
		},
		Context:          context,
		Operation:        llm.OperationTitleGeneration,
		OperationSubType: llm.SubTypeNoStream,
	}

	title, err := lm.ChatCompletionNoStream(req,
		llm.WithMaxGeneratedTokens(25),
		llm.WithReasoningDisabled(),
		llm.WithToolsDisabled(),
	)
	if err != nil {
		return fmt.Errorf("failed to generate title: %w", err)
	}

	title = strings.Trim(title, "\n \"'")

	if err := s.store.UpdateConversationTitle(conversationID, title); err != nil {
		return fmt.Errorf("failed to save title: %w", err)
	}

	return nil
}

// BuildChannelMentionRequest builds a CompletionRequest for a channel mention.
// It reads the bot's own turns from the conversation and interleaves thread
// posts from other users/bots at the correct sequence points.
func (s *Service) BuildChannelMentionRequest(
	conv *store.Conversation,
	context *llm.Context,
	threadData *mmapi.ThreadData,
) (*llm.CompletionRequest, error) {
	// If no thread data, fall back to standard request building.
	if threadData == nil || len(threadData.Posts) == 0 {
		return s.BuildCompletionRequest(conv, context)
	}

	turns, err := s.store.GetTurnsForConversation(conv.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get turns: %w", err)
	}

	// Build a set of post IDs that belong to the bot's turns.
	turnPostIDs := make(map[string]bool)
	// Map from postID to the turn for quick lookup.
	turnByPostID := make(map[string]store.Turn)
	// Turns without post IDs (tool rounds, etc.) keyed by index.
	var turnsWithoutPosts []store.Turn

	for _, turn := range turns {
		if turn.PostID != nil {
			turnPostIDs[*turn.PostID] = true
			turnByPostID[*turn.PostID] = turn
		} else {
			turnsWithoutPosts = append(turnsWithoutPosts, turn)
		}
	}

	posts := make([]llm.Post, 0, len(turns)+len(threadData.Posts)+1)

	// System prompt is always first.
	posts = append(posts, llm.Post{
		Role:    llm.PostRoleSystem,
		Message: conv.SystemPrompt,
	})

	// Build a unified timeline.
	// We iterate over thread posts in order (they are sorted by CreateAt).
	// For each post, either render it from the turn (if it belongs to the bot)
	// or as plain text.
	//
	// Turns without post IDs (tool rounds) are attached right after the
	// last turn-with-post that precedes them in sequence order.
	//
	// Build a map from post-linked turn sequence to following non-post turns.
	turnsByPrecedingPost := make(map[int][]store.Turn)
	if len(turnsWithoutPosts) > 0 {
		// Find the preceding post-linked turn for each non-post turn.
		postLinkedSeqs := make([]int, 0)
		for _, turn := range turns {
			if turn.PostID != nil {
				postLinkedSeqs = append(postLinkedSeqs, turn.Sequence)
			}
		}
		for _, turn := range turnsWithoutPosts {
			// Find the largest post-linked sequence that is less than this turn's sequence.
			precedingSeq := 0
			for _, seq := range postLinkedSeqs {
				if seq < turn.Sequence && seq > precedingSeq {
					precedingSeq = seq
				}
			}
			turnsByPrecedingPost[precedingSeq] = append(turnsByPrecedingPost[precedingSeq], turn)
		}
	}

	// Emit any non-post turns that precede all post-linked turns (precedingSeq = 0).
	for _, turn := range turnsByPrecedingPost[0] {
		blocks, err := unmarshalBlocks(turn.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal turn %s: %w", turn.ID, err)
		}
		posts = append(posts, BlocksToPost(blocks, turn.Role))
	}

	for _, threadPost := range threadData.Posts {
		if turnPostIDs[threadPost.Id] {
			// Render from the turn (full fidelity).
			turn := turnByPostID[threadPost.Id]
			blocks, err := unmarshalBlocks(turn.Content)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal turn %s: %w", turn.ID, err)
			}
			posts = append(posts, BlocksToPost(blocks, turn.Role))

			// Emit any non-post turns that follow this post-linked turn.
			for _, followingTurn := range turnsByPrecedingPost[turn.Sequence] {
				fBlocks, err := unmarshalBlocks(followingTurn.Content)
				if err != nil {
					return nil, fmt.Errorf("failed to unmarshal turn %s: %w", followingTurn.ID, err)
				}
				posts = append(posts, BlocksToPost(fBlocks, followingTurn.Role))
			}
		} else {
			// Render as plain text with @username prefix.
			username := ""
			if user, ok := threadData.UsersByID[threadPost.UserId]; ok {
				username = user.Username
			}
			posts = append(posts, llm.Post{
				Role:    llm.PostRoleUser,
				Message: "@" + username + ": " + format.PostBody(threadPost),
			})
		}
	}

	return &llm.CompletionRequest{
		Posts:     posts,
		Context:   context,
		Operation: conv.Operation,
	}, nil
}
