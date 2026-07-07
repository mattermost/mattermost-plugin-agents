// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	stdcontext "context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

// Store is the subset of store.Store that the conversation service needs.
type Store interface {
	CreateConversation(conv *store.Conversation) error
	GetConversation(id string) (*store.Conversation, error)
	GetConversationByThreadBotUser(rootPostID, botID, userID string) (*store.Conversation, error)
	UpdateConversationTitle(id, title string) error
	UpdateConversationRootPostID(id string, rootPostID string) error
	CreateTurn(turn *store.Turn) error
	CreateTurnAutoSequence(turn *store.Turn) error
	GetTurnsForConversation(conversationID string) ([]store.Turn, error)
	GetTurnByPostID(postID string) (*store.Turn, error)
	UpdateTurnContent(id string, content json.RawMessage) error
	UpdateTurnTokens(id string, tokensIn, tokensOut int64) error
	UpdateTurnPostID(id string, postID *string) error
	DeleteResponseTurns(conversationID, postID string) error
	GetMaxSequenceForConversation(conversationID string) (int, error)
}

// BotLookup answers bot-membership and per-bot config queries.
type BotLookup interface {
	IsAnyBot(userID string) bool

	// GetBotConfigByID returns the bot's EnableVision and MaxFileSize.
	// ok is false when botID is unknown.
	GetBotConfigByID(botID string) (enableVision bool, maxFileSize int64, ok bool)
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
	FileIDs      []string
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
	content, err := marshalBlocks(userBlocksWithAttachments(params.UserMessage, params.FileIDs, s.mmClient))
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

// GetTurns returns all turns for a conversation, ordered by sequence.
func (s *Service) GetTurns(conversationID string) ([]store.Turn, error) {
	return s.store.GetTurnsForConversation(conversationID)
}

// GetInitiatingUserTurn returns the user turn that started the agent run
// whose assistant turn produced postID. Used to derive the run's deterministic
// TraceID at resume time so spans started on a different node still land in
// the original trace. Returns nil if no matching assistant turn or no
// preceding user turn is found.
func (s *Service) GetInitiatingUserTurn(conversationID, postID string) (*store.Turn, error) {
	turns, err := s.store.GetTurnsForConversation(conversationID)
	if err != nil {
		return nil, err
	}
	assistantSeq := -1
	for i := range turns {
		if turns[i].Role == "assistant" && turns[i].PostID != nil && *turns[i].PostID == postID {
			assistantSeq = turns[i].Sequence
			break
		}
	}
	if assistantSeq < 0 {
		return nil, nil
	}
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "user" && turns[i].Sequence < assistantSeq {
			return &turns[i], nil
		}
	}
	return nil, nil
}

// GetPreviousUserTurn returns the user turn that came immediately before
// currentUserTurnID in the conversation, or nil if currentUserTurnID is the
// first user turn. Used to attach a span link from a new run to the previous
// run's trace, so consecutive invocations in the same conversation are
// navigable in Tempo.
func (s *Service) GetPreviousUserTurn(conversationID, currentUserTurnID string) (*store.Turn, error) {
	turns, err := s.store.GetTurnsForConversation(conversationID)
	if err != nil {
		return nil, err
	}
	var prev *store.Turn
	for i := range turns {
		if turns[i].Role != "user" {
			continue
		}
		if turns[i].ID == currentUserTurnID {
			return prev, nil
		}
		t := turns[i]
		prev = &t
	}
	return nil, nil
}

// UpdateTurnContent updates the content JSON of a turn.
func (s *Service) UpdateTurnContent(turnID string, content json.RawMessage) error {
	return s.store.UpdateTurnContent(turnID, content)
}

// CreateTurn persists a new turn in the store with an explicit sequence.
func (s *Service) CreateTurn(turn *store.Turn) error {
	return s.store.CreateTurn(turn)
}

// CreateTurnAutoSequence persists a new turn, atomically assigning the next sequence number.
func (s *Service) CreateTurnAutoSequence(turn *store.Turn) error {
	return s.store.CreateTurnAutoSequence(turn)
}

// GetTurnByPostID returns the assistant turn anchored to postID, or nil.
func (s *Service) GetTurnByPostID(postID string) (*store.Turn, error) {
	return s.store.GetTurnByPostID(postID)
}

// UpdateTurnPostID sets or clears the PostID on a turn.
func (s *Service) UpdateTurnPostID(id string, postID *string) error {
	return s.store.UpdateTurnPostID(id, postID)
}

