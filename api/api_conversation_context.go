// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
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

	enableVision, maxFileSize := a.attachmentConfigForBot(conv.BotID)
	inputs, err := composeInputsFromConversation(conv, turns, a.mmClient, enableVision, maxFileSize)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to build composition: %w", err))
		return
	}

	modelName, tokenLimit := a.modelMetadataForBot(conv.BotID)
	total, totalSource := a.totalTokensForConversation(c, conv.BotID, inputs)

	composition := llm.ComputeComposition(inputs, total, totalSource)
	composition.Model = modelName
	composition.InputTokenLimit = tokenLimit

	c.JSON(http.StatusOK, composition)
}

// composeInputsFromConversation walks stored turns and emits the same
// CompositionInputs that BuildCompletionRequest would produce, minus the
// tool_defs (which depend on per-user MCP state and aren't materialized
// from stored data). Mirrors the merge of assistant + tool_result turns
// from turnsToLLMPosts so tool_use/result content shows up under
// tool_results in the breakdown.
func composeInputsFromConversation(
	conv *store.Conversation,
	turns []store.Turn,
	mmClient mmapi.Client,
	enableVision bool,
	maxFileSize int64,
) ([]llm.CompositionInput, error) {
	var inputs []llm.CompositionInput
	if conv.SystemPrompt != "" {
		inputs = append(inputs, llm.CompositionInput{Source: llm.SourceSystem, Text: conv.SystemPrompt})
	}

	for i := 0; i < len(turns); i++ {
		turn := turns[i]
		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turn.Content, &blocks); err != nil {
			return nil, fmt.Errorf("failed to unmarshal turn %s content: %w", turn.ID, err)
		}
		if turn.Role == "assistant" && i+1 < len(turns) && turns[i+1].Role == "tool_result" {
			var nextBlocks []conversation.ContentBlock
			if err := json.Unmarshal(turns[i+1].Content, &nextBlocks); err != nil {
				return nil, fmt.Errorf("failed to unmarshal turn %s content: %w", turns[i+1].ID, err)
			}
			blocks = append(blocks, nextBlocks...)
			i++
		}
		// Default to redactUnshared=true so the breakdown matches what a
		// non-owner viewer would actually see flowed to the LLM. The auth
		// check above doesn't differentiate owner vs. channel-member, so
		// fail-safe redaction is the right default here.
		_, postComposition := conversation.BlocksToPost(blocks, turn.Role, true, mmClient, enableVision, maxFileSize)
		inputs = append(inputs, postComposition...)
	}
	return inputs, nil
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
	return cfg.Model, lm.InputTokenLimit()
}

// totalTokensForConversation returns the authoritative token total. Prefers
// the provider's CountTokens; falls back to summing EstimateTokens.
func (a *API) totalTokensForConversation(c *gin.Context, botID string, inputs []llm.CompositionInput) (int, string) {
	if a.bots != nil {
		if bot := a.bots.GetBotByID(botID); bot != nil {
			if lm := bot.LLM(); lm != nil {
				req := llm.CompletionRequest{}
				for _, in := range inputs {
					req.Posts = append(req.Posts, llm.Post{Message: in.Text})
				}
				count, err := lm.CountTokens(c.Request.Context(), req)
				if err == nil {
					return count, llm.CompositionTotalCounted
				}
			}
		}
	}

	var sum int
	for _, in := range inputs {
		sum += llm.EstimateTokens(in.Text)
	}
	return sum, llm.CompositionTotalEstimated
}
