// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	ActivateAIProp   = "activate_ai"
	FromWebhookProp  = "from_webhook"
	FromBotProp      = "from_bot"
	FromPluginProp   = "from_plugin"
	FromOAuthAppProp = "from_oauth_app"
	WranglerProp     = "wrangler"
)

var (
	// ErrNoResponse is returned when no response is posted under a normal condition.
	ErrNoResponse = errors.New("no response")
)

// isAutomatedInvoker returns true when the post originates from automation (bot, webhook,
// plugin, or OAuth app). Used to disable channel tool calling for automated invokers
// since they cannot interactively approve tool calls.
func isAutomatedInvoker(post *model.Post, postingUser *model.User) bool {
	if postingUser != nil && postingUser.IsBot {
		return true
	}
	if post == nil {
		return false
	}
	automationProps := []string{FromWebhookProp, FromPluginProp, FromBotProp, FromOAuthAppProp}
	for _, prop := range automationProps {
		if post.GetProp(prop) != nil {
			return true
		}
	}
	return false
}

// isBotActivateAI is true when a bot account (or from_bot integration post) opts in with activate_ai.
func isBotActivateAI(post *model.Post, postingUser *model.User) bool {
	if post == nil || post.GetProp(ActivateAIProp) == nil {
		return false
	}
	if postingUser != nil && postingUser.IsBot {
		return true
	}
	return post.GetProp(FromBotProp) != nil
}

func (c *Conversations) userMCPPreferenceContextOptions(userID string, logMessage string) []llm.ContextOption {
	if c.contextBuilder == nil || c.mmClient == nil || userID == "" {
		return nil
	}

	prefs, err := mcp.LoadUserPreferences(c.mmClient, userID)
	if err != nil {
		c.mmClient.LogWarn(logMessage, "error", err.Error(), "userID", userID)
		return nil
	}
	if len(prefs.DisabledServers) == 0 {
		return nil
	}

	return []llm.ContextOption{
		c.contextBuilder.WithLLMContextDisabledMCPServers(prefs.DisabledServers),
	}
}

func removePreFilteredMCPServersFromVisibleStore(llmContext *llm.Context) {
	if llmContext == nil || llmContext.Tools == nil || len(llmContext.ToolCatalog.DisabledMCPServerOrigins) == 0 {
		return
	}
	llmContext.Tools.RemoveToolsByServerOrigin(llmContext.ToolCatalog.DisabledMCPServerOrigins)
}

// buildConversationContextWithTools assembles an LLM context for a bot
// interaction in a single pass. It applies user MCP preferences for
// DM/group channels, the caller's extra options, and WithLLMContextTools.
// After the build it also runs the post-build steps that all four
// conversation entry points share (filtered visible-store cleanup for
// DM/group channels).
//
// Pass prefsLogMessage == "" to skip the user MCP preferences lookup.
func (c *Conversations) buildConversationContextWithTools(
	ctx context.Context,
	bot *bots.Bot,
	user *model.User,
	channel *model.Channel,
	prefsLogMessage string,
	extraOpts ...llm.ContextOption,
) *llm.Context {
	isDMOrGroup := channel != nil && (channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup)

	opts := make([]llm.ContextOption, 0, len(extraOpts)+4)
	// Service account agents use one shared catalog; per-user MCP server preferences don't apply.
	if isDMOrGroup && prefsLogMessage != "" && user != nil && !c.contextBuilder.UsesServiceAccountCatalog(bot) {
		opts = append(opts, c.userMCPPreferenceContextOptions(user.Id, prefsLogMessage)...)
	}
	opts = append(opts, extraOpts...)
	opts = append(opts, c.contextBuilder.WithLLMContextTools(ctx, bot))

	llmContext := c.contextBuilder.BuildLLMContextUserRequest(bot, user, channel, opts...)

	if isDMOrGroup {
		// Pre-build filtering protects strict registries; post-build removal preserves
		// existing visible-store behavior for flag-off contexts.
		removePreFilteredMCPServersFromVisibleStore(llmContext)
	}

	return llmContext
}

