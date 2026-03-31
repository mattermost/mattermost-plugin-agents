// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
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

	responsePost := &model.Post{
		ChannelId: channel.Id,
		RootId:    responseRootID,
	}
	setAllowToolsInChannelProp(responsePost, allowToolsInChannel)
	return c.respondToPost(bot, postingUser, channel, responsePost, post.Id, func() (*llm.TextStreamResult, error) {
		stream, err := c.ProcessUserRequest(bot, postingUser, channel, post, allowToolsInChannel)
		if err != nil {
			return nil, fmt.Errorf("unable to process bot mention: %w", err)
		}
		return stream, nil
	})
}

func (c *Conversations) handleDMs(bot *bots.Bot, channel *model.Channel, postingUser *model.User, post *model.Post) error {
	if err := c.bots.CheckUsageRestrictionsForUser(bot, postingUser.Id); err != nil {
		return err
	}

	responseRootID := post.Id
	if post.RootId != "" {
		responseRootID = post.RootId
	}

	responsePost := &model.Post{
		ChannelId: channel.Id,
		RootId:    responseRootID,
	}
	return c.respondToPost(bot, postingUser, channel, responsePost, post.Id, func() (*llm.TextStreamResult, error) {
		stream, err := c.ProcessUserRequest(bot, postingUser, channel, post, false)
		if err != nil {
			return nil, fmt.Errorf("unable to process bot mention: %w", err)
		}
		return stream, nil
	})
}

func (c *Conversations) respondToPost(
	bot *bots.Bot,
	postingUser *model.User,
	channel *model.Channel,
	responsePost *model.Post,
	respondingToPostID string,
	buildStream func() (*llm.TextStreamResult, error),
) error {
	if err := c.createResponsePlaceholder(bot.GetMMBot().UserId, postingUser.Id, responsePost, respondingToPostID); err != nil {
		return fmt.Errorf("unable to create response placeholder: %w", err)
	}

	stream, err := buildStream()
	if err != nil {
		c.failResponsePlaceholder(responsePost, postingUser.Locale)
		return err
	}

	if err := c.streamResponseToExistingPost(stream, responsePost, postingUser, channel); err != nil {
		c.failResponsePlaceholder(responsePost, postingUser.Locale)
		return fmt.Errorf("unable to stream response: %w", err)
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
