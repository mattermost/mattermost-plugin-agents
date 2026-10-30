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
	// GetConversation retrieves a conversation by ID. Returns an error if not found.
	GetConversation(id string) (*store.Conversation, error)
	// GetConversationIncludingArchived retrieves a conversation by ID whether
	// or not it is archived.
	GetConversationIncludingArchived(id string) (*store.Conversation, error)
	GetConversationByThreadBotUser(rootPostID, botID, userID string) (*store.Conversation, error)
	// GetLatestConversationByThreadBotUser returns the tuple's active
	// conversation, else its most recently archived one, without writing.
	GetLatestConversationByThreadBotUser(rootPostID, botID, userID string) (*store.Conversation, error)
	// RestoreConversation un-archives the conversation. Returns
	// store.ErrConversationNotFound when it is not archived (or missing) and
	// store.ErrConversationConflict when another active conversation owns its
	// thread.
	RestoreConversation(id string) (*store.Conversation, error)
	// UpdateConversationTitle updates the title of a conversation.
	UpdateConversationTitle(id, title string) error
	// SetConversationTitleIfUnset sets the title only while the conversation
	// has none, reporting whether it did. Used for generated titles so they
	// never overwrite a title the user set.
	SetConversationTitleIfUnset(id, title string) (bool, error)
	// UpdateConversationRootPostID sets the RootPostID on a conversation.
	// Used when the post ID is only known after post creation (e.g., thread analysis DM posts).
	UpdateConversationRootPostID(id string, rootPostID string) error
	// CreateTurn persists a new turn in the store with an explicit sequence.
	CreateTurn(turn *store.Turn) error
	// CreateTurnAutoSequence persists a new turn, atomically assigning the next sequence number.
	CreateTurnAutoSequence(turn *store.Turn) error
	GetTurnsForConversation(conversationID string) ([]store.Turn, error)
	// GetTurnByPostID returns the assistant turn anchored to postID, or nil.
	GetTurnByPostID(postID string) (*store.Turn, error)
	// UpdateTurnContent updates the content JSON of a turn.
	UpdateTurnContent(id string, content json.RawMessage) error
	// CompareAndSwapTurnContent replaces a turn's content only while it still
	// equals expected, and reports whether it did.
	CompareAndSwapTurnContent(id string, expected, content json.RawMessage) (bool, error)
	UpdateTurnTokens(id string, tokensIn, tokensOut int64) error
	// UpdateTurnPostID sets or clears the PostID on a turn.
	UpdateTurnPostID(id string, postID *string) error
	// DeleteResponseTurns removes the post's anchor and any assistant/tool_result
	// turns between it and the originating user turn. Callers must build any
	// completion request before calling this — ExcludeAfterPostID needs the anchor.
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
	SessionID    string
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
	content, err := marshalBlocks(userBlocksWithAttachments(params.UserMessage, params.FileIDs, mmapi.WithFilePolicy(s.mmClient, params.SessionID)))
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

// ErrConversationArchived is returned when an archived conversation cannot be
// continued because another active conversation now owns its thread.
var ErrConversationArchived = errors.New("conversation is archived")

// ErrRootPostDeleted is returned for a conversation whose root post was
// deleted or no longer exists.
var ErrRootPostDeleted = errors.New("conversation root post was deleted")

// PostGetter looks up a Mattermost post. mmapi.Client and pluginapi's
// PostService both satisfy it.
type PostGetter interface {
	GetPost(postID string) (*model.Post, error)
}

// CheckRootPost returns ErrRootPostDeleted when conv's root post was deleted
// (or permanently removed), nil when the post exists or conv has no root
// post, and the lookup error for any other failure, so callers fail closed.
//
// Deleting a root post does not change its conversation row: DeleteAt on
// LLM_Conversations means "archived by its owner", like DeleteAt on a
// Mattermost channel. Every path that serves, restores, or replays a
// conversation by ID calls this so that content of a deleted thread, for
// example one a moderator removed because it leaked a secret, stays
// unreachable whether or not the conversation was archived. Turn paths
// (reply, regenerate, tool decisions, wakes) act on a post of the thread,
// which Mattermost deletes together with its root, so their own post lookup
// already refuses them; restoring an archived conversation checks here too.
func CheckRootPost(posts PostGetter, conv *store.Conversation) error {
	if conv.RootPostID == nil || *conv.RootPostID == "" {
		return nil
	}
	post, err := posts.GetPost(*conv.RootPostID)
	if mmapi.IsNotFound(err) {
		return ErrRootPostDeleted
	}
	if err != nil {
		return fmt.Errorf("failed to get conversation root post: %w", err)
	}
	if post.DeleteAt != 0 {
		return ErrRootPostDeleted
	}
	return nil
}

