// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/customprompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// handleCreateCustomPrompt creates a new custom prompt for the authenticated user.
func (a *API) handleCreateCustomPrompt(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	var prompt customprompts.CustomPrompt
	if err := c.ShouldBindJSON(&prompt); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	prompt.CreatorID = userID

	if err := prompt.Validate(); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	created, err := a.customPromptsStore.Create(prompt)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to create custom prompt: %w", err))
		return
	}

	c.JSON(http.StatusCreated, created)
}

// handleListCustomPrompts returns all prompts visible to the authenticated user.
func (a *API) handleListCustomPrompts(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	prompts, err := a.customPromptsStore.ListForUser(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to list custom prompts: %w", err))
		return
	}

	c.JSON(http.StatusOK, prompts)
}

// handleUpdateCustomPrompt updates an existing custom prompt. Only the creator can update.
func (a *API) handleUpdateCustomPrompt(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	promptID := c.Param("id")

	if _, ok := a.requirePromptOwnership(c, promptID, userID); !ok {
		return
	}

	var prompt customprompts.CustomPrompt
	if err := c.ShouldBindJSON(&prompt); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	prompt.ID = promptID
	prompt.CreatorID = userID

	if err := prompt.Validate(); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if err := a.customPromptsStore.Update(prompt); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to update custom prompt: %w", err))
		return
	}

	c.Status(http.StatusNoContent)
}

// handleDeleteCustomPrompt soft-deletes a custom prompt. Only the creator can delete.
func (a *API) handleDeleteCustomPrompt(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	promptID := c.Param("id")

	if _, ok := a.requirePromptOwnership(c, promptID, userID); !ok {
		return
	}

	if err := a.customPromptsStore.Delete(promptID, userID); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to delete custom prompt: %w", err))
		return
	}

	c.Status(http.StatusNoContent)
}

// handleGetPromptPins returns the pinned prompt IDs for the authenticated user.
func (a *API) handleGetPromptPins(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	ids, err := a.customPromptsStore.GetPinnedIDs(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get pinned prompts: %w", err))
		return
	}

	c.JSON(http.StatusOK, ids)
}

// SetPinRequest represents the request body for pinning/unpinning a prompt.
type SetPinRequest struct {
	PromptID string `json:"prompt_id"`
	Pinned   bool   `json:"pinned"`
}

// handleSetPromptPin pins or unpins a prompt for the authenticated user.
func (a *API) handleSetPromptPin(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	var req SetPinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if req.PromptID == "" {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("prompt_id is required"))
		return
	}

	if err := a.customPromptsStore.SetPinned(userID, req.PromptID, req.Pinned); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to set prompt pin: %w", err))
		return
	}

	c.Status(http.StatusOK)
}

// RenderRequest represents the request body for rendering a custom prompt template.
type RenderRequest struct {
	ChannelID   string `json:"channel_id"`
	BotUsername string `json:"bot_username"`
}

// handleRenderCustomPrompt renders a custom prompt template with the current context.
func (a *API) handleRenderCustomPrompt(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	promptID := c.Param("id")

	var req RenderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	prompt, err := a.customPromptsStore.Get(promptID)
	if err != nil {
		c.AbortWithError(http.StatusNotFound, fmt.Errorf("prompt not found: %w", err))
		return
	}

	// Enforce visibility: only the creator or shared prompts are accessible
	if prompt.CreatorID != userID && !prompt.IsShared {
		c.AbortWithError(http.StatusNotFound, errors.New("prompt not found or not accessible"))
		return
	}

	// Build context options
	var opts []llm.ContextOption

	if a.contextBuilder != nil {
		opts = append(opts, a.contextBuilder.WithLLMContextServerInfo())
	}

	// Add requesting user context
	user, appErr := a.pluginAPI.User.Get(userID)
	if appErr != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get user: %w", appErr))
		return
	}
	if a.contextBuilder != nil {
		opts = append(opts, a.contextBuilder.WithLLMContextRequestingUser(user))
	}

	// Add channel context if provided
	if req.ChannelID != "" {
		if !a.pluginAPI.User.HasPermissionToChannel(userID, req.ChannelID, model.PermissionReadChannel) {
			c.AbortWithError(http.StatusForbidden, errors.New("user doesn't have permission to read channel"))
			return
		}
		channel, channelErr := a.pluginAPI.Channel.Get(req.ChannelID)
		if channelErr != nil {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("failed to get channel: %w", channelErr))
			return
		}
		if a.contextBuilder != nil {
			opts = append(opts, a.contextBuilder.WithLLMContextChannel(channel))
		}
	}

	// Add bot context if provided
	if req.BotUsername != "" {
		bot := a.bots.GetBotByUsernameOrFirst(req.BotUsername)
		if bot != nil && a.contextBuilder != nil {
			opts = append(opts, a.contextBuilder.WithLLMContextBot(bot))
		}
	}

	ctx := llm.NewContext(opts...)

	// Render the template with only whitelisted variables
	rendered, renderErr := a.prompts.FormatString(prompt.Template, ctx.CustomPromptVars())
	if renderErr != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to render prompt: %w", renderErr))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"rendered": rendered,
	})
}
