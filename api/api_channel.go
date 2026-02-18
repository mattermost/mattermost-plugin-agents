// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	stdcontext "context"
	"encoding/json"
	"fmt"
	"net/http"

	"errors"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/channels"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	TitleSummarizeUnreads = "Summarize Unreads"
	TitleSummarizeChannel = "Summarize Channel"
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

func (a *API) handleChannelAnalysis(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if !a.licenseChecker.IsBasicsLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
		return
	}

	var data struct {
		AnalysisType string `json:"analysis_type" binding:"required"`
		Since        string `json:"since"`
		Until        string `json:"until"`
		Days         int    `json:"days"`
		Prompt       string `json:"prompt"`
		TeamID       string `json:"team_id"`
	}
	if bindErr := c.ShouldBindJSON(&data); bindErr != nil {
		c.AbortWithError(http.StatusBadRequest, bindErr)
		return
	}

	const maxAnalysisDays = 14
	if data.Days < 0 || data.Days > maxAnalysisDays {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("days must be between 0 and %d", maxAnalysisDays))
		return
	}

	// Get the user to build context
	user, err := a.pluginAPI.User.Get(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("unable to get user: %w", err))
		return
	}

	opts := []llm.ContextOption{
		a.contextBuilder.WithLLMContextDefaultTools(bot),
	}

	// If the channel is a DM/GM and we have a team ID from the client, use it for context
	if (channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup) && data.TeamID != "" {
		team, teamErr := a.pluginAPI.Team.Get(data.TeamID)
		if teamErr == nil && team != nil {
			opts = append(opts, func(c *llm.Context) {
				c.Team = team
			})
		}
	}

	// Build LLM context with default tools enabled
	llmContext := a.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		channel,
		opts...,
	)

	// Validate that required tools are available for channel analysis
	// The read_channel tool is essential for this feature
	if llmContext.Tools == nil {
		a.pluginAPI.Log.Error("Channel analysis failed: no tools available in context",
			"userID", userID,
			"channelID", channel.Id)
		c.AbortWithError(http.StatusInternalServerError, errors.New("channel analysis requires MCP tools which are not available - check embedded server configuration"))
		return
	}

	// Check if read_channel tool is available
	availableTools := llmContext.Tools.GetTools()
	hasReadChannel := false
	var toolNames []string
	for _, tool := range availableTools {
		toolNames = append(toolNames, tool.Name)
		if tool.Name == "read_channel" {
			hasReadChannel = true
		}
	}

	if !hasReadChannel {
		a.pluginAPI.Log.Error("Channel analysis failed: read_channel tool not available",
			"userID", userID,
			"channelID", channel.Id,
			"availableTools", toolNames)
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("channel analysis requires read_channel tool which is not available (found %d tools: %v) - ensure embedded MCP server is enabled and working", len(availableTools), toolNames))
		return
	}

	// Create channels analyzer
	// We need to initialize Channels service. Since it's not in API struct, we initialize it here.
	// Ideally, it should be initialized in API constructor and passed as a dependency.
	// For now, let's create it.
	analyzer := channels.New(bot.LLM(), a.prompts, a.mmClient, a.dbClient)

	// Prepare analysis data for the prompt
	analysisData := map[string]any{
		"AnalysisType": data.AnalysisType,
		"Since":        data.Since,
		"Until":        data.Until,
		"Days":         data.Days,
		"Prompt":       data.Prompt,
	}

	analysisStream, err := analyzer.AnalyzeChannel(llmContext, channel.Id, analysisData)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to analyze channel: %w", err))
		return
	}

	// Create analysis post
	siteURL := a.pluginAPI.Configuration.GetConfig().ServiceSettings.SiteURL
	if siteURL == nil || *siteURL == "" {
		c.AbortWithError(http.StatusInternalServerError, errors.New("site URL not configured"))
		return
	}
	analysisPost := a.makeAnalysisPost(user.Locale, "", data.AnalysisType, *siteURL)

	if err := a.streamingService.StreamToNewDM(stdcontext.Background(), bot.GetMMBot().UserId, analysisStream, user.Id, analysisPost, ""); err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	a.conversationsService.SaveTitleAsync(analysisPost.Id, TitleSummarizeChannel)

	c.JSON(http.StatusOK, map[string]string{
		"postid":    analysisPost.Id,
		"channelid": analysisPost.ChannelId,
	})
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