func (c *Conversations) MessageHasBeenPosted(pluginContext *plugin.Context, post *model.Post) {
	// Our own posts (response placeholders, streamed replies) never trigger a
	// response. Return before starting a span so each turn does not leave an
	// extra, unlinked trace behind for its placeholder post.
	if c.bots.IsAnyBot(post.UserId) {
		return
	}

	ctx := context.Background()
	if pluginContext != nil {
		ctx = auth.WithSessionID(ctx, pluginContext.SessionId)
	}
	ctx, span := telemetry.Tracer().Start(ctx, "message has been posted",
		trace.WithAttributes(
			telemetry.PostID.String(post.Id),
			telemetry.ChannelID.String(post.ChannelId),
			telemetry.UserID.String(post.UserId),
		),
	)
	defer span.End()

	if err := c.handleMessages(ctx, post); err != nil {
		if errors.Is(err, ErrNoResponse) {
			c.mmClient.LogDebug(err.Error())
		} else {
			c.mmClient.LogError(err.Error())
		}
	}
}

func (c *Conversations) handleMessages(ctx context.Context, post *model.Post) error {
	// Don't respond to ourselves
	if c.bots.IsAnyBot(post.UserId) {
		return fmt.Errorf("not responding to ourselves: %w", ErrNoResponse)
	}

	// Never respond to remote posts
	if post.RemoteId != nil && *post.RemoteId != "" {
		return fmt.Errorf("not responding to remote posts: %w", ErrNoResponse)
	}

	// Don't respond to system messages; their text can mention agents and trigger replies.
	if post.IsSystemMessage() {
		return fmt.Errorf("not responding to system messages: %w", ErrNoResponse)
	}

	// Wrangler posts should be ignored
	if post.GetProp(WranglerProp) != nil {
		return fmt.Errorf("not responding to wrangler posts: %w", ErrNoResponse)
	}

	// Don't respond to plugins unless they ask for it
	if post.GetProp(FromPluginProp) != nil && post.GetProp(ActivateAIProp) == nil {
		return fmt.Errorf("not responding to plugin posts: %w", ErrNoResponse)
	}

	// Don't respond to webhooks
	if post.GetProp(FromWebhookProp) != nil {
		return fmt.Errorf("not responding to webhook posts: %w", ErrNoResponse)
	}

	channel, err := c.mmClient.GetChannel(post.ChannelId)
	if err != nil {
		return fmt.Errorf("unable to get channel: %w", err)
	}

	postingUser, err := c.mmClient.GetUser(post.UserId)
	if err != nil {
		return err
	}

	// Don't respond to other bots unless they ask for it
	if (postingUser.IsBot || post.GetProp(FromBotProp) != nil) && post.GetProp(ActivateAIProp) == nil {
		return fmt.Errorf("not responding to other bots: %w", ErrNoResponse)
	}

	// Mentioning the agent a DM is with is still a DM turn; mentioning a
	// different agent there is a mention of that agent.
	dmBot := c.bots.GetBotForDMChannel(channel)
	if bot := c.bots.GetBotMentioned(post.Message); bot != nil && (dmBot == nil || bot.GetMMBot().UserId != dmBot.GetMMBot().UserId) {
		return c.handleMentions(ctx, bot, post, postingUser, channel)
	}

	if dmBot != nil {
		return c.handleDMs(ctx, dmBot, channel, postingUser, post)
	}

	// Per-channel auto-reply handles the no-mention fall-through, and the two
	// features work in tandem. When auto-reply declines for a normal reason
	// (root_posts mode skipping a thread reply, missing license, stale bot),
	// fall through to the reminder so the user still learns that @mentioning
	// the agent works — it does in every one of those cases. A real failure
	// returns without the reminder, and when the ambient classifier
	// deliberately stays silent there is no reminder about the configured
	// agent.
	if setting := c.autoReplySettingForChannel(channel); setting != nil {
		autoReplyErr := c.handleAutoReply(ctx, setting, post, postingUser, channel)
		if errors.Is(autoReplyErr, ErrNoResponse) {
			skipBotID := ""
			if errors.Is(autoReplyErr, errAmbientDeclined) {
				skipBotID = setting.BotID
			}
			c.maybeNotifyAgentMentionNeeded(ctx, post, channel, skipBotID)
		}
		return autoReplyErr
	}

	// Reply in a thread that did not @mention an agent: when the previous post
	// was authored by an agent, nudge the user with an ephemeral reminder.
	c.maybeNotifyAgentMentionNeeded(ctx, post, channel, "")

	return nil
}

