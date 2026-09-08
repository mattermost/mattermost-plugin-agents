// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	stdcontext "context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/subtitles"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

const ThreadIDProp = "referenced_thread"
const AnalysisTypeProp = "prompt_type"

// ConfigProvider provides configuration values for conversation behavior
type ConfigProvider interface {
	EnableChannelMentionToolCalling() bool
	AllowNativeWebSearchInChannels() bool
	MCP() mcp.Config
}

type Conversations struct {
	prompts           *llm.Prompts
	mmClient          mmapi.Client
	streamingService  streaming.Service
	contextBuilder    *llmcontext.Builder
	bots              *bots.MMBots
	db                *mmapi.DBClient
	licenseChecker    *enterprise.LicenseChecker
	i18n              *i18n.Bundle
	meetingsService   MeetingsService
	configProvider    ConfigProvider
	toolPolicyChecker mcp.ToolPolicyChecker
	convService       *conversation.Service
	autoReplySettings AutoReplySettings
}

// MeetingsService defines the interface for meetings functionality needed by conversations
type MeetingsService interface {
	GetCaptionsFileIDFromProps(post *model.Post) (fileID string, err error)
	SummarizeTranscription(ctx stdcontext.Context, bot *bots.Bot, transcription *subtitles.Subtitles, context *llm.Context) (*llm.TextStreamResult, error)
}

func New(
	prompts *llm.Prompts,
	mmClient mmapi.Client,
	streamingService streaming.Service,
	contextBuilder *llmcontext.Builder,
	botsService *bots.MMBots,
	db *mmapi.DBClient,
	licenseChecker *enterprise.LicenseChecker,
	i18nBundle *i18n.Bundle,
	meetingsService MeetingsService,
	configProvider ConfigProvider,
) *Conversations {
	return &Conversations{
		prompts:          prompts,
		mmClient:         mmClient,
		streamingService: streamingService,
		contextBuilder:   contextBuilder,
		bots:             botsService,
		db:               db,
		licenseChecker:   licenseChecker,
		i18n:             i18nBundle,
		meetingsService:  meetingsService,
		configProvider:   configProvider,
	}
}

// SetMeetingsService sets the meetings service (used to break circular dependency during initialization)
func (c *Conversations) SetMeetingsService(meetingsService MeetingsService) {
	c.meetingsService = meetingsService
}

// SetToolPolicyChecker sets the per-tool policy checker used for auto-approval
// and DM auto-run decisions.
func (c *Conversations) SetToolPolicyChecker(checker mcp.ToolPolicyChecker) {
	c.toolPolicyChecker = checker
}

// SetAutoReplySettings sets the per-channel auto-reply settings lookup.
// The auto-reply feature is disabled when unset.
func (c *Conversations) SetAutoReplySettings(s AutoReplySettings) {
	c.autoReplySettings = s
}

// SetConversationService sets the conversation entity service.
func (c *Conversations) SetConversationService(svc *conversation.Service) {
	c.convService = svc
}

// DMConversationResult is the return value of CreateOrGetDMConversation.
type DMConversationResult struct {
	ConversationID string
	IsNew          bool
	UserTurnID     string
}

// CreateOrGetDMConversation creates or retrieves a conversation for a DM.
// This is separated from ProcessDMRequest so the conversation_id can be
// set on the response post before it is created.
func (c *Conversations) CreateOrGetDMConversation(
	botID string,
	postingUser *model.User,
	channel *model.Channel,
	post *model.Post,
	llmCtx *llm.Context,
) (*DMConversationResult, error) {
	if c.convService == nil {
		return nil, fmt.Errorf("conversation service not configured")
	}
	if llmCtx == nil {
		llmCtx = &llm.Context{}
	}
	if llmCtx.RequestingUser == nil {
		llmCtx.RequestingUser = postingUser
	}
	if llmCtx.Channel == nil {
		llmCtx.Channel = channel
	}

	systemPrompt := ""
	if c.prompts != nil {
		sp, err := c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, llmCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to format system prompt: %w", err)
		}
		systemPrompt = sp
	}

	postID := post.Id

	if post.RootId == "" {
		channelID := channel.Id
		result, err := c.convService.CreateConversation(conversation.CreateConversationParams{
			UserID:       postingUser.Id,
			BotID:        botID,
			ChannelID:    &channelID,
			RootPostID:   &postID,
			Operation:    "conversation",
			SystemPrompt: systemPrompt,
			UserMessage:  post.Message,
			UserPostID:   &postID,
			FileIDs:      post.FileIds,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create conversation: %w", err)
		}
		return &DMConversationResult{ConversationID: result.ConversationID, IsNew: true, UserTurnID: result.UserTurnID}, nil
	}

	result, err := c.convService.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       postingUser.Id,
		BotID:        botID,
		ChannelID:    channel.Id,
		RootPostID:   post.RootId,
		Operation:    "conversation",
		SystemPrompt: systemPrompt,
		UserMessage:  post.Message,
		UserPostID:   &postID,
		FileIDs:      post.FileIds,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get or create conversation: %w", err)
	}
	return &DMConversationResult{ConversationID: result.Conversation.ID, IsNew: result.IsNew, UserTurnID: result.UserTurnID}, nil
}

