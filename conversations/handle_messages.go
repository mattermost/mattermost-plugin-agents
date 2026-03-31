// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversation"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/mmtools"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost-plugin-ai/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
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

// computeAllowToolsInChannel returns whether tools should be allowed for a channel mention,
// given the config flag and whether the invoker is automated.
func computeAllowToolsInChannel(configEnabled bool, post *model.Post, postingUser *model.User) bool {
	return configEnabled && !isAutomatedInvoker(post, postingUser)
}

func (c *Conversations) MessageHasBeenPosted(ctx *plugin.Context, post *model.Post) {
	if err := c.handleMessages(post); err != nil {
		if errors.Is(err, ErrNoResponse) {
			c.mmClient.LogDebug(err.Error())
		} else {
			c.mmClient.LogError(err.Error())
		}
	}
}

func (c *Conversations) handleMessages(post *model.Post) error {
	// Don't respond to ourselves
	if c.bots.IsAnyBot(post.UserId) {
		return fmt.Errorf("not responding to ourselves: %w", ErrNoResponse)
	}

	// Never respond to remote posts
	if post.RemoteId != nil && *post.RemoteId != "" {
		return fmt.Errorf("not responding to remote posts: %w", ErrNoResponse)
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
		return c.handleMentions(bot, post, postingUser, channel)
	}

	// Check if this is post in the DM channel with any bot
	if bot := c.bots.GetBotForDMChannel(channel); bot != nil {
		return c.handleDMs(bot, channel, postingUser, post)
	}

	return nil
}

func (c *Conversations) handleMentions(bot *bots.Bot, post *model.Post, postingUser *model.User, channel *model.Channel) error {
	if err := c.bots.CheckUsageRestrictions(postingUser.Id, bot, channel); err != nil {
		return err
	}

	// Check config to determine if tools should be allowed in channel mentions
	configEnabled := c.configProvider != nil && c.configProvider.EnableChannelMentionToolCalling()
	allowToolsInChannel := computeAllowToolsInChannel(configEnabled, post, postingUser)

	responseRootID := post.Id
	if post.RootId != "" {
		responseRootID = post.RootId
	}

	return c.handleMentionViaConversation(bot, post, postingUser, channel, allowToolsInChannel, responseRootID)
}

// handleMentionViaConversation processes a channel mention using the conversation entity model.
// It creates/continues a conversation for (RootPostID, BotID), runs the ToolRunner for
// auto-run tools, writes intermediate tool turns, and streams the final response.
func (c *Conversations) handleMentionViaConversation(
	bot *bots.Bot,
	post *model.Post,
	postingUser *model.User,
	channel *model.Channel,
	allowToolsInChannel bool,
	responseRootID string,
) error {
	contextOpts := []llm.ContextOption{
		c.contextBuilder.WithLLMContextTools(bot),
	}
	llmContext := c.contextBuilder.BuildLLMContextUserRequest(bot, postingUser, channel, contextOpts...)

	toolsDisabled := !allowToolsInChannel
	if llmContext != nil {
		if toolsDisabled && llmContext.Tools != nil {
			llmContext.DisabledToolsInfo = llmContext.Tools.GetToolsInfo()
		} else {
			llmContext.DisabledToolsInfo = nil
		}
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
		RootPostID:   responseRootID,
		Operation:    "conversation",
		SystemPrompt: systemPrompt,
		UserMessage:  post.Message,
		UserPostID:   &userPostID,
	})
	if convErr != nil {
		return fmt.Errorf("failed to get or create conversation: %w", convErr)
	}

	responsePost := &model.Post{
		ChannelId: channel.Id,
		RootId:    responseRootID,
	}
	if placeholderErr := c.createResponsePlaceholder(bot.GetMMBot().UserId, postingUser.Id, responsePost, post.Id); placeholderErr != nil {
		return fmt.Errorf("unable to create response placeholder: %w", placeholderErr)
	}

	responsePost.AddProp(streaming.ConversationIDProp, convResult.Conversation.ID)
	if updateErr := c.mmClient.UpdatePost(responsePost); updateErr != nil {
		c.mmClient.LogError("Failed to set conversation_id prop", "error", updateErr)
	}

	threadData, threadErr := mmapi.GetThreadData(c.mmClient, responseRootID)
	if threadErr != nil {
		c.failResponsePlaceholder(responsePost, postingUser.Locale)
		return fmt.Errorf("failed to get thread data: %w", threadErr)
	}

	completionRequest, reqErr := c.convService.BuildChannelMentionRequest(convResult.Conversation, llmContext, threadData)
	if reqErr != nil {
		c.failResponsePlaceholder(responsePost, postingUser.Locale)
		return fmt.Errorf("failed to build completion request: %w", reqErr)
	}

	var opts []llm.LanguageModelOption
	if toolsDisabled {
		opts = append(opts, llm.WithToolsDisabled())
		if c.configProvider != nil && c.configProvider.AllowNativeWebSearchInChannels() && bot.HasNativeWebSearchEnabled() {
			opts = append(opts, llm.WithNativeWebSearchAllowed())
		}
	}

	runner := toolrunner.New(bot.LLM())
	result, runErr := runner.Run(*completionRequest, func(tc llm.ToolCall) bool {
		if !allowToolsInChannel || c.toolPolicyChecker == nil {
			return false
		}
		// LLM-returned tool calls may lack ServerOrigin; resolve from tool store.
		origin := tc.ServerOrigin
		if origin == "" && llmContext.Tools != nil {
			origin = llmContext.Tools.GetServerOrigin(tc.Name)
		}
		policy, enabled := c.toolPolicyChecker.GetToolPolicy(origin, tc.Name)
		return policy == mcp.ToolPolicyAutoRun && enabled
	}, opts...)
	if runErr != nil {
		c.failResponsePlaceholder(responsePost, postingUser.Locale)
		return fmt.Errorf("tool runner failed: %w", runErr)
	}

	if len(result.ToolTurns) > 0 {
		if writeErr := c.convService.WriteToolTurns(convResult.Conversation.ID, result.ToolTurns, false); writeErr != nil {
			c.mmClient.LogError("Failed to write tool turns", "error", writeErr)
		}
	}

	if streamErr := c.streamResponseToExistingPost(result.Stream, responsePost, postingUser, channel); streamErr != nil {
		c.failResponsePlaceholder(responsePost, postingUser.Locale)
		return fmt.Errorf("unable to stream response: %w", streamErr)
	}

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

	return nil
}