func (c *Conversations) handleMentions(ctx context.Context, bot *bots.Bot, post *model.Post, postingUser *model.User, channel *model.Channel) (err error) {
	policy, policyErr := c.resolveTurnPolicy(ctx, turnRequest{bot: bot, user: postingUser, channel: channel, trigger: post})
	if policyErr != nil {
		if _, ok := errors.AsType[*enterprise.LicenseError](policyErr); ok {
			c.postMultiplayerChannelsUnavailable(bot, channel, post)
			return fmt.Errorf("%w: %w", policyErr, ErrNoResponse)
		}
		return policyErr
	}

	responseRootID := post.Id
	if post.RootId != "" {
		responseRootID = post.RootId
	}

	responsePost := &model.Post{
		ChannelId: channel.Id,
		RootId:    responseRootID,
	}
	if placeholderErr := c.createResponsePlaceholder(bot.GetMMBot().UserId, postingUser.Id, responsePost, post.Id); placeholderErr != nil {
		return fmt.Errorf("unable to create response placeholder: %w", placeholderErr)
	}
	progress := newResponseProgressReporter(ctx, c.mmClient, responsePost)
	defer progress.Flush()
	progress.Advance(responseProgressCheckingMCP)
	defer func() {
		if err != nil {
			c.failResponsePlaceholder(responsePost, postingUser.Locale)
		}
	}()

	return c.handleMentionViaConversation(ctx, policy, post, responsePost, progress)
}

