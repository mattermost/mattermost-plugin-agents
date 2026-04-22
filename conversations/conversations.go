// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/i18n"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost-plugin-agents/subtitles"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
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
}

// MeetingsService defines the interface for meetings functionality needed by conversations
type MeetingsService interface {
	GetCaptionsFileIDFromProps(post *model.Post) (fileID string, err error)
	SummarizeTranscription(bot *bots.Bot, transcription *subtitles.Subtitles, context *llm.Context) (*llm.TextStreamResult, error)
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

// SetConversationService sets the conversation entity service.
func (c *Conversations) SetConversationService(svc *conversation.Service) {
	c.convService = svc
}

// DMConversationResult is the return value of CreateOrGetDMConversation.
type DMConversationResult struct {
	ConversationID string
	IsNew          bool
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
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create conversation: %w", err)
		}
		return &DMConversationResult{ConversationID: result.ConversationID, IsNew: true}, nil
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
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get or create conversation: %w", err)
	}
	return &DMConversationResult{ConversationID: result.Conversation.ID, IsNew: result.IsNew}, nil
}

// DMStreamResult is the return value of ProcessDMRequest.
type DMStreamResult struct {
	Stream *llm.TextStreamResult
}

// ProcessDMRequest builds a completion request from the conversation and
// runs the tool loop, returning the final stream. The conversation must
// already exist (created via CreateOrGetDMConversation).
func (c *Conversations) ProcessDMRequest(
	convID string,
	lm llm.LanguageModel,
	llmCtx *llm.Context,
) (*DMStreamResult, error) {
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

	runner := toolrunner.New(lm)
	runResult, err := runner.Run(*completionReq, c.shouldAutoExecuteTool(llmCtx, true), func(turns []toolrunner.ToolTurn) {
		if writeErr := c.convService.WriteToolTurns(convID, turns, true); writeErr != nil {
			c.mmClient.LogError("Failed to write tool turns", "error", writeErr, "conversation_id", convID)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("tool runner failed: %w", err)
	}

	stream := runResult.Stream
	if webSearchData := mmtools.ConsumeWebSearchContexts(llmCtx); len(webSearchData) > 0 {
		stream = mmtools.DecorateStreamWithAnnotations(stream, webSearchData, nil)
	}

	return &DMStreamResult{Stream: stream}, nil
}

// shouldAutoExecuteTool returns a callback that decides whether a tool call
// should be auto-executed based on the tool policy and the conversation
// context. In DMs, both auto_run and auto_run_everywhere bypass approval.
// In channels, only auto_run_everywhere bypasses approval — the legacy
// auto_run policy is DM-only so the channel-visible follow-up cannot
// reveal unshared tool output without an explicit Share from the requester.
func (c *Conversations) shouldAutoExecuteTool(llmCtx *llm.Context, isDM bool) func(llm.ToolCall) bool {
	return func(tc llm.ToolCall) bool {
		if c.toolPolicyChecker == nil {
			return false
		}
		origin := tc.ServerOrigin
		if origin == "" && llmCtx.Tools != nil {
			origin = llmCtx.Tools.GetServerOrigin(tc.Name)
		}
		policy, enabled := c.toolPolicyChecker.GetToolPolicy(origin, tc.Name)
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
	if c.toolPolicyChecker == nil {
		return false
	}
	for _, turn := range turns {
		for _, tc := range turn.AssistantToolCalls {
			origin := tc.ServerOrigin
			if origin == "" && llmCtx.Tools != nil {
				origin = llmCtx.Tools.GetServerOrigin(tc.Name)
			}
			policy, enabled := c.toolPolicyChecker.GetToolPolicy(origin, tc.Name)
			if !enabled || !mcp.IsToolPolicyAutoRunEverywhere(policy) {
				return false
			}
		}
	}
	return len(turns) > 0
}
