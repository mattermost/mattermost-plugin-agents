// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/react"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost-plugin-agents/v2/threads"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	TitleThreadSummary     = "Thread Summary"
	TitleFindActionItems   = "Action Items"
	TitleFindOpenQuestions = "Open Questions"
)

func (a *API) postAuthorizationRequired(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	postID := c.Param("postid")

	post, err := a.pluginAPI.Post.GetPost(postID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.Set(ContextPostKey, post)

	channel, err := a.pluginAPI.Channel.Get(post.ChannelId)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.Set(ContextChannelKey, channel)

	if !a.pluginAPI.User.HasPermissionToChannel(userID, channel.Id, model.PermissionReadChannel) {
		c.AbortWithError(http.StatusForbidden, errors.New("user doesn't have permission to read channel post in in"))
		return
	}

	bot := c.MustGet(ContextBotKey).(*bots.Bot)
	if err := a.bots.CheckUsageRestrictions(userID, bot, channel); err != nil {
		c.AbortWithError(http.StatusForbidden, err)
		return
	}
}

func (a *API) handleReact(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	requestingUser, err := a.pluginAPI.User.Get(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	context := a.contextBuilder.BuildLLMContextUserRequest(
		bot,
		requestingUser,
		channel,
	)

	emojiName, err := react.New(
		bot.LLM(),
		a.prompts,
	).Resolve(c.Request.Context(), post.Message, context)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Add reaction to the post as the requesting user
	if err := a.pluginAPI.Post.AddReaction(&model.Reaction{
		EmojiName: emojiName,
		UserId:    userID,
		PostId:    post.Id,
	}); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to add reaction: %w", err))
	}

	c.Status(http.StatusOK)
}

func (a *API) handleThreadAnalysis(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if !a.licenseChecker.IsBasicsLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
		return
	}

	var data struct {
		AnalysisType string `json:"analysis_type" binding:"required"`
	}
	if bindErr := c.ShouldBindJSON(&data); bindErr != nil {
		c.AbortWithError(http.StatusBadRequest, bindErr)
		return
	}

	switch data.AnalysisType {
	case "summarize_thread":
		// Valid analysis type for thread summarization
	case "action_items":
		// Valid analysis type for finding action items
	case "open_questions":
		// Valid analysis type for finding open questions
	default:
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid analysis type: %s", data.AnalysisType))
		return
	}

	// Get the user to build context
	user, err := a.pluginAPI.User.Get(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("unable to get user: %w", err))
		return
	}

	// Thread analysis disables tools, so skip MCP/tool initialization entirely.
	llmContext := a.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		channel,
		a.contextBuilder.WithLLMContextNoTools(),
	)

	// Create thread analyzer with conversation service
	botUserID := bot.GetMMBot().UserId
	analyzer := threads.New(bot.LLM(), a.prompts, a.mmClient, a.convService)
	var analyzeResult *threads.AnalyzeResult
	var title string
	switch data.AnalysisType {
	case "summarize_thread":
		title = TitleThreadSummary
		analyzeResult, err = analyzer.Summarize(c.Request.Context(), post.Id, llmContext, botUserID, userID)
	case "action_items":
		title = TitleFindActionItems
		analyzeResult, err = analyzer.FindActionItems(c.Request.Context(), post.Id, llmContext, botUserID, userID)
	case "open_questions":
		title = TitleFindOpenQuestions
		analyzeResult, err = analyzer.FindOpenQuestions(c.Request.Context(), post.Id, llmContext, botUserID, userID)
	}
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to analyze thread: %w", err))
		return
	}

	// Create analysis post with conversation ID
	analysisPost := a.makeAnalysisPost(user.Locale, post.Id, data.AnalysisType, analyzeResult.ConversationID)
	if err := a.streamingService.StreamToNewDM(telemetry.DetachContext(c.Request.Context()), botUserID, analyzeResult.Stream, user.Id, analysisPost, post.Id); err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Update conversation's RootPostID to the analysis DM post (now that it's been created)
	if a.convService != nil {
		if updateErr := a.convService.UpdateConversationRootPostID(analyzeResult.ConversationID, analysisPost.Id); updateErr != nil {
			// Log the error but don't fail the request -- the analysis was already streamed
			a.mmClient.LogError("Failed to update conversation root post ID", "error", updateErr.Error())
		}
		// Set title directly (no LLM call needed for fixed analysis titles)
		if titleErr := a.convService.UpdateConversationTitle(analyzeResult.ConversationID, title); titleErr != nil {
			a.mmClient.LogError("Failed to set conversation title", "error", titleErr.Error())
		}
	}

	c.JSON(http.StatusOK, map[string]string{
		"postid":    analysisPost.Id,
		"channelid": analysisPost.ChannelId,
	})
}