// handleMentionViaConversation processes a channel mention using the conversation entity model.
// It creates/continues a conversation for (RootPostID, BotID), runs the ToolRunner for
// auto-run tools, writes intermediate tool turns, and streams the final response.
func (c *Conversations) handleMentionViaConversation(
	ctx context.Context,
	policy *turnPolicy,
	post *model.Post,
	responsePost *model.Post,
	progress *responseProgressReporter,
) error {
	bot, postingUser, channel := policy.bot, policy.user, policy.channel
	// Build the context once WITH tools so the system prompt can reference
	// .Tools and .DisabledTools.
	llmContext := policy.buildContext(ctx, "Failed to load user tool preferences")
	// The run owns the scoped session until its stream finishes.
	scopedHandedOff := false
	defer func() {
		if !scopedHandedOff {
			llmContext.ReleaseScopedSession()
		}
	}()
	progress.Advance(responseProgressLoadingConversation)

	systemPrompt, fmtErr := c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, llmContext)
	if fmtErr != nil {
		return fmt.Errorf("failed to format system prompt: %w", fmtErr)
	}

	userPostID := post.Id
	convResult, convErr := c.getOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       postingUser.Id,
		SessionID:    auth.SessionIDFromContext(ctx),
		BotID:        bot.GetMMBot().UserId,
		ChannelID:    channel.Id,
		RootPostID:   responsePost.RootId,
		Operation:    "conversation",
		SystemPrompt: systemPrompt,
		UserMessage:  post.Message,
		UserPostID:   &userPostID,
		FileIDs:      post.FileIds,
	})
	if convErr != nil {
		return fmt.Errorf("failed to get or create conversation: %w", convErr)
	}
	responsePost.AddProp(streaming.ConversationIDProp, convResult.Conversation.ID)
	if updateErr := c.mmClient.UpdatePost(responsePost); updateErr != nil {
		return fmt.Errorf("failed to attach conversation to response placeholder: %w", updateErr)
	}
	llmContext.ResponsePostID = responsePost.Id
	if turn := llmContext.ToolCatalog.ScopedTurn; turn != nil {
		turn.ConversationID = convResult.Conversation.ID
	}

	ctx, runSpan := c.startAgentRunSpan(ctx, agentRunInfo{
		convID:     convResult.Conversation.ID,
		userTurnID: convResult.UserTurnID,
		bot:        bot,
		post:       post,
		response:   responsePost,
	}, progress)
	// Ownership of runSpan passes to the streaming goroutine once streaming
	// starts, so the run's duration covers the whole response.
	runHandedOff := false
	defer func() {
		if !runHandedOff {
			runSpan.End()
		}
	}()

	threadData, threadErr := mmapi.GetThreadData(c.mmClient, responsePost.RootId)
	if threadErr != nil {
		return fmt.Errorf("failed to get thread data: %w", threadErr)
	}
	progress.Advance(responseProgressPreparingRequest)

	// Channel mention: the follow-up stream is channel-visible, so any
	// tool_result content the requester previously kept private must be
	// redacted before it reaches the LLM. BuildChannelMentionRequest
	// defaults to redacting; we never opt in to AllowUnsharedToolContent
	// here.
	completionRequest, reqErr := c.convService.BuildChannelMentionRequest(
		convResult.Conversation,
		llmContext,
		threadData,
		conversation.BuildOptions{SessionID: auth.SessionIDFromContext(ctx)},
	)
	if reqErr != nil {
		return fmt.Errorf("failed to build completion request: %w", reqErr)
	}

	progress.Advance(responseProgressConnectingProvider)
	result, runErr := policy.runToolLoop(ctx, llmContext, convResult.Conversation.ID, *completionRequest, "Failed to write tool turns")

	if runErr != nil {
		return fmt.Errorf("tool runner failed: %w", runErr)
	}

	stream := decorateStreamWithWebSearchAnnotations(result.Stream, llmContext)
	stream = c.decorateStreamWithCreatedFiles(ctx, bot, stream, responsePost, nil, llmContext, llmContext)

	if streamErr := c.streamToExistingPost(ctx, stream, responsePost, postingUser, channel, false, func() {
		llmContext.ReleaseScopedSession()
		runSpan.End()
	}); streamErr != nil {
		return fmt.Errorf("unable to stream response: %w", streamErr)
	}
	runHandedOff = true
	scopedHandedOff = true

	if convResult.IsNew {
		titleCtx := telemetry.DetachContext(ctx)
		go func() {
			if genErr := c.convService.GenerateTitle(
				titleCtx,
				convResult.Conversation.ID,
				bot.LLM(),
				post.Message,
				llmContext,
			); genErr != nil {
				c.mmClient.LogError("Failed to generate title", "error", genErr.Error())
			}
		}()
	}

	return nil
}

// existingThreadConversationID returns the ID of the conversation the thread
// rooted at rootID already has with bot, or "" when there is none yet.
func (c *Conversations) existingThreadConversationID(bot *bots.Bot, user *model.User, rootID string) string {
	if c.convService == nil || rootID == "" {
		return ""
	}
	conv, err := c.convService.GetConversationByThread(rootID, bot.GetMMBot().UserId, user.Id)
	if err != nil || conv == nil {
		return ""
	}
	return conv.ID
}

func (c *Conversations) handleDMs(ctx context.Context, bot *bots.Bot, channel *model.Channel, postingUser *model.User, post *model.Post) (err error) {
	policy, policyErr := c.resolveTurnPolicy(ctx, turnRequest{bot: bot, user: postingUser, channel: channel, trigger: post})
	if errors.Is(policyErr, bots.ErrUsageRestriction) {
		return policyErr
	}

	responseRootID := post.Id
	if post.RootId != "" {
		responseRootID = post.RootId
	}
	responsePost := &model.Post{
		ChannelId: channel.Id,
		RootId:    responseRootID,
	}
	if placeholderErr := c.createResponsePlaceholder(bot.GetMMBot().UserId, postingUser.Id, responsePost, post.Id); placeholderErr != nil {
		return fmt.Errorf("unable to create response placeholder: %w", placeholderErr)
	}
	progress := newResponseProgressReporter(ctx, c.mmClient, responsePost)
	defer progress.Flush()
	progress.Advance(responseProgressCheckingMCP)
	if errors.Is(policyErr, ErrAnalysisSourceUnavailable) {
		c.setPlaceholderMessage(responsePost, postingUser.Locale, "agents.analysis_source_unavailable",
			"I can't continue this conversation because you no longer have access to what it is about.")
		return fmt.Errorf("%w: %w", policyErr, ErrNoResponse)
	}
	defer func() {
		if err != nil {
			c.failResponsePlaceholder(responsePost, postingUser.Locale)
		}
	}()
	if policyErr != nil {
		return policyErr
	}

	return c.handleDMViaConversation(ctx, policy, post, responsePost, progress)
}

