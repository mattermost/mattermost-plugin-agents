// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	stdcontext "context"
	"fmt"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/subtitles"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"go.opentelemetry.io/otel/trace"
)

const ThreadIDProp = "referenced_thread"
const AnalysisTypeProp = "prompt_type"

// AnalyzedChannelIDProp records, on a channel analysis response, the channel
// that was analyzed, so continuing the conversation can re-check access to it.
const AnalyzedChannelIDProp = "analyzed_channel_id"

// ConfigProvider provides configuration values for conversation behavior
type ConfigProvider interface {
	EnableChannelMentionToolCalling() bool
	AllowNativeWebSearchInChannels() bool
	EnableAmbientAutoReply() bool
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
	wakeScheduler     WakeScheduler
	mutexAPI          cluster.MutexPluginAPI
	convService       *conversation.Service
	autoReplySettings AutoReplySettings
	ambientLimiter    *ambientLimiter
}

// MeetingsService defines the interface for meetings functionality needed by conversations
type MeetingsService interface {
	GetCaptionsFileIDFromProps(post *model.Post) (fileID string, err error)
	SummarizeTranscription(ctx stdcontext.Context, bot *bots.Bot, transcription *subtitles.Subtitles, context *llm.Context) (*llm.TextStreamResult, error)
}

// New creates the conversations service. toolPolicyChecker is required: tool
// decisions consult it without a nil check.
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
	toolPolicyChecker mcp.ToolPolicyChecker,
) *Conversations {
	return &Conversations{
		prompts:           prompts,
		mmClient:          mmClient,
		streamingService:  streamingService,
		contextBuilder:    contextBuilder,
		bots:              botsService,
		db:                db,
		licenseChecker:    licenseChecker,
		i18n:              i18nBundle,
		meetingsService:   meetingsService,
		configProvider:    configProvider,
		toolPolicyChecker: toolPolicyChecker,
		ambientLimiter:    newAmbientLimiter(),
	}
}