func (c *Conversations) handleDMs(bot *bots.Bot, channel *model.Channel, postingUser *model.User, post *model.Post) error {
	if err := c.bots.CheckUsageRestrictionsForUser(bot, postingUser.Id); err != nil {
		return err
	}

	return c.handleDMViaConversation(bot, channel, postingUser, post)
}

// handleDMViaConversation processes a DM message using the conversation entity model.
func (c *Conversations) handleDMViaConversation(bot *bots.Bot, channel *model.Channel, postingUser *model.User, post *model.Post) error {
	contextOpts := []llm.ContextOption{
		c.contextBuilder.WithLLMContextTools(bot),
	}
	webSearchParams := c.extractWebSearchContext(post)
	if len(webSearchParams) > 0 {
		contextOpts = append(contextOpts, c.contextBuilder.WithLLMContextParameters(webSearchParams))
	}
	llmContext := c.contextBuilder.BuildLLMContextUserRequest(bot, postingUser, channel, contextOpts...)
	if llmContext.Parameters == nil {
		llmContext.Parameters = make(map[string]interface{})
	}
	if _, hasCount := llmContext.Parameters[mmtools.WebSearchCountKey]; !hasCount {
		llmContext.Parameters[mmtools.WebSearchCountKey] = 0
	}
	if _, hasQueries := llmContext.Parameters[mmtools.WebSearchExecutedQueriesKey]; !hasQueries {
		llmContext.Parameters[mmtools.WebSearchExecutedQueriesKey] = []string{}
	}

	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		prefs, err := mcp.LoadUserPreferences(c.mmClient, postingUser.Id)
		if err != nil {
			c.mmClient.LogWarn("Failed to load user tool preferences", "error", err.Error(), "userID", postingUser.Id)
		} else if len(prefs.DisabledServers) > 0 {
			if llmContext.Tools != nil {
				llmContext.Tools.RemoveToolsByServerOrigin(prefs.DisabledServers)
			}
		}
	}

	if llmContext.Tools != nil {
		authErrors := llmContext.Tools.GetAuthErrors()
		if len(authErrors) > 0 {
			rootID := post.RootId
			if rootID == "" {
				rootID = post.Id
			}
			c.sendOAuthNotifications(bot, postingUser.Id, channel.Id, rootID, authErrors)
		}
	}

	responseRootID := post.Id
	if post.RootId != "" {
		responseRootID = post.RootId
	}

	responsePost := &model.Post{
		ChannelId: channel.Id,
		RootId:    responseRootID,
	}
	if err := c.createResponsePlaceholder(bot.GetMMBot().UserId, postingUser.Id, responsePost, post.Id); err != nil {
		return fmt.Errorf("unable to create response placeholder: %w", err)
	}

	dmResult, err := c.ProcessDMRequest(bot.GetMMBot().UserId, bot.LLM(), postingUser, channel, post, llmContext)
	if err != nil {
		c.failResponsePlaceholder(responsePost, postingUser.Locale)
		return fmt.Errorf("unable to process DM request: %w", err)
	}

	responsePost.AddProp(streaming.ConversationIDProp, dmResult.ConversationID)
	if updateErr := c.mmClient.UpdatePost(responsePost); updateErr != nil {
		c.mmClient.LogError("Failed to set conversation_id prop", "error", updateErr)
	}

	if streamErr := c.streamResponseToExistingPost(dmResult.Stream, responsePost, postingUser, channel); streamErr != nil {
		c.failResponsePlaceholder(responsePost, postingUser.Locale)
		return fmt.Errorf("unable to stream response: %w", streamErr)
	}

	if dmResult.IsNew {
		go func() {
			if titleErr := c.convService.GenerateTitle(dmResult.ConversationID, bot.LLM(), post.Message, llmContext); titleErr != nil {
				c.mmClient.LogError("Failed to generate title", "error", titleErr.Error())
			}
		}()
	}

	return nil
}

func (c *Conversations) createResponsePlaceholder(botID, requesterUserID string, post *model.Post, respondingToPostID string) error {
	streaming.ModifyPostForBot(botID, requesterUserID, post, respondingToPostID)
	return c.mmClient.CreatePost(post)
}

func (c *Conversations) streamResponseToExistingPost(stream *llm.TextStreamResult, post *model.Post, postingUser *model.User, channel *model.Channel) error {
	ctx, err := c.streamingService.GetStreamingContext(context.Background(), post.Id)
	if err != nil {
		return err
	}

	locale := c.responseLocale(postingUser, channel)
	go func() {
		defer c.streamingService.FinishStreaming(post.Id)
		c.streamingService.StreamToPost(ctx, stream, post, locale)
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
	if config := c.mmClient.GetConfig(); config != nil && config.LocalizationSettings.DefaultServerLocale != nil && *config.LocalizationSettings.DefaultServerLocale != "" {
		return *config.LocalizationSettings.DefaultServerLocale
	}
	return "en"
}