// handleDMViaConversation processes a DM message using the conversation entity model.
func (c *Conversations) handleDMViaConversation(ctx context.Context, policy *turnPolicy, post, responsePost *model.Post, progress *responseProgressReporter) error {
	var extraOpts []llm.ContextOption
	if webSearchParams := c.extractWebSearchContext(post); len(webSearchParams) > 0 {
		extraOpts = append(extraOpts, c.contextBuilder.WithLLMContextParameters(webSearchParams))
	}
	// Build the context once WITH tools so the system prompt can reference them.
	llmContext := policy.buildContext(ctx, "Failed to load user tool preferences", extraOpts...)
	progress.Advance(responseProgressLoadingConversation)

	convResult, err := c.createOrGetDMConversation(auth.SessionIDFromContext(ctx), policy.bot.GetMMBot().UserId, policy.user, policy.channel, post, llmContext)
	if err != nil {
		return fmt.Errorf("unable to create DM conversation: %w", err)
	}

	return c.streamDMConversation(ctx, policy, post, responsePost, progress, llmContext, convResult)
}

// streamDMConversation runs the tool loop for the conversation's latest user
// turn and streams the answer onto responsePost.
func (c *Conversations) streamDMConversation(
	ctx context.Context,
	policy *turnPolicy,
	post, responsePost *model.Post,
	progress *responseProgressReporter,
	llmContext *llm.Context,
	convResult *DMConversationResult,
) error {
	bot, channel, postingUser := policy.bot, policy.channel, policy.user
	ensureDMWebSearchTracking(llmContext)
	responsePost.AddProp(streaming.ConversationIDProp, convResult.ConversationID)
	if updateErr := c.mmClient.UpdatePost(responsePost); updateErr != nil {
		return fmt.Errorf("failed to attach conversation to response placeholder: %w", updateErr)
	}
	llmContext.ResponsePostID = responsePost.Id

	ctx, runSpan := c.startAgentRunSpan(ctx, agentRunInfo{
		convID:     convResult.ConversationID,
		userTurnID: convResult.UserTurnID,
		bot:        bot,
		post:       post,
		response:   responsePost,
	}, progress)
	// Ownership of runSpan passes to the streaming goroutine once streaming
	// starts, so the run's duration covers the whole response.
	runHandedOff := false
	defer func() {
		if !runHandedOff {
			runSpan.End()
		}
	}()

	progress.Advance(responseProgressPreparingRequest)
	dmStream, err := c.processDMRequest(ctx, convResult.ConversationID, policy, llmContext, func() {
		progress.Advance(responseProgressConnectingProvider)
	})
	if err != nil {
		return fmt.Errorf("unable to process DM request: %w", err)
	}

	stream := c.decorateStreamWithCreatedFiles(ctx, bot, dmStream.Stream, responsePost, nil, llmContext, llmContext)

	if streamErr := c.streamToExistingPost(ctx, stream, responsePost, postingUser, channel, false, func() {
		dmStream.release()
		runSpan.End()
	}); streamErr != nil {
		dmStream.release()
		return fmt.Errorf("unable to stream response: %w", streamErr)
	}
	runHandedOff = true

	if convResult.IsNew {
		titleCtx := telemetry.DetachContext(ctx)
		go func() {
			if titleErr := c.convService.GenerateTitle(titleCtx, convResult.ConversationID, bot.LLM(), post.Message, llmContext); titleErr != nil {
				c.mmClient.LogError("Failed to generate title", "error", titleErr.Error())
			}
		}()
	}

	return nil
}