// SetMeetingsService sets the meetings service (used to break circular dependency during initialization)
func (c *Conversations) SetMeetingsService(meetingsService MeetingsService) {
	c.meetingsService = meetingsService
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
// This is separated from processDMRequest so the conversation_id can be
// set on the response post before it is created. Callers without a session
// fail closed on attachments.
func (c *Conversations) CreateOrGetDMConversation(
	botID string,
	postingUser *model.User,
	channel *model.Channel,
	post *model.Post,
	llmCtx *llm.Context,
) (*DMConversationResult, error) {
	return c.createOrGetDMConversation("", botID, postingUser, channel, post, llmCtx)
}

func (c *Conversations) createOrGetDMConversation(
	sessionID string,
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
			SessionID:    sessionID,
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

	result, err := c.getOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       postingUser.Id,
		SessionID:    sessionID,
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

// DMStreamResult is the return value of processDMRequest.
type DMStreamResult struct {
	Stream *llm.TextStreamResult
	// release ends the request's span, stops forwarding Stream and cancels
	// the tool loop. Call it once the consumer stops reading, including when
	// it never starts.
	release func()
}

// processDMRequest builds a completion request from the DM conversation and
// runs the tool loop under policy, returning the final stream. The
// conversation must already exist.
func (c *Conversations) processDMRequest(
	ctx stdcontext.Context,
	convID string,
	policy *turnPolicy,
	llmCtx *llm.Context,
	beforeProvider func(),
) (*DMStreamResult, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "process dm request",
		trace.WithAttributes(telemetry.ConversationID.String(convID)),
	)
	// The tool loop keeps running after this returns; on success the span
	// ends when the returned stream closes so it covers that work.
	// The tool loop gets its own cancel so a reply that stops reading (a
	// stopped stream) also stops the loop and its in-flight LLM call,
	// instead of leaving it blocked on a send nobody will receive.
	loopCtx, cancelLoop := stdcontext.WithCancel(ctx)
	spanHandedOff := false
	defer func() {
		if !spanHandedOff {
			cancelLoop()
			span.End()
		}
	}()

	if c.convService == nil {
		return nil, fmt.Errorf("conversation service not configured")
	}
	if llmCtx == nil {
		llmCtx = &llm.Context{}
	}

	// The turn was accepted, and its conversation activated, before this
	// runs. If the owner archives it in the meantime the reply still
	// completes on the archived row; it is not restored.
	conv, err := c.convService.GetConversationIncludingArchived(convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	completionReq, err := c.convService.BuildCompletionRequest(
		conv,
		llmCtx,
		conversation.BuildOptions{SessionID: auth.SessionIDFromContext(ctx)},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build completion request: %w", err)
	}

	if beforeProvider != nil {
		beforeProvider()
	}
	runResult, err := policy.runToolLoop(loopCtx, llmCtx, convID, *completionReq, "Failed to write tool turns", "conversation_id", convID)
	if err != nil {
		return nil, fmt.Errorf("tool runner failed: %w", err)
	}

	stream := runResult.Stream
	if webSearchData := mmtools.ConsumeWebSearchContexts(llmCtx); len(webSearchData) > 0 {
		stream = mmtools.DecorateStreamWithAnnotations(stream, webSearchData, nil)
	}

	spanHandedOff = true
	wrapped, releaseSpan := endSpanOnStreamClose(stream, span)
	release := func() {
		releaseSpan()
		cancelLoop()
	}
	return &DMStreamResult{Stream: wrapped, release: release}, nil
}

// endSpanOnStreamClose forwards every event of stream and ends span when the
// stream ends (an end or error event, or the channel closing), so a span
// around lazily-produced work lasts as long as it. The span ends before the
// terminal event is forwarded: consumers stop reading at that event, and
// must not observe the span still open afterwards.
//
// The returned release ends the span at once and stops forwarding; the rest
// of upstream is drained so earlier stages can finish. A consumer that stops
// reading early (a canceled reply) or never starts must call it, or the span
// stays open while the tool loop runs on.
func endSpanOnStreamClose(stream *llm.TextStreamResult, span trace.Span) (*llm.TextStreamResult, func()) {
	out := make(chan llm.TextStreamEvent)
	done := make(chan struct{})
	var once sync.Once
	// release ends the span itself: the forwarder may be blocked reading
	// upstream (mid tool call) and not notice done until the next event.
	release := func() {
		once.Do(func() {
			close(done)
			span.End()
		})
	}
	go func() {
		defer close(out)
		defer span.End() // idempotent; covers a close without a terminal event
		// After release, keep reading upstream and drop the events instead of
		// returning: an upstream stage such as the citation decorator sends
		// with a plain blocking send and would hang forever if nobody read.
		// Release also cancels the tool loop, so upstream closes soon after.
		released := false
		for event := range stream.Stream {
			if !released {
				select {
				case <-done:
					released = true
				default:
				}
			}
			if released {
				continue
			}
			if event.Type == llm.EventTypeEnd || event.Type == llm.EventTypeError {
				span.End()
			}
			select {
			case out <- event:
			case <-done:
				released = true
			}
		}
	}()
	return &llm.TextStreamResult{Stream: out}, release
}

// withProviderWebSearchLicense omits the provider-native web search tool below
// Professional so the request is sent without it. A nil checker fails closed.
func (c *Conversations) withProviderWebSearchLicense(opts []llm.LanguageModelOption) []llm.LanguageModelOption {
	if c.licenseChecker.Allows(enterprise.CapProviderWebSearch) {
		return opts
	}
	return append([]llm.LanguageModelOption{llm.WithSkipNativeWebSearch()}, opts...)
}

// channelMentionToolCallingEnabled reports whether the admin config allows
// tool calling for channel mentions.
func (c *Conversations) channelMentionToolCallingEnabled() bool {
	return c.configProvider != nil && c.configProvider.EnableChannelMentionToolCalling()
}

// ambientAutoReplyEnabled reports whether the admin config allows the
// experimental ambient auto-reply modes.
func (c *Conversations) ambientAutoReplyEnabled() bool {
	return c.configProvider != nil && c.configProvider.EnableAmbientAutoReply()
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