// GetConversation retrieves an active conversation by ID. Returns
// store.ErrConversationNotFound when it is missing or archived.
func (s *Service) GetConversation(id string) (*store.Conversation, error) {
	return s.store.GetConversation(id)
}

// GetConversationIncludingArchived retrieves a conversation by ID whether or
// not it is archived. Archiving keeps the conversation's Mattermost posts, so
// every path that serves or acts on one of those posts looks it up this way;
// callers that start or resume a turn then call ActivateConversation.
func (s *Service) GetConversationIncludingArchived(id string) (*store.Conversation, error) {
	return s.store.GetConversationIncludingArchived(id)
}

// GetConversationByThread returns the conversation rooted at rootPostID for
// the bot and user, archived or not, or nil when the thread has none. It
// never writes: a turn that passes its gates restores the conversation via
// GetOrCreateConversation or ActivateConversation.
func (s *Service) GetConversationByThread(rootPostID, botID, userID string) (*store.Conversation, error) {
	conv, err := s.store.GetLatestConversationByThreadBotUser(rootPostID, botID, userID)
	if errors.Is(err, store.ErrConversationNotFound) {
		return nil, nil
	}
	return conv, err
}

// ActivateConversation returns conv restored to active when it is archived,
// and conv unchanged otherwise. Every entry point that starts or resumes a
// turn on an existing conversation calls it once the turn is accepted, so a
// refused turn never un-archives. The restore is conditional on the row still
// being archived, so concurrent callers write at most once. Returns
// ErrConversationArchived when another active conversation owns the thread or
// the conversation was archived again concurrently.
//
// A conversation whose root post was deleted is never restored: it returns
// ErrRootPostDeleted (see CheckRootPost).
func (s *Service) ActivateConversation(conv *store.Conversation) (*store.Conversation, error) {
	if conv.DeleteAt == 0 {
		return conv, nil
	}
	if err := CheckRootPost(s.mmClient, conv); err != nil {
		return nil, err
	}
	restored, err := s.store.RestoreConversation(conv.ID)
	switch {
	case err == nil:
		return restored, nil
	case errors.Is(err, store.ErrConversationConflict):
		return nil, fmt.Errorf("%w: another conversation is active in its thread", ErrConversationArchived)
	case errors.Is(err, store.ErrConversationNotFound):
		// A concurrent caller restored it first.
		active, getErr := s.store.GetConversation(conv.ID)
		if errors.Is(getErr, store.ErrConversationNotFound) {
			return nil, ErrConversationArchived
		}
		return active, getErr
	default:
		return nil, fmt.Errorf("failed to restore conversation: %w", err)
	}
}