func (a *API) handleTranscribeFile(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	fileID := c.Param("fileid")
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	result, err := a.meetingsService.HandleTranscribeFile(userID, bot, post, channel, fileID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.Render(http.StatusOK, render.JSON{Data: result})
}

func (a *API) handleSummarizeTranscription(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	result, err := a.meetingsService.HandleSummarizeTranscription(userID, bot, post, channel)
	if err != nil {
		if err.Error() == "not a calls or zoom bot post" {
			c.AbortWithError(http.StatusBadRequest, errors.New("not a calls or zoom bot post"))
			return
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("unable to summarize transcription: %w", err))
		return
	}

	c.Render(http.StatusOK, render.JSON{Data: result})
}

func (a *API) handleStop(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	botID := post.UserId
	if !a.bots.IsAnyBot(botID) {
		c.AbortWithError(http.StatusBadRequest, errors.New("not a bot post"))
		return
	}

	// Check ownership via conversation entity
	if !a.isConversationOwner(post, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("only the original poster can stop the stream"))
		return
	}

	a.streamingService.StopStreaming(post.Id)

	// In HA without sticky sessions the /stop request can land on a node
	// that does not hold the cancel function for this post; broadcast a
	// reliable cluster event so peer nodes cancel locally too. The local
	// stop above remains a no-op on those nodes, and the broadcast is a
	// no-op on the node that already canceled — both calls are idempotent.
	if a.streamStopNotifier != nil {
		if err := a.streamStopNotifier.PublishStreamStop(post.Id); err != nil {
			a.pluginAPI.Log.Error("Failed to publish stream stop cluster event", "post_id", post.Id, "error", err.Error())
		}
	}

	c.Status(http.StatusOK)
}

func (a *API) handleRegenerate(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if !a.isConversationOwner(post, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("only the original poster can regenerate"))
		return
	}

	err := a.conversationsService.HandleRegenerate(c.Request.Context(), userID, post, channel)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("unable to regenerate post: %w", err))
		return
	}

	c.Status(http.StatusOK)
}

func (a *API) handleToolCall(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	if !a.licenseChecker.IsBasicsLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
		return
	}

	isDM := mmapi.IsDMWith(post.UserId, channel)
	if !isDM && !a.config.EnableChannelMentionToolCalling() {
		c.AbortWithError(http.StatusForbidden, errors.New("channel tool calling is disabled"))
		return
	}

	if !a.isConversationOwner(post, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("only the original requester can approve/reject tool calls"))
		return
	}

	var data struct {
		AcceptedToolIDs []string `json:"accepted_tool_ids" binding:"required"`

		// ToolAnswers carries the user's answers for accepted
		// user-interaction tool calls, keyed by tool_use block ID.
		ToolAnswers map[string]mmtools.UserInteractionAnswer `json:"tool_answers"`
	}

	if err := c.ShouldBindJSON(&data); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if err := a.conversationsService.HandleToolCall(c.Request.Context(), userID, post, channel, data.AcceptedToolIDs, data.ToolAnswers); err != nil {
		c.AbortWithError(toolApprovalHTTPStatus(err), err)
		return
	}

	c.Status(http.StatusOK)
}