// DMStreamResult is the return value of ProcessDMRequest.
type DMStreamResult struct {
	Stream *llm.TextStreamResult
}

// ProcessDMRequest builds a completion request from the conversation and
// runs the tool loop, returning the final stream. The conversation must
// already exist (created via CreateOrGetDMConversation).
//
// maxToolTurns bounds the tool-call-execute-recall loop for this bot; pass 0
// or any non-positive value to use the system default
// (llm.DefaultMaxToolTurns).
func (c *Conversations) ProcessDMRequest(
	ctx stdcontext.Context,
	convID string,
	lm llm.LanguageModel,
	llmCtx *llm.Context,
	maxToolTurns int,
) (*DMStreamResult, error) {
	return c.processDMRequest(ctx, convID, lm, llmCtx, maxToolTurns, nil)
}

func (c *Conversations) processDMRequest(
	ctx stdcontext.Context,
	convID string,
	lm llm.LanguageModel,
	llmCtx *llm.Context,
	maxToolTurns int,
	beforeProvider func(),
) (*DMStreamResult, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "process dm request")
	defer span.End()

	if c.convService == nil {
		return nil, fmt.Errorf("conversation service not configured")
	}
	if llmCtx == nil {
		llmCtx = &llm.Context{}
	}

	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	completionReq, err := c.convService.BuildCompletionRequest(conv, llmCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to build completion request: %w", err)
	}

	if beforeProvider != nil {
		beforeProvider()
	}
	runResult, err := c.runToolLoop(ctx, lm, maxToolTurns, *completionReq,
		c.shouldAutoExecuteTool(llmCtx, true),
		convID,
		func([]toolrunner.ToolTurn) bool { return true },
		nil, "Failed to write tool turns", "conversation_id", convID)
	if err != nil {
		return nil, fmt.Errorf("tool runner failed: %w", err)
	}

	stream := runResult.Stream
	if webSearchData := mmtools.ConsumeWebSearchContexts(llmCtx); len(webSearchData) > 0 {
		stream = mmtools.DecorateStreamWithAnnotations(stream, webSearchData, nil)
	}

	return &DMStreamResult{Stream: stream}, nil
}

// runToolLoop runs the ToolRunner over req, persisting each intermediate tool
// round to the conversation as it completes. sharedForTurns decides the shared
// flag written with each round; writeFailMsg (plus writeFailArgs) is logged
// when persisting a round fails.
func (c *Conversations) runToolLoop(
	ctx stdcontext.Context,
	lm llm.LanguageModel,
	maxRounds int,
	req llm.CompletionRequest,
	shouldExecute func(llm.ToolCall) bool,
	convID string,
	sharedForTurns func([]toolrunner.ToolTurn) bool,
	opts []llm.LanguageModelOption,
	writeFailMsg string,
	writeFailArgs ...any,
) (*toolrunner.ToolRunResult, error) {
	runner := toolrunner.New(lm, toolrunner.WithMaxRounds(maxRounds))
	return runner.Run(ctx, req, shouldExecute, func(turns []toolrunner.ToolTurn) {
		if writeErr := c.convService.WriteToolTurns(convID, turns, sharedForTurns(turns)); writeErr != nil {
			c.mmClient.LogError(writeFailMsg, append([]any{"error", writeErr}, writeFailArgs...)...)
		}
	}, opts...)
}

