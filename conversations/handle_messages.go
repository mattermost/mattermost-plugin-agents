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
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
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

// computeAllowToolsInChannel returns whether tools should be allowed for a channel mention,
// given the config flag and whether the invoker is automated. Bot activate_ai requires a
// tool policy checker: without it, strict filtering and MCP auto-approval are no-ops and tools
// must stay disabled so automated invokers cannot strand pending approvals.
func computeAllowToolsInChannel(configEnabled bool, post *model.Post, postingUser *model.User, hasToolPolicyChecker bool) bool {
	if !configEnabled {
		return false
	}
	if isBotActivateAI(post, postingUser) {
		return hasToolPolicyChecker
	}
	return !isAutomatedInvoker(post, postingUser)
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

func (c *Conversations) MessageHasBeenPosted(_ *plugin.Context, post *model.Post) {
	ctx, span := telemetry.Tracer().Start(context.Background(), "message has been posted",
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

	// Check we are mentioned like @ai
	if bot := c.bots.GetBotMentioned(post.Message); bot != nil {
		return c.handleMentions(ctx, bot, post, postingUser, channel)
	}

	// Check if this is post in the DM channel with any bot
	if bot := c.bots.GetBotForDMChannel(channel); bot != nil {
		return c.handleDMs(ctx, bot, channel, postingUser, post)
	}

	// Per-channel auto-reply handles the no-mention fall-through, and the two
	// features work in tandem. When auto-reply declines for a normal reason
	// (root_posts mode skipping a thread reply, missing license, stale bot),
	// fall through to the reminder so the user still learns that @mentioning
	// the agent works — it does in every one of those cases. Only a real
	// failure returns without the reminder.
	if setting := c.autoReplySettingForChannel(channel); setting != nil {
		autoReplyErr := c.handleAutoReply(ctx, setting, post, postingUser, channel)
		if errors.Is(autoReplyErr, ErrNoResponse) {
			c.maybeNotifyAgentMentionNeeded(post, channel)
		}
		return autoReplyErr
	}

	// Reply in a thread that did not @mention an agent: when the previous post
	// was authored by an agent, nudge the user with an ephemeral reminder.
	c.maybeNotifyAgentMentionNeeded(post, channel)

	return nil
}

func (c *Conversations) handleMentions(ctx context.Context, bot *bots.Bot, post *model.Post, postingUser *model.User, channel *model.Channel) (err error) {
	if restrictionErr := c.bots.CheckUsageRestrictions(postingUser.Id, bot, channel); restrictionErr != nil {
		return restrictionErr
	}

	// Check config to determine if tools should be allowed in channel mentions
	configEnabled := c.channelMentionToolCallingEnabled()
	hasToolPolicyChecker := c.toolPolicyChecker != nil
	allowToolsInChannel := computeAllowToolsInChannel(configEnabled, post, postingUser, hasToolPolicyChecker)
	channelToolsAutoRunEverywhereOnly := configEnabled && isBotActivateAI(post, postingUser) && hasToolPolicyChecker

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
	progress.Advance(responseProgressCheckingMCP)
	defer func() {
		if err != nil {
			c.failResponsePlaceholder(responsePost, postingUser.Locale)
		}
	}()

	return c.handleMentionViaConversation(ctx, bot, post, postingUser, channel, allowToolsInChannel, channelToolsAutoRunEverywhereOnly, responsePost, progress)
}

// handleMentionViaConversation processes a channel mention using the conversation entity model.
// It creates/continues a conversation for (RootPostID, BotID), runs the ToolRunner for
// auto-run tools, writes intermediate tool turns, and streams the final response.
// When channelToolsAutoRunEverywhereOnly is true (bot activate_ai), only MCP tools with
// auto_run_everywhere policy are kept.
func (c *Conversations) handleMentionViaConversation(
	ctx context.Context,
	bot *bots.Bot,
	post *model.Post,
	postingUser *model.User,
	channel *model.Channel,
	allowToolsInChannel bool,
	channelToolsAutoRunEverywhereOnly bool,
	responsePost *model.Post,
	progress *responseProgressReporter,
) error {
	var extraOpts []llm.ContextOption
	if channelToolsAutoRunEverywhereOnly {
		extraOpts = append(extraOpts, c.contextBuilder.WithLLMContextMCPToolFilter(func(tool llm.Tool) bool {
			return botChannelAutoEverywhereKeepTool(c.toolPolicyChecker, tool)
		}))
	}
	// User-interaction tools need someone who can answer them: a human invoker
	// with channel tool calling enabled. Bot activate_ai flows run unattended.
	if allowToolsInChannel && !channelToolsAutoRunEverywhereOnly {
		extraOpts = append(extraOpts, c.contextBuilder.WithLLMContextInteractive())
	}
	// Unconditional: disabled-tools contexts are already gated by the
	// toolsDisabled machinery below.
	extraOpts = append(extraOpts, c.contextBuilder.WithLLMContextResponseFiles())
	// Build the context once WITH tools so the system prompt can reference
	// .Tools and .DisabledToolsInfo.
	llmContext := c.buildConversationContextWithTools(
		ctx,
		bot, postingUser, channel,
		"Failed to load user tool preferences",
		extraOpts...,
	)
	progress.Advance(responseProgressLoadingConversation)

	toolsDisabled := applyToolAvailability(llmContext, false, allowToolsInChannel)
	if channelToolsAutoRunEverywhereOnly {
		c.applyBotChannelAutoEverywhereToolFilter(llmContext)
	}

	systemPrompt, fmtErr := c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, llmContext)
	if fmtErr != nil {
		return fmt.Errorf("failed to format system prompt: %w", fmtErr)
	}

	userPostID := post.Id
	convResult, convErr := c.convService.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       postingUser.Id,
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
	if channelToolsAutoRunEverywhereOnly {
		c.applyBotChannelAutoEverywhereToolFilter(llmContext)
	}

	ctx, runSpan := c.startAgentRunSpan(ctx, convResult.Conversation.ID, convResult.UserTurnID)
	defer runSpan.End()

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
	)
	if reqErr != nil {
		return fmt.Errorf("failed to build completion request: %w", reqErr)
	}

	opts := c.toolsDisabledLLMOptions(bot, toolsDisabled)

	// Channel mention: isDM=false gates auto-exec to auto_run_everywhere only.
	autoExec := c.shouldAutoExecuteTool(llmContext, false)
	progress.Advance(responseProgressConnectingProvider)
	result, runErr := c.runToolLoop(ctx, bot.LLM(), bot.GetConfig().EffectiveMaxToolTurns(), *completionRequest,
		func(tc llm.ToolCall) bool {
			if !allowToolsInChannel {
				return false
			}
			return autoExec(tc)
		},
		convResult.Conversation.ID,
		func(turns []toolrunner.ToolTurn) bool { return c.allToolsAutoRunEverywhere(turns, llmContext) },
		opts, "Failed to write tool turns")

	if runErr != nil {
		return fmt.Errorf("tool runner failed: %w", runErr)
	}

	stream := decorateStreamWithWebSearchAnnotations(result.Stream, llmContext)
	stream = c.decorateStreamWithCreatedFiles(ctx, bot, stream, responsePost, nil, llmContext, llmContext)

	if streamErr := c.streamToExistingPost(ctx, stream, responsePost, postingUser, channel, false); streamErr != nil {
		return fmt.Errorf("unable to stream response: %w", streamErr)
	}

	if convResult.IsNew {
		go func() {
			if genErr := c.convService.GenerateTitle(
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

func (c *Conversations) handleDMs(ctx context.Context, bot *bots.Bot, channel *model.Channel, postingUser *model.User, post *model.Post) (err error) {
	if restrictionErr := c.bots.CheckUsageRestrictionsForUser(bot, postingUser.Id); restrictionErr != nil {
		return restrictionErr
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
	progress.Advance(responseProgressCheckingMCP)
	defer func() {
		if err != nil {
			c.failResponsePlaceholder(responsePost, postingUser.Locale)
		}
	}()

	return c.handleDMViaConversation(ctx, bot, channel, postingUser, post, responsePost, progress)
}

// handleDMViaConversation processes a DM message using the conversation entity model.
func (c *Conversations) handleDMViaConversation(ctx context.Context, bot *bots.Bot, channel *model.Channel, postingUser *model.User, post, responsePost *model.Post, progress *responseProgressReporter) error {
	extraOpts := []llm.ContextOption{
		c.contextBuilder.WithLLMContextInteractive(),
		c.contextBuilder.WithLLMContextResponseFiles(),
	}
	if webSearchParams := c.extractWebSearchContext(post); len(webSearchParams) > 0 {
		extraOpts = append(extraOpts, c.contextBuilder.WithLLMContextParameters(webSearchParams))
	}
	// Build the context once WITH tools so the system prompt can reference them.
	llmContext := c.buildConversationContextWithTools(
		ctx,
		bot, postingUser, channel,
		"Failed to load user tool preferences",
		extraOpts...,
	)
	progress.Advance(responseProgressLoadingConversation)
	ensureDMWebSearchTracking(llmContext)

	convResult, err := c.CreateOrGetDMConversation(bot.GetMMBot().UserId, postingUser, channel, post, llmContext)
	if err != nil {
		return fmt.Errorf("unable to create DM conversation: %w", err)
	}
	responsePost.AddProp(streaming.ConversationIDProp, convResult.ConversationID)
	if updateErr := c.mmClient.UpdatePost(responsePost); updateErr != nil {
		return fmt.Errorf("failed to attach conversation to response placeholder: %w", updateErr)
	}

	ctx, runSpan := c.startAgentRunSpan(ctx, convResult.ConversationID, convResult.UserTurnID)
	defer runSpan.End()

	progress.Advance(responseProgressPreparingRequest)
	dmStream, err := c.processDMRequest(ctx, convResult.ConversationID, bot.LLM(), llmContext, bot.GetConfig().EffectiveMaxToolTurns(), func() {
		progress.Advance(responseProgressConnectingProvider)
	})
	if err != nil {
		return fmt.Errorf("unable to process DM request: %w", err)
	}

	stream := c.decorateStreamWithCreatedFiles(ctx, bot, dmStream.Stream, responsePost, nil, llmContext, llmContext)

	if streamErr := c.streamToExistingPost(ctx, stream, responsePost, postingUser, channel, false); streamErr != nil {
		return fmt.Errorf("unable to stream response: %w", streamErr)
	}

	if convResult.IsNew {
		go func() {
			if titleErr := c.convService.GenerateTitle(convResult.ConversationID, bot.LLM(), post.Message, llmContext); titleErr != nil {
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

// startAgentRunSpan anchors this run's trace to the initiating user turn ID so
// cross-node resumes can reproduce the same TraceID, and links to the previous
// user turn (if any) so Tempo renders a clickable jump from this trace back to
// the prior invocation. The caller must defer End on the returned span.
func (c *Conversations) startAgentRunSpan(ctx context.Context, convID, userTurnID string) (context.Context, trace.Span) {
	ctx = telemetry.WithTurnID(ctx, userTurnID)
	runOpts := []trace.SpanStartOption{trace.WithNewRoot()}
	if prev, prevErr := c.convService.GetPreviousUserTurn(convID, userTurnID); prevErr == nil && prev != nil {
		runOpts = append(runOpts, trace.WithLinks(trace.Link{
			SpanContext: telemetry.SpanContextForTurn(prev.ID),
		}))
	}
	return telemetry.Tracer().Start(ctx, "agent run", runOpts...)
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
func (c *Conversations) streamToExistingPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, postingUser *model.User, channel *model.Channel, continuation bool) error {
	streamCtx, err := c.streamingService.GetStreamingContext(ctx, post.Id)
	if err != nil {
		return err
	}

	locale := c.responseLocale(postingUser, channel)
	go func() {
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
	message := "Sorry! An error occurred while accessing the LLM. See server logs for details."
	if c.i18n != nil {
		T := i18n.LocalizerFunc(c.i18n, c.fallbackLocale(userLocale))
		message = T("agents.stream_to_post_access_llm_error", message)
	}
	post.Message = message
	if err := c.mmClient.UpdatePost(post); err != nil {
		c.mmClient.LogError("Failed to update response placeholder after startup error", "error", err)
	}
}

func (c *Conversations) responseLocale(postingUser *model.User, channel *model.Channel) string {
	defaultLocale := c.fallbackLocale("")
	if channel != nil && channel.Type == model.ChannelTypeDirect && postingUser != nil && postingUser.Locale != "" {
		return postingUser.Locale
	}
	return defaultLocale
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