// DeleteResponseTurns removes the post's anchor and any assistant/tool_result
// turns between it and the originating user turn. Callers must build any
// completion request before calling this — ExcludeAfterPostID needs the anchor.
func (s *Service) DeleteResponseTurns(conversationID, postID string) error {
	return s.store.DeleteResponseTurns(conversationID, postID)
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
	FileIDs      []string
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
	existing, err := s.store.GetConversationByThreadBotUser(params.RootPostID, params.BotID, params.UserID)
	if err != nil && !errors.Is(err, store.ErrConversationNotFound) {
		return nil, fmt.Errorf("failed to look up conversation: %w", err)
	}

	if existing != nil {
		turnID, appendErr := s.appendUserTurn(existing.ID, params.UserMessage, params.UserPostID, params.FileIDs)
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
		FileIDs:      params.FileIDs,
	})
	if errors.Is(err, store.ErrConversationConflict) {
		// Another request created the conversation concurrently. Look it up and append the user turn.
		raceConv, lookupErr := s.store.GetConversationByThreadBotUser(params.RootPostID, params.BotID, params.UserID)
		if lookupErr != nil && !errors.Is(lookupErr, store.ErrConversationNotFound) {
			return nil, fmt.Errorf("failed to look up conversation after conflict: %w", lookupErr)
		}
		if raceConv == nil {
			return nil, fmt.Errorf("conversation vanished after conflict")
		}
		turnID, appendErr := s.appendUserTurn(raceConv.ID, params.UserMessage, params.UserPostID, params.FileIDs)
		if appendErr != nil {
			return nil, appendErr
		}
		return &GetOrCreateResult{
			Conversation: raceConv,
			IsNew:        false,
			UserTurnID:   turnID,
		}, nil
	}
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
func (s *Service) appendUserTurn(conversationID, message string, postID *string, fileIDs []string) (string, error) {
	content, err := marshalBlocks(userBlocksWithAttachments(message, fileIDs, s.mmClient))
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
		CreatedAt:      model.GetMillis(),
	}

	if err := s.store.CreateTurnAutoSequence(turn); err != nil {
		return "", fmt.Errorf("failed to create user turn: %w", err)
	}

	return turnID, nil
}

// BuildOptions controls optional behavior of BuildCompletionRequest.
type BuildOptions struct {
	ExcludeAfterPostID string

	// AllowUnsharedToolContent opts IN to sending tool_result content whose
	// Shared flag is not true to the LLM. The default is to redact — any
	// code path whose LLM response may reach other users (channel mentions,
	// channel follow-ups, regenerations in channels) MUST leave this false
	// so kept-private tool output cannot be paraphrased into a channel post.
	//
	// Set to true only in contexts where the LLM response is scoped to the
	// requester (e.g. the DM follow-up stream), since DM tool_results are
	// always shared=true anyway and nothing would be redacted in that case.
	AllowUnsharedToolContent bool
}

// BuildCompletionRequest builds an llm.CompletionRequest from the conversation's
// system prompt and all its turns. Thin wrapper around AssembleRequest that
// fetches turns from the store and resolves the bot's attachment config.
func (s *Service) BuildCompletionRequest(
	conv *store.Conversation,
	context *llm.Context,
	opts ...BuildOptions,
) (*llm.CompletionRequest, error) {
	turns, err := s.store.GetTurnsForConversation(conv.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get turns: %w", err)
	}
	enableVision, maxFileSize := s.attachmentConfigForBot(conv.BotID)
	return AssembleRequest(conv, turns, context, s.mmClient, enableVision, maxFileSize, opts...)
}

