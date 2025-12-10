// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	stdcontext "context"
	"encoding/json"
	"net/http"

	"errors"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/channels"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	TitleThreadSummary     = "Thread Summary"
	TitleSummarizeUnreads  = "Summarize Unreads"
	TitleSummarizeChannel  = "Summarize Channel"
	TitleFindActionItems   = "Find Action Items"
	TitleFindOpenQuestions = "Find Open Questions"
)

func (a *API) channelAuthorizationRequired(c *gin.Context) {
	channelID := c.Param("channelid")
	userID := c.GetHeader("Mattermost-User-Id")

	channel, err := a.pluginAPI.Channel.Get(channelID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.Set(ContextChannelKey, channel)

	if !a.pluginAPI.User.HasPermissionToChannel(userID, channel.Id, model.PermissionReadChannel) {
		c.AbortWithError(http.StatusForbidden, errors.New("user doesn't have permission to read channel"))
		return
	}

	bot := c.MustGet(ContextBotKey).(*bots.Bot)
	if err := a.bots.CheckUsageRestrictions(userID, bot, channel); err != nil {
		c.AbortWithError(http.StatusForbidden, err)
		return
	}
}

func (a *API) handleInterval(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	// Check license
	if !a.licenseChecker.IsBasicsLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
		return
	}

	// Parse request data
	data := struct {
		StartTime    int64  `json:"start_time"`
		EndTime      int64  `json:"end_time"` // 0 means "until present"
		PresetPrompt string `json:"preset_prompt"`
		Prompt       string `json:"prompt"`
	}{}
	err := json.NewDecoder(c.Request.Body).Decode(&data)
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	defer c.Request.Body.Close()

	// Validate time range
	if data.EndTime != 0 && data.StartTime >= data.EndTime {
		c.AbortWithError(http.StatusBadRequest, errors.New("start_time must be before end_time"))
		return
	}

	// Cap the date range at 14 days
	maxDuration := int64(14 * 24 * 60 * 60) // 14 days in seconds
	if data.EndTime != 0 && (data.EndTime-data.StartTime) > maxDuration {
		c.AbortWithError(http.StatusBadRequest, errors.New("date range cannot exceed 14 days"))
		return
	}

	// Get user
	user, err := a.pluginAPI.User.Get(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Build LLM context
	context := a.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		channel,
		a.contextBuilder.WithLLMContextDefaultTools(bot),
	)

	// Map preset prompt to prompt type and title
	promptPreset := ""
	promptTitle := ""
	switch data.PresetPrompt {
	case "summarize_unreads":
		promptPreset = prompts.PromptSummarizeChannelSinceSystem
		promptTitle = TitleSummarizeUnreads
	case "summarize_range":
		promptPreset = prompts.PromptSummarizeChannelRangeSystem
		promptTitle = TitleSummarizeChannel
	case "action_items":
		promptPreset = prompts.PromptFindActionItemsSystem
		promptTitle = TitleFindActionItems
	case "open_questions":
		promptPreset = prompts.PromptFindOpenQuestionsSystem
		promptTitle = TitleFindOpenQuestions
	default:
		c.AbortWithError(http.StatusBadRequest, errors.New("invalid preset prompt"))
		return
	}

	// Call channels interval processing
	resultStream, err := channels.New(bot.LLM(), a.prompts, a.mmClient, a.dbClient).Interval(context, channel.Id, data.StartTime, data.EndTime, promptPreset)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Create post for the response
	post := &model.Post{}
	post.AddProp(streaming.NoRegen, "true")

	// Stream result to new DM
	if err := a.streamingService.StreamToNewDM(stdcontext.Background(), bot.GetMMBot().UserId, resultStream, user.Id, post, ""); err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Save title asynchronously
	a.conversationsService.SaveTitleAsync(post.Id, promptTitle)

	// Return result
	result := map[string]string{
		"postid":    post.Id,
		"channelid": post.ChannelId,
	}

	c.Render(http.StatusOK, render.JSON{Data: result})
}
