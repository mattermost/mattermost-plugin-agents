// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/llm"
)

type SimpleCompletionRequest struct {
	SystemPrompt    string         `json:"systemPrompt"`
	UserPrompt      string         `json:"userPrompt"`
	BotUsername     string         `json:"botUsername"`
	RequesterUserID string         `json:"requesterUserID"`
	Parameters      map[string]any `json:"parameters"`
}

func (a *API) handleInterPluginSimpleCompletion(c *gin.Context) {
	var req SimpleCompletionRequest
	if err := c.BindJSON(&req); err != nil {
		return
	}

	userID := req.RequesterUserID
	if userID == "" {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("requesterUserID is required"))
		return
	}

	// If bot username is not provided, use the default bot
	botUsername := req.BotUsername
	if botUsername == "" {
		botUsername = a.config.GetDefaultBotName()
	}

	// Get the bot by username or use the first available bot
	bot := a.bots.GetBotByUsernameOrFirst(botUsername)
	if bot == nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("bot not found: %s", botUsername))
		return
	}

	// Get user information
	user, err := a.pluginAPI.User.Get(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get user: %v", err))
		return
	}

	// Create a proper context for the LLM
	context := a.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		nil, // No channel for inter-plugin requests
		a.contextBuilder.WithLLMContextParameters(req.Parameters),
		a.contextBuilder.WithLLMContextDefaultTools(bot, true),
	)

	// Format system prompt using template
	formattedSystemPrompt, err := a.prompts.FormatString(req.SystemPrompt, context)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to format system prompt: %v", err))
		return
	}

	formattedUserPrompt, err := a.prompts.FormatString(req.UserPrompt, context)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to format user prompt: %v", err))
		return
	}

	// Create a completion request with system prompt and user prompt
	completionRequest := llm.CompletionRequest{
		Posts: []llm.Post{
			{
				Role:    llm.PostRoleSystem,
				Message: formattedSystemPrompt,
			},
			{
				Role:    llm.PostRoleUser,
				Message: formattedUserPrompt,
			},
		},
		Context: context,
	}

	// Execute the completion
	response, err := bot.LLM().ChatCompletionNoStream(completionRequest)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to execute chat completion: %v", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"response": response,
	})
}