func ensureDMWebSearchTracking(llmContext *llm.Context) {
	if llmContext == nil {
		return
	}
	if llmContext.Parameters == nil {
		llmContext.Parameters = make(map[string]any)
	}
	if _, hasCount := llmContext.Parameters[mmtools.WebSearchCountKey]; !hasCount {
		llmContext.Parameters[mmtools.WebSearchCountKey] = 0
	}
	if _, hasQueries := llmContext.Parameters[mmtools.WebSearchExecutedQueriesKey]; !hasQueries {
		llmContext.Parameters[mmtools.WebSearchExecutedQueriesKey] = []string{}
	}
}

// agentRunInfo identifies the turn an agent run answers.
type agentRunInfo struct {
	convID     string
	userTurnID string
	bot        *bots.Bot
	post       *model.Post // the user's post
	response   *model.Post // the bot's response placeholder
}

// startAgentRunSpan anchors this run's trace to the initiating user turn ID so
// cross-node resumes can reproduce the same TraceID, and links to the previous
// user turn (if any) so Tempo renders a clickable jump from this trace back to
// the prior invocation. The run is also linked to the span active in ctx (the
// message hook), and that span records the run's trace ID, so either one leads
// to the other. Progress events recorded so far move onto the run span. The
// caller must End the returned span.
func (c *Conversations) startAgentRunSpan(ctx context.Context, info agentRunInfo, progress *responseProgressReporter) (context.Context, trace.Span) {
	hookSpan := trace.SpanFromContext(ctx)
	links := []trace.Link{}
	if hookSC := hookSpan.SpanContext(); hookSC.IsValid() {
		links = append(links, trace.Link{SpanContext: hookSC})
	}
	if prev, prevErr := c.convService.GetPreviousUserTurn(info.convID, info.userTurnID); prevErr == nil && prev != nil {
		links = append(links, trace.Link{SpanContext: telemetry.SpanContextForTurn(prev.ID)})
	}

	attrs := []attribute.KeyValue{
		telemetry.ConversationID.String(info.convID),
		telemetry.UserTurnID.String(info.userTurnID),
	}
	if info.post != nil {
		attrs = append(attrs,
			telemetry.PostID.String(info.post.Id),
			telemetry.ChannelID.String(info.post.ChannelId),
			telemetry.UserID.String(info.post.UserId),
		)
	}
	if info.response != nil {
		attrs = append(attrs, telemetry.ResponsePostID.String(info.response.Id))
		rootID := info.response.RootId
		if rootID == "" {
			rootID = info.response.Id
		}
		attrs = append(attrs, telemetry.ThreadRootPostID.String(rootID))
	}
	if info.bot != nil {
		attrs = append(attrs,
			telemetry.AgentID.String(info.bot.GetMMBot().UserId),
			telemetry.AgentName.String(info.bot.GetMMBot().Username),
		)
	}

	ctx = telemetry.WithTurnID(ctx, info.userTurnID)
	ctx, span := telemetry.Tracer().Start(ctx, "agent run",
		trace.WithNewRoot(),
		trace.WithLinks(links...),
		trace.WithAttributes(attrs...),
	)
	hookSpan.SetAttributes(
		telemetry.ConversationID.String(info.convID),
		telemetry.UserTurnID.String(info.userTurnID),
		telemetry.RunTraceID.String(span.SpanContext().TraceID().String()),
	)
	if progress != nil {
		progress.AttachSpan(span)
	}
	return ctx, span
}

// cloneWithAgentMention clones post and rewrites its message to lead with the
// agent's @mention, preserving the original text. Used to synthesize a mention
// so a post can be routed through handleMentions.
func cloneWithAgentMention(post *model.Post, botUsername string) *model.Post {
	mentionPost := post.Clone()
	mentionPost.Message = "@" + botUsername
	if message := strings.TrimSpace(post.Message); message != "" {
		mentionPost.Message += " " + message
	}
	return mentionPost
}