// AssembleRequest builds the CompletionRequest from already-loaded turns and
// externally-supplied rendering config. Exported so callers without a full
// Service (e.g. the /context endpoint) can reuse the runtime assembly path.
func AssembleRequest(
	conv *store.Conversation,
	turns []store.Turn,
	context *llm.Context,
	mmClient mmapi.Client,
	enableVision bool,
	maxFileSize int64,
	opts ...BuildOptions,
) (*llm.CompletionRequest, error) {
	// Default: redact unshared tool_result content so privacy is the
	// fail-safe. Callers whose LLM response will NOT reach other users
	// (DM follow-ups) can opt in to full content via AllowUnsharedToolContent.
	redactUnshared := true
	if len(opts) > 0 {
		redactUnshared = !opts[0].AllowUnsharedToolContent
		// Truncate back to right after the originating user turn. Stopping
		// at the anchor alone would leave demoted continuation turns at the
		// tail; bifrost rejects an assistant-ended request as prefill.
		if opts[0].ExcludeAfterPostID != "" {
			excludeID := opts[0].ExcludeAfterPostID
			anchorIdx := -1
			for i, turn := range turns {
				if turn.Role == "assistant" && turn.PostID != nil && *turn.PostID == excludeID {
					anchorIdx = i
					break
				}
			}
			if anchorIdx >= 0 {
				truncateAt := anchorIdx
				for i := anchorIdx - 1; i >= 0; i-- {
					if turns[i].Role == "user" {
						truncateAt = i + 1
						break
					}
				}
				turns = turns[:truncateAt]
			}
		}
	}

	if context != nil {
		RestoreLoadedMCPToolsFromTurns(context.Tools, turns)
	}

	posts := make([]llm.Post, 0, len(turns)+1)

	// System prompt is always first.
	posts = append(posts, llm.Post{
		Role:    llm.PostRoleSystem,
		Message: conv.SystemPrompt,
	})

	conversionOpts := PostConversionOptions{
		RedactUnshared: redactUnshared,
		MMClient:       mmClient,
		EnableVision:   enableVision,
		MaxFileSize:    maxFileSize,
	}
	if context != nil {
		conversionOpts.ToolStore = context.Tools
	}
	turnPosts, err := turnsToLLMPosts(turns, conversionOpts)
	if err != nil {
		return nil, err
	}
	posts = append(posts, turnPosts...)

	return &llm.CompletionRequest{
		Posts:     posts,
		Context:   context,
		Operation: conv.Operation,
	}, nil
}

// attachmentConfigForBot returns the bot's EnableVision and MaxFileSize.
// Falls back to vision-off + DefaultMaxFileSize when the bot is unknown.
func (s *Service) attachmentConfigForBot(botID string) (bool, int64) {
	if s.bots == nil {
		return false, DefaultMaxFileSize
	}
	enableVision, maxFileSize, ok := s.bots.GetBotConfigByID(botID)
	if !ok {
		return false, DefaultMaxFileSize
	}
	return enableVision, maxFileSize
}

// turnsToLLMPosts converts a contiguous slice of turns into llm.Posts,
// merging each tool_result turn into the preceding assistant turn so that
// BlocksToPost can pair tool_result blocks with their tool_use entries in a
// single llm.Post. Without this merge, tool_use entries go out with empty
// Result fields and bifrost emits empty-content tool messages, which
// Anthropic rejects with "text content blocks must be non-empty".
func turnsToLLMPosts(
	turns []store.Turn,
	conversionOpts PostConversionOptions,
) ([]llm.Post, error) {
	posts := make([]llm.Post, 0, len(turns))
	for i := 0; i < len(turns); i++ {
		turn := turns[i]
		blocks, err := unmarshalBlocks(turn.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal turn %s content: %w", turn.ID, err)
		}
		if turn.Role == "assistant" && i+1 < len(turns) && turns[i+1].Role == "tool_result" {
			nextBlocks, err := unmarshalBlocks(turns[i+1].Content)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal turn %s content: %w", turns[i+1].ID, err)
			}
			blocks = append(blocks, nextBlocks...)
			i++
		}
		post := BlocksToPost(blocks, turn.Role, conversionOpts)
		if turn.Role == "assistant" {
			// Anthropic signed thinking must be replayed byte-for-byte. Our stored
			// content blocks intentionally normalize assistant output (merge text
			// blocks, pair tool_use/result turns, redact private content), so the
			// persisted thinking block is not safe to send back as provider history.
			// Keep reasoning for UI/persistence, but omit it from rebuilt requests.
			post.Reasoning = ""
			post.ReasoningSignature = ""
		}
		posts = append(posts, post)
	}
	return posts, nil
}

