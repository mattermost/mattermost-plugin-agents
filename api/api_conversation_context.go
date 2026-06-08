// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// handleGetConversationContext returns the per-source token composition for
// the requested conversation. Auth mirrors handleGetConversation. No LLM
// call is made; the composition is computed from stored turns.
func (a *API) handleGetConversationContext(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	conversationID := c.Param("conversationid")

	conv, err := a.conversationStore.GetConversation(conversationID)
	if err != nil {
		if errors.Is(err, store.ErrConversationNotFound) {
			c.AbortWithError(http.StatusNotFound, fmt.Errorf("conversation not found"))
			return
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get conversation: %w", err))
		return
	}

	if conv.ChannelID != nil {
		if !a.pluginAPI.User.HasPermissionToChannel(userID, *conv.ChannelID, model.PermissionReadChannel) {
			c.AbortWithError(http.StatusForbidden, fmt.Errorf("user doesn't have permission to this conversation"))
			return
		}
	} else if userID != conv.UserID {
		c.AbortWithError(http.StatusForbidden, fmt.Errorf("user doesn't have permission to this conversation"))
		return
	}

	turns, err := a.conversationStore.GetTurnsForConversation(conv.ID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get turns: %w", err))
		return
	}

	// Same assembly the runtime uses so providers see the same shape (e.g.
	// Anthropic's alternating-role requirement, which CountTokens enforces).
	enableVision, maxFileSize := a.attachmentConfigForBot(conv.BotID)
	llmCtx := a.buildContextForConversation(userID, conv)
	req, err := conversation.AssembleRequest(conv, turns, llmCtx, a.mmClient, enableVision, maxFileSize)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to build composition: %w", err))
		return
	}

	modelName, tokenLimit := a.modelMetadataForBot(conv.BotID)
	total, totalSource := a.totalTokensForRequest(c, conv.BotID, req)

	composition := llm.ComputeComposition(req.Composition(), total, totalSource)
	composition.Model = modelName
	composition.InputTokenLimit = tokenLimit

	c.JSON(http.StatusOK, composition)
}

// buildContextForConversation populates llm.Context with the same Tools the
// runtime would attach for this user+bot+channel, so AssembleRequest emits
// tool_defs composition rows. Returns an empty Context when prerequisites
// aren't wired (unit tests, missing bot/user/channel).
func (a *API) buildContextForConversation(userID string, conv *store.Conversation) *llm.Context {
	if a.contextBuilder == nil || a.bots == nil {
		return &llm.Context{}
	}
	bot := a.bots.GetBotByID(conv.BotID)
	if bot == nil {
		return &llm.Context{}
	}
	user, err := a.pluginAPI.User.Get(userID)
	if err != nil {
		return &llm.Context{}
	}
	var channel *model.Channel
	if conv.ChannelID != nil {
		ch, chErr := a.pluginAPI.Channel.Get(*conv.ChannelID)
		if chErr == nil {
			channel = ch
		}
	}
	return a.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		channel,
		a.contextBuilder.WithLLMContextTools(bot),
	)
}

// attachmentConfigForBot reads the bot's EnableVision and MaxFileSize, with
// safe fallbacks when the bot isn't wired (unit tests).
func (a *API) attachmentConfigForBot(botID string) (bool, int64) {
	if a.bots == nil {
		return false, conversation.DefaultMaxFileSize
	}
	enableVision, maxFileSize, ok := a.bots.GetBotConfigByID(botID)
	if !ok {
		return false, conversation.DefaultMaxFileSize
	}
	if maxFileSize <= 0 {
		maxFileSize = conversation.DefaultMaxFileSize
	}
	return enableVision, maxFileSize
}

// modelMetadataForBot returns the bot's model name and input token limit,
// or ("", 0) when the LLM isn't wired. Warns on a zero limit so a hidden
// utilization ring leaves a server-side breadcrumb.
func (a *API) modelMetadataForBot(botID string) (string, int) {
	if a.bots == nil {
		return "", 0
	}
	bot := a.bots.GetBotByID(botID)
	if bot == nil {
		return "", 0
	}
	cfg := bot.GetConfig()
	lm := bot.LLM()
	if lm == nil {
		return cfg.Model, 0
	}
	limit := lm.InputTokenLimit()
	if limit == 0 {
		a.pluginAPI.Log.Warn("context endpoint: bot reports zero input token limit",
			"bot_id", botID,
			"bot_name", cfg.Name,
			"service_type", bot.GetService().Type,
			"model", cfg.Model,
			"hint", "set 'Input token limit' in the system console AI service page, "+
				"or restart the plugin if a recently-saved value isn't taking effect",
		)
	}
	return cfg.Model, limit
}

// totalTokensForRequest returns the authoritative token total, preferring
// the provider's CountTokens and falling back to the heuristic estimator.
func (a *API) totalTokensForRequest(c *gin.Context, botID string, req *llm.CompletionRequest) (int, string) {
	count, ok := a.tryCountTokens(c, botID, req)
	if ok {
		return count, llm.CompositionTotalCounted
	}

	return llm.EstimateRequestTokens(req.Composition()), llm.CompositionTotalEstimated
}

// tryCountTokens runs the provider's CountTokens path. Each fallback branch
// Warns with the path that dropped out so "Total is estimated" reports from
// the field leave a server-side trail (the user-visible caveat is unspecific).
func (a *API) tryCountTokens(c *gin.Context, botID string, req *llm.CompletionRequest) (int, bool) {
	if a.bots == nil {
		a.pluginAPI.Log.Warn("context endpoint estimating tokens: bot lookup unavailable",
			"bot_id", botID,
		)
		return 0, false
	}
	bot := a.bots.GetBotByID(botID)
	if bot == nil {
		a.pluginAPI.Log.Warn("context endpoint estimating tokens: bot not found",
			"bot_id", botID,
		)
		return 0, false
	}
	lm := bot.LLM()
	if lm == nil {
		a.pluginAPI.Log.Warn("context endpoint estimating tokens: bot has no LLM wired",
			"bot_id", botID,
			"bot_name", bot.GetConfig().Name,
		)
		return 0, false
	}
	count, err := lm.CountTokens(c.Request.Context(), *req)
	if err != nil {
		// Unsupported counting is an expected capability miss, not a failure.
		level := a.pluginAPI.Log.Warn
		if errors.Is(err, llm.ErrUnsupportedTokenCount) {
			level = a.pluginAPI.Log.Debug
		}
		level("context endpoint estimating tokens: provider CountTokens failed",
			"bot_id", botID,
			"bot_name", bot.GetConfig().Name,
			"service_type", bot.GetService().Type,
			"model", bot.GetConfig().Model,
			"error", err.Error(),
		)
		return 0, false
	}
	return count, true
}