func (c *Conversations) createResponsePlaceholder(botID, requesterUserID string, post *model.Post, respondingToPostID string) error {
	streaming.ModifyPostForBot(botID, requesterUserID, post, respondingToPostID)
	return c.mmClient.CreatePost(post)
}

// streamToExistingPost streams an LLM response onto an existing post. With
// continuation=true it streams a tool-approval follow-up instead (see
// streamingService.StreamContinuationToPost).
//
// onFinish, when non-nil, runs after streaming ends; callers use it to end
// a span that should cover the whole response. It is not called when an
// error is returned.
func (c *Conversations) streamToExistingPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, postingUser *model.User, channel *model.Channel, continuation bool, onFinish func()) error {
	streamCtx, err := c.streamingService.GetStreamingContext(ctx, post.Id)
	if err != nil {
		return err
	}

	locale := c.responseLocale(postingUser, channel)
	go func() {
		if onFinish != nil {
			defer onFinish()
		}
		defer c.streamingService.FinishStreaming(post.Id)
		if continuation {
			c.streamingService.StreamContinuationToPost(streamCtx, stream, post, locale, postingUser.Id)
		} else {
			c.streamingService.StreamToPost(streamCtx, stream, post, locale, postingUser.Id)
		}
	}()

	return nil
}

func (c *Conversations) failResponsePlaceholder(post *model.Post, userLocale string) {
	c.setPlaceholderMessage(post, userLocale, "agents.stream_to_post_access_llm_error",
		"Sorry! An error occurred while accessing the LLM. See server logs for details.")
}

func (c *Conversations) setPlaceholderMessage(post *model.Post, userLocale, messageID, fallback string) {
	message := fallback
	if c.i18n != nil {
		T := i18n.LocalizerFunc(c.i18n, c.fallbackLocale(userLocale))
		message = T(messageID, fallback)
	}
	post.Message = message
	if err := c.mmClient.UpdatePost(post); err != nil {
		c.mmClient.LogError("Failed to update response placeholder", "error", err)
	}
}

func (c *Conversations) responseLocale(postingUser *model.User, channel *model.Channel) string {
	defaultLocale := c.fallbackLocale("")
	if channel != nil && channel.Type == model.ChannelTypeDirect && postingUser != nil && postingUser.Locale != "" {
		return postingUser.Locale
	}
	return defaultLocale
}

// postMultiplayerChannelsUnavailable replies in the thread as the agent so
// everyone in the channel sees why it did not answer. The reply uses the
// server locale because it is visible to the whole channel.
func (c *Conversations) postMultiplayerChannelsUnavailable(bot *bots.Bot, channel *model.Channel, post *model.Post) {
	if c.mmClient == nil || bot == nil || channel == nil || post == nil {
		return
	}

	fallback := "Agents can reply in channels and group messages on Mattermost Professional and above. You can still chat with me in a direct message."
	message := fallback
	if c.i18n != nil {
		T := i18n.LocalizerFunc(c.i18n, c.fallbackLocale(""))
		message = T("agents.multiplayer_channels_requires_professional", fallback)
	}

	rootID := post.RootId
	if rootID == "" {
		rootID = post.Id
	}
	reply := &model.Post{
		UserId:    bot.GetMMBot().UserId,
		ChannelId: channel.Id,
		RootId:    rootID,
		Message:   message,
	}
	if err := c.mmClient.CreatePost(reply); err != nil {
		c.mmClient.LogError("Failed to post multiplayer availability reply", "error", err.Error())
	}
}

func (c *Conversations) fallbackLocale(userLocale string) string {
	if userLocale != "" {
		return userLocale
	}
	if c.mmClient != nil {
		if config := c.mmClient.GetConfig(); config != nil && config.LocalizationSettings.DefaultServerLocale != nil && *config.LocalizationSettings.DefaultServerLocale != "" {
			return *config.LocalizationSettings.DefaultServerLocale
		}
	}
	return "en"
}