// channelMentionToolCallingEnabled reports whether the admin config allows
// tool calling for channel mentions.
func (c *Conversations) channelMentionToolCallingEnabled() bool {
	return c.configProvider != nil && c.configProvider.EnableChannelMentionToolCalling()
}

// shouldAutoExecuteTool returns a callback that decides whether a tool call
// should be auto-executed based on the tool policy and the conversation
// context. In DMs, both auto_run and auto_run_everywhere bypass approval.
// In channels, only auto_run_everywhere bypasses approval — the legacy
// auto_run policy is DM-only so the channel-visible follow-up cannot
// reveal unshared tool output without an explicit Share from the requester.
func (c *Conversations) shouldAutoExecuteTool(llmCtx *llm.Context, isDM bool) func(llm.ToolCall) bool {
	return func(tc llm.ToolCall) bool {
		if isMCPMetaToolCall(tc, llmCtx) {
			return true
		}
		var lookup llm.ToolLookup
		var found bool
		if llmCtx != nil {
			lookup, found = llmCtx.Tools.LookupTool(tc.Name, tc.ServerOrigin)
		}
		if !found {
			return false
		}
		// Interaction tools are answered by the user; auto-executing one
		// would bypass the question entirely.
		if lookup.Tool.UserInteraction != "" {
			return false
		}
		// Auto-execute built-ins (e.g. CreateFile) run without approval, like
		// the MCP meta-tools: their only side effect is scoped to the
		// assistant's own response. Only honored for built-ins — an MCP tool
		// carrying the flag must still go through policy.
		if isAutoExecuteBuiltIn(lookup.Tool) {
			return true
		}
		if c.toolPolicyChecker == nil {
			return false
		}
		policy, enabled := c.toolPolicyChecker.GetToolPolicy(lookup.ServerOrigin, lookup.BareName)
		if !enabled {
			return false
		}
		if isDM {
			return mcp.IsToolPolicyAutoRunInDM(policy)
		}
		return mcp.IsToolPolicyAutoRunEverywhere(policy)
	}
}

// allToolsAutoRunEverywhere checks whether every tool call across the given
// tool turns has an auto_run_everywhere policy.  When true, tool results can
// be written with shared=true so the result-approval UI is skipped.
func (c *Conversations) allToolsAutoRunEverywhere(turns []toolrunner.ToolTurn, llmCtx *llm.Context) bool {
	sawToolCall := false
	for _, turn := range turns {
		for _, tc := range turn.AssistantToolCalls {
			sawToolCall = true
			if isMCPMetaToolCall(tc, llmCtx) {
				continue
			}
			var lookup llm.ToolLookup
			var found bool
			if llmCtx != nil {
				lookup, found = llmCtx.Tools.LookupTool(tc.Name, tc.ServerOrigin)
			}
			if !found {
				return false
			}
			// Auto-execute built-ins never require approval, so a round made
			// up only of them can still be written shared=true.
			if isAutoExecuteBuiltIn(lookup.Tool) {
				continue
			}
			if c.toolPolicyChecker == nil {
				return false
			}
			policy, enabled := c.toolPolicyChecker.GetToolPolicy(lookup.ServerOrigin, lookup.BareName)
			if !enabled || !mcp.IsToolPolicyAutoRunEverywhere(policy) {
				return false
			}
		}
	}
	return sawToolCall
}

// isAutoExecuteBuiltIn reports whether the tool is a built-in flagged to run
// without user approval. The AutoExecute flag is only honored for built-ins
// (empty ServerOrigin) — MCP tools must always go through policy.
func isAutoExecuteBuiltIn(tool llm.Tool) bool {
	return tool.AutoExecute && tool.ServerOrigin == ""
}

func isMCPMetaToolCall(tc llm.ToolCall, llmCtx *llm.Context) bool {
	if !mcp.IsMCPMetaTool(tc.Name) || tc.ServerOrigin != "" {
		return false
	}
	if llmCtx == nil || llmCtx.Tools == nil {
		return true
	}
	return llmCtx.Tools.GetServerOrigin(tc.Name) == ""
}
