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
// the requested conversation. Auth mirrors handleGetConversation — channel
// members (or DM owner) only. No LLM call is made: the composition is
// computed from stored turns. The provider's CountTokens is preferred when
// available; otherwise we fall back to the heuristic estimator and mark the
// Composition.TotalSource as "estimated".
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

	// Reuse the exact assembly path the production runtime uses
	// (conversation.Service.BuildCompletionRequest delegates to this), so
	// the breakdown reflects what providers actually see — including
	// provider-specific shape requirements like Anthropic's alternating
	// user/assistant roles, without which CountTokens would fail and the
	// total would silently fall back to "estimated".
	enableVision, maxFileSize := a.attachmentConfigForBot(conv.BotID)
	llmCtx := a.buildContextForConversation(userID, conv)
	req, err := conversation.AssembleRequest(conv, turns, llmCtx, a.mmClient, enableVision, maxFileSize)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to build composition: %w", err))
		return
	}

	modelName, tokenLimit := a.modelMetadataForBot(conv.BotID)
	total, totalSource := a.totalTokensForRequest(c, conv.BotID, req)

	composition := llm.ComputeComposition(req.Composition, total, totalSource)
	composition.Model = modelName
	composition.InputTokenLimit = tokenLimit

	c.JSON(http.StatusOK, composition)
}

// buildContextForConversation builds an llm.Context that matches what the
// runtime would assemble for this user + bot + channel — most importantly,
// with Tools populated so AssembleRequest can emit per-tool composition
// inputs (the "tool_defs" rows in the breakdown). Without this, the popover
// silently drops tool_defs and under-reports context for tool-heavy bots.
//
// CountTokens itself still won't see tools (the bifrost adapter strips
// Params on count_tokens because providers reject them), but the breakdown
// can still attribute proportions to tool_defs from the runtime weighting.
//
// Returns an empty Context when the inputs needed to populate it aren't
// available (e.g. unit tests that don't wire bots/contextBuilder, or the
// user/channel lookup fails) — the endpoint degrades to history-only
// composition rather than failing.
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

// attachmentConfigForBot reads the bot's EnableVision and MaxFileSize for
// rendering attachments at composition time. Falls back to vision-off and
// the conversation package's DefaultMaxFileSize when the bot lookup isn't
// wired up (typical of unit tests).
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

// modelMetadataForBot reads the bot's LanguageModel (if available) for the
// model name and input token limit. Returns ("", 0) when the bot or LLM is
// not wired up, which is the common path for unit tests.
//
// Logs a Warn when the token limit reports zero on a wired LLM, since the
// webapp can't draw a ring without a denominator and there's no other path
// to see why the meter was hidden. Common causes: the system console value
// hasn't been persisted, or the bot was constructed before the service
// config gained a limit and the config-update listener didn't refresh it.
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

// totalTokensForRequest returns the authoritative token total. Prefers the
// provider's CountTokens on the already-assembled request (so providers like
// Anthropic that require alternating roles don't reject the shape); falls
// back to summing EstimateTokens across composition inputs when the provider
// can't count or no bot LLM is wired.
//
// Each fallback branch logs a Warn with enough context to diagnose without
// also having to repro locally. The user-visible "Total is estimated" caveat
// has no way to surface *why* we estimated — when a Claude session shows
// "estimated" even though Anthropic supports CountTokens, these logs are the
// only signal that tells us which step dropped out.
func (a *API) totalTokensForRequest(c *gin.Context, botID string, req *llm.CompletionRequest) (int, string) {
	count, ok := a.tryCountTokens(c, botID, req)
	if ok {
		return count, llm.CompositionTotalCounted
	}

	return llm.EstimateRequestTokens(req.Composition), llm.CompositionTotalEstimated
}

// tryCountTokens runs the provider's CountTokens path and returns ok=true on
// success. The fallback paths each log a Warn so we can diagnose "Total is
// estimated" reports from the field — the user-visible caveat has no room
// to explain *why* we estimated.
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
		a.pluginAPI.Log.Warn("context endpoint estimating tokens: provider CountTokens failed",
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