// findThreadConversation returns the active conversation for the thread, bot,
// and user. A user-archived conversation is restored rather than replaced, so
// a reply in an archived thread continues it with its history instead of
// silently starting a new conversation. Returns store.ErrConversationNotFound
// when the thread has no conversation at all.
func (s *Service) findThreadConversation(rootPostID, botID, userID string) (*store.Conversation, error) {
	conv, err := s.store.GetLatestConversationByThreadBotUser(rootPostID, botID, userID)
	if err != nil {
		return nil, err
	}
	active, err := s.ActivateConversation(conv)
	if errors.Is(err, ErrConversationArchived) {
		// A concurrent create made another conversation active first.
		return s.store.GetConversationByThreadBotUser(rootPostID, botID, userID)
	}
	return active, err
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

// CompareAndSwapTurnContent replaces a turn's content only while it still
// equals expected, and reports whether it did.
func (s *Service) CompareAndSwapTurnContent(turnID string, expected, content json.RawMessage) (bool, error) {
	return s.store.CompareAndSwapTurnContent(turnID, expected, content)
}

// CreateTurnAutoSequence persists a new turn, atomically assigning the next sequence number.
func (s *Service) CreateTurnAutoSequence(turn *store.Turn) error {
	return s.store.CreateTurnAutoSequence(turn)
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
	SessionID    string
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
	existing, err := s.findThreadConversation(params.RootPostID, params.BotID, params.UserID)
	if err != nil && !errors.Is(err, store.ErrConversationNotFound) {
		return nil, fmt.Errorf("failed to look up conversation: %w", err)
	}

	if existing != nil {
		turnID, appendErr := s.appendUserTurn(existing.ID, params.UserMessage, params.UserPostID, params.FileIDs, params.SessionID)
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
		SessionID:    params.SessionID,
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
		turnID, appendErr := s.appendUserTurn(raceConv.ID, params.UserMessage, params.UserPostID, params.FileIDs, params.SessionID)
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

// AppendSyntheticUserTurn appends a user turn with no backing Mattermost
// post: included in the LLM context on the next request, invisible in the
// thread. Used to re-wake an agent (wait_for_async_work). The kind is persisted
// on the text block so callers can identify the turn without matching its text.
func (s *Service) AppendSyntheticUserTurn(conversationID, kind, message string) error {
	blocks := textBlocks(message)
	for i := range blocks {
		blocks[i].SyntheticKind = kind
	}
	_, err := s.appendUserTurnBlocks(conversationID, blocks, nil)
	return err
}

// appendUserTurn creates a new user turn at the next available sequence number.
func (s *Service) appendUserTurn(conversationID, message string, postID *string, fileIDs []string, sessionID string) (string, error) {
	return s.appendUserTurnBlocks(conversationID, userBlocksWithAttachments(message, fileIDs, mmapi.WithFilePolicy(s.mmClient, sessionID)), postID)
}

func (s *Service) appendUserTurnBlocks(conversationID string, blocks []ContentBlock, postID *string) (string, error) {
	content, err := marshalBlocks(blocks)
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
	SessionID          string

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

	// SkipThreadPostsAfterLatestUserTurn drops non-turn thread posts created
	// or edited after the latest post-linked user turn in channel requests.
	// Unattended runs set this so thread posts nobody vetted cannot steer
	// auto-run tools.
	SkipThreadPostsAfterLatestUserTurn bool

	// ResponsePostID is the bot post the request is being built for. In channel
	// requests it is omitted from the rendered thread unless it is already a
	// turn: a fresh placeholder is empty and not yet part of the conversation.
	ResponsePostID string
}

func firstBuildOptions(opts []BuildOptions) BuildOptions {
	if len(opts) == 0 {
		return BuildOptions{}
	}
	return opts[0]
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

	mmClient = mmapi.WithFilePolicy(mmClient, firstBuildOptions(opts).SessionID)

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

// MissingToolResultContent is replayed as the result of a tool_use that has no
// stored tool_result (e.g. still awaiting approval). Providers reject a
// tool_use that is not answered by a non-empty tool_result.
const MissingToolResultContent = "No result: this tool call was not executed."

// turnsToLLMPosts converts a slice of turns into llm.Posts. tool_result turns
// produce no post of their own: each result is folded into the assistant turn
// holding its tool_use (see pairToolResults), so every tool_use replays with
// its own non-empty result.
func turnsToLLMPosts(
	turns []store.Turn,
	conversionOpts PostConversionOptions,
) ([]llm.Post, error) {
	blocksByTurn := make([][]ContentBlock, len(turns))
	for i, turn := range turns {
		blocks, err := UnmarshalBlocks(turn.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal turn %s content: %w", turn.ID, err)
		}
		blocksByTurn[i] = blocks
	}
	pairToolResults(turns, blocksByTurn)

	posts := make([]llm.Post, 0, len(turns))
	for i, turn := range turns {
		if turn.Role == "tool_result" {
			continue
		}
		post := BlocksToPost(blocksByTurn[i], turn.Role, conversionOpts)
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

// pairToolResults appends each tool_result block to the blocks of the nearest
// preceding assistant turn holding its tool_use. A result need not follow its
// tool_use directly: channel approvals append results at the end of the
// conversation, after any newer bot posts. Results without a matching tool_use
// are dropped, and executed tool_use blocks left without a result are answered
// with MissingToolResultContent.
func pairToolResults(turns []store.Turn, blocksByTurn [][]ContentBlock) {
	type toolUseKey struct {
		turn int
		id   string
	}
	ownerByID := make(map[string]int)
	answered := make(map[toolUseKey]bool)
	for i, turn := range turns {
		switch turn.Role {
		case "assistant":
			for _, b := range blocksByTurn[i] {
				if b.Type == BlockTypeToolUse && b.ID != "" {
					ownerByID[b.ID] = i
				}
				if b.Type == BlockTypeToolResult {
					answered[toolUseKey{i, b.ToolUseID}] = true
				}
			}
		case "tool_result":
			for _, b := range blocksByTurn[i] {
				owner, ok := ownerByID[b.ToolUseID]
				if b.Type != BlockTypeToolResult || !ok {
					continue
				}
				blocksByTurn[owner] = append(blocksByTurn[owner], b)
				answered[toolUseKey{owner, b.ToolUseID}] = true
			}
			blocksByTurn[i] = nil
		}
	}

	for i, turn := range turns {
		if turn.Role != "assistant" {
			continue
		}
		for _, b := range blocksByTurn[i] {
			if b.Type != BlockTypeToolUse || b.ID == "" || answered[toolUseKey{i, b.ID}] {
				continue
			}
			// BlocksToPost replays these as NotExecutedToolResult.
			if b.Status == StatusPending || b.Status == StatusAccepted {
				continue
			}
			blocksByTurn[i] = append(blocksByTurn[i], ContentBlock{
				Type:      BlockTypeToolResult,
				ToolUseID: b.ID,
				Content:   MissingToolResultContent,
				Status:    StatusError,
				Shared:    new(true),
			})
		}
	}
}

func toolUseIDs(content json.RawMessage) []string {
	blocks, err := UnmarshalBlocks(content)
	if err != nil {
		return nil
	}
	var ids []string
	for _, b := range blocks {
		if b.Type == BlockTypeToolUse && b.ID != "" {
			ids = append(ids, b.ID)
		}
	}
	return ids
}

// toolResultOwnerSlot returns the run of the first tool_use answered by a
// tool_result turn's content.
func toolResultOwnerSlot(content json.RawMessage, slotByToolUseID map[string]runSlot) (runSlot, bool) {
	blocks, err := UnmarshalBlocks(content)
	if err != nil {
		return runSlot{}, false
	}
	for _, b := range blocks {
		if slot, ok := slotByToolUseID[b.ToolUseID]; ok && b.Type == BlockTypeToolResult {
			return slot, true
		}
	}
	return runSlot{}, false
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
		tt.AssistantServerTools,
		tt.AssistantSegments,
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

// WebsocketEventConversationUpdated is published to a conversation's owner
// when its title changes or it is archived (webapp:
// custom_mattermost-ai_conversation_updated).
const WebsocketEventConversationUpdated = "conversation_updated"

// PublishConversationTitleUpdated tells the conversation's owner, and only
// them, that its title is now title.
func PublishConversationTitleUpdated(client mmapi.Client, conv *store.Conversation, title string) {
	client.PublishWebSocketEvent(
		WebsocketEventConversationUpdated,
		map[string]any{
			"conversation_id": conv.ID,
			"title":           title,
		},
		&model.WebsocketBroadcast{UserId: conv.UserID},
	)
}

// PublishConversationArchived tells the conversation's owner, and only them,
// that it was archived, so every open view drops it from its thread list.
func PublishConversationArchived(client mmapi.Client, conv *store.Conversation) {
	client.PublishWebSocketEvent(
		WebsocketEventConversationUpdated,
		map[string]any{
			"conversation_id": conv.ID,
			"archived":        true,
		},
		&model.WebsocketBroadcast{UserId: conv.UserID},
	)
}

// GenerateTitle generates a short title for the conversation and saves it,
// unless the conversation already has a title: a title the user set, before
// or while this runs, is never overwritten. When the generated title is
// saved, the owner is notified over the websocket.
// This should be called asynchronously (in a goroutine) after conversation creation.
// The lm parameter provides the language model for title generation.
// The context parameter provides bot/user context for the LLM call; ctx
// carries the trace so the title call is recorded under the turn's agent run.
func (s *Service) GenerateTitle(
	ctx stdcontext.Context,
	conversationID string,
	lm llm.LanguageModel,
	userMessage string,
	context *llm.Context,
) error {
	conv, err := s.store.GetConversation(conversationID)
	if err != nil {
		return fmt.Errorf("failed to get conversation: %w", err)
	}
	if conv.Title != "" {
		return nil
	}

	request := "Write a short title for the following request. Include only the title and nothing else, no quotations. Request:\n" + userMessage

	req := llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleUser, Message: request},
		},
		Context:          context,
		Operation:        llm.OperationTitleGeneration,
		OperationSubType: llm.SubTypeNoStream,
	}

	title, err := lm.ChatCompletionNoStream(ctx, req,
		llm.WithMaxGeneratedTokens(25),
		llm.WithReasoningDisabled(),
		llm.WithToolsDisabled(),
	)
	if err != nil {
		return fmt.Errorf("failed to generate title: %w", err)
	}

	title = strings.Trim(title, "\n \"'")
	if title == "" {
		return nil
	}

	// Conditional on the title still being unset: the user may have renamed
	// the conversation while the title was generated.
	updated, err := s.store.SetConversationTitleIfUnset(conversationID, title)
	if err != nil {
		return fmt.Errorf("failed to save title: %w", err)
	}
	if updated {
		PublishConversationTitleUpdated(s.mmClient, conv, title)
	}

	return nil
}

// floatingRun is a post-less user turn and the post-less turns that follow it.
// It has no thread position of its own, so it is rendered before the first
// thread post created after it, and never after the post-linked turn that
// follows it in the conversation (beforePostID).
type floatingRun struct {
	turns        []store.Turn
	createdAt    int64
	beforePostID string
}

// runSlot is where a post-less turn is rendered: a floating run, or else
// after the post-linked turn with sequence seq (0 for before all of them).
type runSlot struct {
	run *floatingRun
	seq int
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
	mmClient := mmapi.WithFilePolicy(s.mmClient, firstBuildOptions(opts).SessionID)

	// Post-linked turns are rendered where their post sits in the thread.
	turnPostIDs := make(map[string]bool)
	turnByPostID := make(map[string]store.Turn)
	latestPostLinkedSequence := 0
	latestPostLinkedPostID := ""
	latestPostLinkedRole := ""
	latestUserPostID := ""
	// Post-less turns (tool rounds, synthetic user turns) are grouped by
	// walking turns in sequence order: tool rounds ride with the preceding
	// post-linked turn so tool_use/tool_result stay contiguous, while a
	// post-less user turn opens a floating run positioned by time.
	// tool_result turns instead join the run holding their tool_use:
	// approvals append results at the end of the conversation, so an older
	// post approved after a newer one would otherwise lend its results to
	// the newer post.
	turnsByPrecedingPost := make(map[int][]store.Turn)
	var floatingRuns []*floatingRun
	// unanchored are the runs since the last post-linked turn; that turn's
	// post is the latest position they may be rendered at.
	var unanchored []*floatingRun
	var openRun *floatingRun
	precedingSeq := 0
	appendToSlot := func(slot runSlot, turn store.Turn) {
		if slot.run != nil {
			slot.run.turns = append(slot.run.turns, turn)
			return
		}
		turnsByPrecedingPost[slot.seq] = append(turnsByPrecedingPost[slot.seq], turn)
	}
	slotByToolUseID := make(map[string]runSlot)
	for _, turn := range turns {
		var slot runSlot
		switch {
		case turn.PostID != nil:
			turnPostIDs[*turn.PostID] = true
			turnByPostID[*turn.PostID] = turn
			if turn.Sequence > latestPostLinkedSequence {
				latestPostLinkedSequence = turn.Sequence
				latestPostLinkedPostID = *turn.PostID
				latestPostLinkedRole = turn.Role
			}
			if turn.Role == "user" {
				latestUserPostID = *turn.PostID
			}
			for _, run := range unanchored {
				run.beforePostID = *turn.PostID
			}
			unanchored = nil
			openRun = nil
			precedingSeq = turn.Sequence
			slot = runSlot{seq: turn.Sequence}
		case turn.Role == "user":
			openRun = &floatingRun{turns: []store.Turn{turn}, createdAt: turn.CreatedAt}
			floatingRuns = append(floatingRuns, openRun)
			unanchored = append(unanchored, openRun)
			slot = runSlot{run: openRun}
		default:
			slot = runSlot{run: openRun, seq: precedingSeq}
			if turn.Role == "tool_result" {
				if owner, ok := toolResultOwnerSlot(turn.Content, slotByToolUseID); ok {
					slot = owner
				}
			}
			appendToSlot(slot, turn)
		}
		if turn.Role == "assistant" {
			for _, id := range toolUseIDs(turn.Content) {
				slotByToolUseID[id] = slot
			}
		}
	}

	// Unattended runs drop non-turn thread posts nobody vetted: those created
	// or changed after the latest user turn's post. If that post is gone from
	// the thread there is no reference point, so every non-turn post is dropped.
	skipUnvettedPosts := firstBuildOptions(opts).SkipThreadPostsAfterLatestUserTurn
	var vettedCutoff int64
	if skipUnvettedPosts {
		for _, threadPost := range threadData.Posts {
			if threadPost.Id == latestUserPostID {
				vettedCutoff = threadPost.CreateAt
				break
			}
		}
	}

	posts := make([]llm.Post, 0, len(turns)+len(threadData.Posts)+1)

	// System prompt is always first.
	posts = append(posts, llm.Post{
		Role:    llm.PostRoleSystem,
		Message: conv.SystemPrompt,
	})

	// Emit any non-post turns that precede all post-linked turns
	// (precedingSeq = 0). Route through turnsToLLMPosts so tool_use and
	// tool_result within the same tool round merge into a single llm.Post,
	// matching BuildCompletionRequest's behavior.
	conversionOpts := PostConversionOptions{
		RedactUnshared: redactUnshared,
		MMClient:       mmClient,
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

	// Thread posts in order, each rendered from the bot's turn when one exists,
	// otherwise as plain user text. Floating runs are emitted before the first
	// post that is later than them or that their conversation position precedes.
	pendingRuns := floatingRuns
	emitRun := func(run *floatingRun) error {
		runPosts, err := turnsToLLMPosts(run.turns, conversionOpts)
		if err != nil {
			return err
		}
		posts = append(posts, runPosts...)
		return nil
	}
	emitRunsBefore := func(threadPost *model.Post) error {
		for len(pendingRuns) > 0 {
			run := pendingRuns[0]
			if run.beforePostID != threadPost.Id && run.createdAt >= threadPost.CreateAt {
				return nil
			}
			if err := emitRun(run); err != nil {
				return err
			}
			pendingRuns = pendingRuns[1:]
		}
		return nil
	}

	responsePostID := firstBuildOptions(opts).ResponsePostID
	for _, threadPost := range threadData.Posts {
		if !turnPostIDs[threadPost.Id] {
			if threadPost.Id == responsePostID {
				continue
			}
			if skipUnvettedPosts && (vettedCutoff == 0 || threadPost.CreateAt > vettedCutoff || threadPost.UpdateAt > vettedCutoff) {
				continue
			}
		}
		if err := emitRunsBefore(threadPost); err != nil {
			return nil, err
		}
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
			blocks := userBlocksWithAttachments(format.AuthoredPost(threadPost, username), threadPost.FileIds, mmClient)
			posts = append(posts, BlocksToPost(blocks, "user", PostConversionOptions{RedactUnshared: redactUnshared, MMClient: mmClient, EnableVision: enableVision, MaxFileSize: maxFileSize}))
		}
		if latestPostLinkedRole == "user" && threadPost.Id == latestPostLinkedPostID {
			break
		}
	}
	for _, run := range pendingRuns {
		if err := emitRun(run); err != nil {
			return nil, err
		}
	}

	return &llm.CompletionRequest{
		Posts:     posts,
		Context:   context,
		Operation: conv.Operation,
	}, nil
}