// CreatePlaceholderAssistantTurn creates an empty assistant turn linked to the response post.
// Returns the turn ID. Called at stream start.
func (s *Service) CreatePlaceholderAssistantTurn(
	conversationID string,
	postID *string,
) (string, error) {
	turnID := model.NewId()
	turn := &store.Turn{
		ID:             turnID,
		ConversationID: conversationID,
		PostID:         postID,
		Role:           "assistant",
		Content:        json.RawMessage("[]"),
		CreatedAt:      model.GetMillis(),
	}

	if err := s.store.CreateTurnAutoSequence(turn); err != nil {
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
	for _, tt := range toolTurns {
		if writeErr := s.writeToolRound(conversationID, tt, shared); writeErr != nil {
			return writeErr
		}
	}

	return nil
}

// writeToolRound writes one assistant + tool_result turn pair for a single tool round.
func (s *Service) writeToolRound(conversationID string, tt toolrunner.ToolTurn, shared bool) error {
	assistantBlocks := toolUseBlocks(
		tt.AssistantMessage,
		tt.AssistantReasoning,
		tt.AssistantToolCalls,
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
		CreatedAt:      model.GetMillis(),
	}
	err = s.store.CreateTurnAutoSequence(assistantTurn)
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
		CreatedAt:      model.GetMillis(),
	}
	err = s.store.CreateTurnAutoSequence(resultTurn)
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

	title, err := lm.ChatCompletionNoStream(stdcontext.Background(), req,
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
	opts ...BuildOptions,
) (*llm.CompletionRequest, error) {
	// Default redacts (safe); AllowUnsharedToolContent opts out.
	redactUnshared := true
	if len(opts) > 0 {
		redactUnshared = !opts[0].AllowUnsharedToolContent
	}

	// If no thread data, fall back to standard request building.
	if threadData == nil || len(threadData.Posts) == 0 {
		return s.BuildCompletionRequest(conv, context, opts...)
	}

	turns, err := s.store.GetTurnsForConversation(conv.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get turns: %w", err)
	}

	if context != nil {
		RestoreLoadedMCPToolsFromTurns(context.Tools, turns)
	}

	enableVision, maxFileSize := s.attachmentConfigForBot(conv.BotID)

	// Build a set of post IDs that belong to the bot's turns.
	turnPostIDs := make(map[string]bool)
	// Map from postID to the turn for quick lookup.
	turnByPostID := make(map[string]store.Turn)
	// Turns without post IDs (tool rounds, etc.) keyed by index.
	var turnsWithoutPosts []store.Turn
	latestPostLinkedSequence := 0
	latestPostLinkedPostID := ""
	latestPostLinkedRole := ""

	for _, turn := range turns {
		if turn.PostID != nil {
			turnPostIDs[*turn.PostID] = true
			turnByPostID[*turn.PostID] = turn
			if turn.Sequence > latestPostLinkedSequence {
				latestPostLinkedSequence = turn.Sequence
				latestPostLinkedPostID = *turn.PostID
				latestPostLinkedRole = turn.Role
			}
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

	// Emit any non-post turns that precede all post-linked turns
	// (precedingSeq = 0). Route through turnsToLLMPosts so tool_use and
	// tool_result within the same tool round merge into a single llm.Post,
	// matching BuildCompletionRequest's behavior.
	conversionOpts := PostConversionOptions{
		RedactUnshared: redactUnshared,
		MMClient:       s.mmClient,
		EnableVision:   enableVision,
		MaxFileSize:    maxFileSize,
	}
	if context != nil {
		conversionOpts.ToolStore = context.Tools
	}
	leadingPosts, err := turnsToLLMPosts(turnsByPrecedingPost[0], conversionOpts)
	if err != nil {
		return nil, err
	}
	posts = append(posts, leadingPosts...)

	for _, threadPost := range threadData.Posts {
		if turnPostIDs[threadPost.Id] {
			// Render the post-linked turn and any trailing tool-round turns
			// as a single contiguous run so tool_result merges into the
			// preceding assistant turn's tool_use blocks.
			turn := turnByPostID[threadPost.Id]
			run := append([]store.Turn{turn}, turnsByPrecedingPost[turn.Sequence]...)
			runPosts, err := turnsToLLMPosts(run, conversionOpts)
			if err != nil {
				return nil, err
			}
			posts = append(posts, runPosts...)
		} else {
			// Render as user content with @username prefix, preserving any
			// uploaded files on thread posts that are not stored turns.
			username := ""
			if user, ok := threadData.UsersByID[threadPost.UserId]; ok {
				username = user.Username
			}
			blocks := userBlocksWithAttachments(format.AuthoredPost(threadPost, username), threadPost.FileIds, s.mmClient)
			posts = append(posts, BlocksToPost(blocks, "user", PostConversionOptions{RedactUnshared: redactUnshared, MMClient: s.mmClient, EnableVision: enableVision, MaxFileSize: maxFileSize}))
		}
		if latestPostLinkedRole == "user" && threadPost.Id == latestPostLinkedPostID {
			break
		}
	}

	return &llm.CompletionRequest{
		Posts:     posts,
		Context:   context,
		Operation: conv.Operation,
	}, nil
}