// toolApprovalHTTPStatus maps errors from HandleToolCall/HandleToolResult to
// HTTP statuses. Stale-click and missing-conversation cases are client-side
// issues (400); requester-mismatch is a permission denial (403); everything
// else falls through to 500.
func toolApprovalHTTPStatus(err error) int {
	switch {
	case errors.Is(err, conversations.ErrStaleToolClick),
		errors.Is(err, conversations.ErrPostMissingConversationID),
		errors.Is(err, conversations.ErrInvalidToolAnswer):
		return http.StatusBadRequest
	case errors.Is(err, conversations.ErrNotRequester):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

func (a *API) handleToolResult(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	if !a.licenseChecker.IsBasicsLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
		return
	}

	isDM := mmapi.IsDMWith(post.UserId, channel)
	if !isDM && !a.config.EnableChannelMentionToolCalling() {
		c.AbortWithError(http.StatusForbidden, errors.New("channel tool calling is disabled"))
		return
	}

	if !a.isConversationOwner(post, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("only the original requester can approve/reject tool results"))
		return
	}

	var data struct {
		AcceptedToolIDs []string `json:"accepted_tool_ids" binding:"required"`
	}

	if err := c.ShouldBindJSON(&data); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if err := a.conversationsService.HandleToolResult(c.Request.Context(), userID, post, channel, data.AcceptedToolIDs); err != nil {
		c.AbortWithError(toolApprovalHTTPStatus(err), err)
		return
	}

	c.Status(http.StatusOK)
}

// isConversationOwner checks whether the given user is the owner of the
// conversation associated with the post (via the conversation_id prop).
//
// Falls back to the llm_requester_user_id prop for legacy custom_llmbot posts
// that were not produced via the conversation entity flow — currently only
// meeting summarization. Remove the fallback once meeting flows migrate.
func (a *API) isConversationOwner(post *model.Post, userID string) bool {
	convID, ok := post.GetProp(streaming.ConversationIDProp).(string)
	if !ok || convID == "" {
		requester, _ := post.GetProp(streaming.LLMRequesterUserIDProp).(string)
		return requester != "" && requester == userID
	}

	// Try the full conversation service first, then fall back to the store interface.
	if a.convService != nil {
		conv, err := a.convService.GetConversation(convID)
		if err != nil {
			return false
		}
		return conv.UserID == userID
	}
	if a.conversationStore != nil {
		conv, err := a.conversationStore.GetConversation(convID)
		if err != nil {
			return false
		}
		return conv.UserID == userID
	}
	return false
}

func (a *API) handlePostbackSummary(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	result, err := a.meetingsService.HandlePostbackSummary(userID, post)
	if err != nil {
		if err.Error() == "post missing reference to transcription post ID" {
			c.AbortWithError(http.StatusBadRequest, err)
		} else {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("unable to post back summary: %w", err))
		}
		return
	}

	c.Render(http.StatusOK, render.JSON{Data: result})
}

// handleLoopInAgent runs the target reply through the channel mention path
// without persisting a synthetic @mention post.
func (a *API) handleLoopInAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if err := a.conversationsService.HandleLoopInAgent(telemetry.DetachContext(c.Request.Context()), userID, bot, post, channel); err != nil {
		c.AbortWithError(loopInAgentHTTPStatus(err), err)
		return
	}

	c.Status(http.StatusOK)
}

func loopInAgentHTTPStatus(err error) int {
	switch {
	case errors.Is(err, conversations.ErrLoopInNotPostOwner),
		errors.Is(err, conversations.ErrLoopInWrongAgent):
		return http.StatusForbidden
	case errors.Is(err, conversations.ErrLoopInNotThreadReply),
		errors.Is(err, conversations.ErrLoopInUnsupportedChannel),
		errors.Is(err, conversations.ErrLoopInAlreadyMentioned),
		errors.Is(err, conversations.ErrLoopInNoAgentContext):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// makeAnalysisPost creates a post for thread analysis results
func (a *API) makeAnalysisPost(locale string, postIDToAnalyze string, analysisType string, conversationID string) *model.Post {
	post := &model.Post{}
	post.AddProp(conversations.ThreadIDProp, postIDToAnalyze)
	post.AddProp(conversations.AnalysisTypeProp, analysisType)
	if conversationID != "" {
		post.AddProp(streaming.ConversationIDProp, conversationID)
	}

	return post
}
