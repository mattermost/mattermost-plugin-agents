// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversation"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/format"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/mmtools"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost-plugin-ai/subtitles"
	"github.com/mattermost/mattermost-plugin-ai/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const ThreadIDProp = "referenced_thread"
const AnalysisTypeProp = "prompt_type"

// AIThread represents a user's conversation with an AI
type AIThread struct {
	ID         string `json:"id"`
	Message    string `json:"message"`
	Title      string `json:"title"`
	ChannelID  string `json:"channel_id"`
	ReplyCount int    `json:"reply_count"`
	UpdateAt   int64  `json:"update_at"`
}

// ConfigProvider provides configuration values for conversation behavior
type ConfigProvider interface {
	EnableChannelMentionToolCalling() bool
	AllowNativeWebSearchInChannels() bool
	MCP() mcp.Config
}

type Conversations struct {
	prompts           *llm.Prompts
	mmClient          mmapi.Client
	streamingService  streaming.Service
	contextBuilder    *llmcontext.Builder
	bots              *bots.MMBots
	db                *mmapi.DBClient
	licenseChecker    *enterprise.LicenseChecker
	i18n              *i18n.Bundle
	meetingsService   MeetingsService
	configProvider    ConfigProvider
	toolPolicyChecker streaming.ToolPolicyChecker
	convService       *conversation.Service
}

// MeetingsService defines the interface for meetings functionality needed by conversations
type MeetingsService interface {
	GetCaptionsFileIDFromProps(post *model.Post) (fileID string, err error)
	SummarizeTranscription(bot *bots.Bot, transcription *subtitles.Subtitles, context *llm.Context) (*llm.TextStreamResult, error)
}

func New(
	prompts *llm.Prompts,
	mmClient mmapi.Client,
	streamingService streaming.Service,
	contextBuilder *llmcontext.Builder,
	botsService *bots.MMBots,
	db *mmapi.DBClient,
	licenseChecker *enterprise.LicenseChecker,
	i18nBundle *i18n.Bundle,
	meetingsService MeetingsService,
	configProvider ConfigProvider,
) *Conversations {
	return &Conversations{
		prompts:          prompts,
		mmClient:         mmClient,
		streamingService: streamingService,
		contextBuilder:   contextBuilder,
		bots:             botsService,
		db:               db,
		licenseChecker:   licenseChecker,
		i18n:             i18nBundle,
		meetingsService:  meetingsService,
		configProvider:   configProvider,
	}
}

// SetMeetingsService sets the meetings service (used to break circular dependency during initialization)
func (c *Conversations) SetMeetingsService(meetingsService MeetingsService) {
	c.meetingsService = meetingsService
}

// SetToolPolicyChecker sets the per-tool policy checker used for auto-approval
// and DM auto-run decisions.
func (c *Conversations) SetToolPolicyChecker(checker streaming.ToolPolicyChecker) {
	c.toolPolicyChecker = checker
}

// SetConversationService sets the conversation entity service.
func (c *Conversations) SetConversationService(svc *conversation.Service) {
	c.convService = svc
}

// DMRequestResult is the return value of ProcessDMRequest.
type DMRequestResult struct {
	ConversationID string
	IsNew          bool
	Stream         *llm.TextStreamResult
}

// ProcessDMRequest handles a DM message using the conversation entity model.
func (c *Conversations) ProcessDMRequest(
	botID string,
	lm llm.LanguageModel,
	postingUser *model.User,
	channel *model.Channel,
	post *model.Post,
	llmCtx *llm.Context,
) (*DMRequestResult, error) {
	if c.convService == nil {
		return nil, fmt.Errorf("conversation service not configured")
	}
	if llmCtx == nil {
		llmCtx = &llm.Context{}
	}
	if llmCtx.RequestingUser == nil {
		llmCtx.RequestingUser = postingUser
	}
	if llmCtx.Channel == nil {
		llmCtx.Channel = channel
	}

	systemPrompt := ""
	if c.prompts != nil {
		sp, err := c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, llmCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to format system prompt: %w", err)
		}
		systemPrompt = sp
	}

	var convID string
	var isNew bool
	postID := post.Id

	if post.RootId == "" {
		channelID := channel.Id
		result, err := c.convService.CreateConversation(conversation.CreateConversationParams{
			UserID:       postingUser.Id,
			BotID:        botID,
			ChannelID:    &channelID,
			RootPostID:   &postID,
			Operation:    "conversation",
			SystemPrompt: systemPrompt,
			UserMessage:  post.Message,
			UserPostID:   &postID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create conversation: %w", err)
		}
		convID = result.ConversationID
		isNew = true
	} else {
		result, err := c.convService.GetOrCreateConversation(conversation.GetOrCreateParams{
			UserID:       postingUser.Id,
			BotID:        botID,
			ChannelID:    channel.Id,
			RootPostID:   post.RootId,
			Operation:    "conversation",
			SystemPrompt: systemPrompt,
			UserMessage:  post.Message,
			UserPostID:   &postID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get or create conversation: %w", err)
		}
		convID = result.Conversation.ID
		isNew = result.IsNew
	}

	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	completionReq, err := c.convService.BuildCompletionRequest(conv, llmCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to build completion request: %w", err)
	}

	shouldExecute := func(tc llm.ToolCall) bool {
		if c.toolPolicyChecker == nil {
			return false
		}
		policy, enabled := c.toolPolicyChecker.GetToolPolicy(tc.ServerOrigin, tc.Name)
		return policy == mcp.ToolPolicyAutoRun && enabled
	}

	runner := toolrunner.New(lm)
	runResult, err := runner.Run(*completionReq, shouldExecute)
	if err != nil {
		return nil, fmt.Errorf("tool runner failed: %w", err)
	}
	if len(runResult.ToolTurns) > 0 {
		if writeErr := c.convService.WriteToolTurns(convID, runResult.ToolTurns, true); writeErr != nil {
			return nil, fmt.Errorf("failed to write tool turns: %w", writeErr)
		}
	}
	return &DMRequestResult{
		ConversationID: convID,
		IsNew:          isNew,
		Stream:         runResult.Stream,
	}, nil
}

func (c *Conversations) appendDMAutoRunOptions(isDM bool, llmContext *llm.Context, opts []llm.LanguageModelOption) []llm.LanguageModelOption {
	if !isDM || c.toolPolicyChecker == nil || llmContext == nil || llmContext.Tools == nil {
		return opts
	}
	allTools := llmContext.Tools.GetTools()
	var autoRunNames []string
	for _, t := range allTools {
		policy, enabled := c.toolPolicyChecker.GetToolPolicy(t.ServerOrigin, t.Name)
		if policy == mcp.ToolPolicyAutoRun && enabled {
			autoRunNames = append(autoRunNames, llm.ToolAutoRunKey(t.ServerOrigin, t.Name))
		}
	}
	if len(autoRunNames) > 0 {
		opts = append(opts, llm.WithAutoRunTools(autoRunNames))
	}
	return opts
}

// ProcessUserRequestWithContext is an internal helper that uses an existing context to process a message
func (c *Conversations) ProcessUserRequestWithContext(bot *bots.Bot, postingUser *model.User, channel *model.Channel, post *model.Post, context *llm.Context, allowToolsInChannel bool) (*llm.TextStreamResult, error) {
	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)
	toolsDisabled := !isDM && !allowToolsInChannel
	if context != nil {
		if toolsDisabled && context.Tools != nil {
			context.DisabledToolsInfo = context.Tools.GetToolsInfo()
		} else {
			context.DisabledToolsInfo = nil
		}
	}

	var posts []llm.Post
	if post.RootId == "" {
		prompt, err := c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, context)
		if err != nil {
			return nil, fmt.Errorf("failed to format prompt: %w", err)
		}
		posts = []llm.Post{{Role: llm.PostRoleSystem, Message: prompt}}
	} else {
		previousConversation, errThread := mmapi.GetThreadData(c.mmClient, post.Id)
		if errThread != nil {
			return nil, fmt.Errorf("failed to get previous conversation: %w", errThread)
		}
		previousConversation.CutoffBeforePostID(post.Id)
		var err error
		posts, err = c.existingConversationToLLMPosts(bot, previousConversation, context)
		if err != nil {
			return nil, fmt.Errorf("failed to convert existing conversation to LLM posts: %w", err)
		}
	}

	posts = append(posts, c.PostToAIPost(bot, post))
	completionRequest := llm.CompletionRequest{Posts: posts, Context: context, Operation: llm.OperationConversation}
	var opts []llm.LanguageModelOption
	if toolsDisabled {
		opts = append(opts, llm.WithToolsDisabled())
		if c.configProvider != nil && c.configProvider.AllowNativeWebSearchInChannels() && bot.HasNativeWebSearchEnabled() {
			opts = append(opts, llm.WithNativeWebSearchAllowed())
		}
	}
	opts = c.appendDMAutoRunOptions(isDM, context, opts)

	result, err := bot.LLM().ChatCompletion(completionRequest, opts...)
	if err != nil {
		return nil, err
	}

	var toolsStore *llm.ToolStore
	if context != nil && context.Tools != nil {
		toolsStore = context.Tools
	}
	result = llm.EnrichToolCallsWithServerOrigin(result, toolsStore)
	webSearchData := mmtools.ConsumeWebSearchContexts(context)
	c.mmClient.LogDebug("Checking for web search data in ProcessUserRequestWithContext", "has_data", len(webSearchData) > 0, "num_contexts", len(webSearchData))
	if len(webSearchData) > 0 {
		result = mmtools.DecorateStreamWithAnnotations(result, webSearchData, nil)
	}
	if !toolsDisabled && context != nil && context.Tools != nil && c.toolPolicyChecker != nil {
		result = wrapStreamWithMCPAutoApproval(result, context, c.toolPolicyChecker)
	}

	go func() {
		request := "Write a short title for the following request. Include only the title and nothing else, no quotations. Request:\n" + post.Message
		if err := c.GenerateTitle(bot, request, post.Id, context); err != nil {
			c.mmClient.LogError("Failed to generate title", "error", err.Error())
			return
		}
	}()

	return result, nil
}

// ProcessUserRequest processes a user request to a bot
func (c *Conversations) ProcessUserRequest(bot *bots.Bot, postingUser *model.User, channel *model.Channel, post *model.Post, allowToolsInChannel bool) (*llm.TextStreamResult, error) {
	webSearchParams := c.extractWebSearchContext(post)
	var contextOpts []llm.ContextOption
	contextOpts = append(contextOpts, c.contextBuilder.WithLLMContextTools(bot))
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

	var disabledOrigins map[string]bool
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		prefs, err := mcp.LoadUserPreferences(c.mmClient, postingUser.Id)
		if err != nil {
			c.mmClient.LogWarn("Failed to load user tool preferences, proceeding without filtering", "error", err.Error(), "userID", postingUser.Id)
		} else if len(prefs.DisabledServers) > 0 {
			disabledOrigins = make(map[string]bool, len(prefs.DisabledServers))
			for _, origin := range prefs.DisabledServers {
				disabledOrigins[origin] = true
			}
			if llmContext.Tools != nil {
				llmContext.Tools.RemoveToolsByServerOrigin(prefs.DisabledServers)
			}
		}
	}

	if llmContext.Tools != nil {
		authErrors := llmContext.Tools.GetAuthErrors()
		if len(disabledOrigins) > 0 {
			filtered := authErrors[:0]
			for _, ae := range authErrors {
				if !disabledOrigins[ae.ServerOrigin] {
					filtered = append(filtered, ae)
				}
			}
			authErrors = filtered
		}
		if len(authErrors) > 0 {
			rootID := post.RootId
			if rootID == "" {
				rootID = post.Id
			}
			c.sendOAuthNotifications(bot, postingUser.Id, channel.Id, rootID, authErrors)
		}
	}

	return c.ProcessUserRequestWithContext(bot, postingUser, channel, post, llmContext, allowToolsInChannel)
}

func (c *Conversations) GenerateTitle(bot *bots.Bot, request string, postID string, context *llm.Context) error {
	titleRequest := llm.CompletionRequest{
		Posts:            []llm.Post{{Role: llm.PostRoleUser, Message: request}},
		Context:          context,
		Operation:        llm.OperationTitleGeneration,
		OperationSubType: llm.SubTypeNoStream,
	}
	conversationTitle, err := bot.LLM().ChatCompletionNoStream(titleRequest, llm.WithMaxGeneratedTokens(25), llm.WithReasoningDisabled(), llm.WithToolsDisabled())
	if err != nil {
		return fmt.Errorf("failed to get title: %w", err)
	}
	conversationTitle = strings.Trim(conversationTitle, "\n \"'")
	if err := c.SaveTitle(postID, conversationTitle); err != nil {
		return fmt.Errorf("failed to save title: %w", err)
	}
	return nil
}

// existingConversationToLLMPosts converts existing conversation to LLM posts format
func (c *Conversations) existingConversationToLLMPosts(bot *bots.Bot, conv *mmapi.ThreadData, context *llm.Context) ([]llm.Post, error) {
	originalThreadID, ok := conv.Posts[0].GetProp(ThreadIDProp).(string)
	if ok && originalThreadID != "" && conv.Posts[0].UserId == bot.GetMMBot().UserId {
		threadPost, err := c.mmClient.GetPost(originalThreadID)
		if err != nil {
			return nil, err
		}
		threadChannel, err := c.mmClient.GetChannel(threadPost.ChannelId)
		if err != nil {
			return nil, err
		}
		if !c.mmClient.HasPermissionToChannel(context.RequestingUser.Id, threadChannel.Id, model.PermissionReadChannel) ||
			c.bots.CheckUsageRestrictions(context.RequestingUser.Id, bot, threadChannel) != nil {
			T := i18n.LocalizerFunc(c.i18n, context.RequestingUser.Locale)
			responsePost := &model.Post{
				ChannelId: context.Channel.Id,
				RootId:    originalThreadID,
				Message:   T("agents.no_longer_access_error", "Sorry, you no longer have access to the original thread."),
			}
			if err = c.BotCreateNonResponsePost(bot.GetMMBot().UserId, context.RequestingUser.Id, responsePost); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("user no longer has access to original thread")
		}
		analysisType, ok := conv.Posts[0].GetProp(AnalysisTypeProp).(string)
		if !ok {
			return nil, fmt.Errorf("missing analysis type")
		}
		posts, err := c.buildAnalysisFallbackPosts(originalThreadID, context, analysisType)
		if err != nil {
			return nil, err
		}
		posts = append(posts, c.ThreadToLLMPosts(bot, conv)...)
		return posts, nil
	}

	prompt, err := c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format prompt: %w", err)
	}
	posts := []llm.Post{{Role: llm.PostRoleSystem, Message: prompt}}
	posts = append(posts, c.ThreadToLLMPosts(bot, conv)...)
	return posts, nil
}

func (c *Conversations) buildAnalysisFallbackPosts(originalThreadID string, context *llm.Context, analysisType string) ([]llm.Post, error) {
	threadData, err := mmapi.GetThreadData(c.mmClient, originalThreadID)
	if err != nil {
		return nil, err
	}
	formattedThread := format.ThreadData(threadData)
	context.Parameters = map[string]any{"Thread": formattedThread}
	systemPromptName := prompts.PromptSummarizeThreadSystem
	userPromptName := prompts.PromptThreadUser
	switch analysisType {
	case "action_items":
		systemPromptName = prompts.PromptFindActionItemsSystem
		userPromptName = prompts.PromptFindActionItemsUser
	case "open_questions":
		systemPromptName = prompts.PromptFindOpenQuestionsSystem
		userPromptName = prompts.PromptFindOpenQuestionsUser
	}
	systemPrompt, err := c.prompts.Format(systemPromptName, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format system prompt: %w", err)
	}
	userPrompt, err := c.prompts.Format(userPromptName, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format user prompt: %w", err)
	}
	return []llm.Post{
		{Role: llm.PostRoleSystem, Message: systemPrompt},
		{Role: llm.PostRoleUser, Message: userPrompt},
	}, nil
}

// GetAIThreads gets AI conversation threads for a user
func (c *Conversations) GetAIThreads(userID string) ([]AIThread, error) {
	allBots := c.bots.GetAllBots()
	dmChannelIDs := []string{}
	for _, bot := range allBots {
		channelName := model.GetDMNameFromIds(userID, bot.GetMMBot().UserId)
		botDMChannel, err := c.mmClient.GetChannelByName("", channelName, false)
		if err != nil {
			if errors.Is(err, pluginapi.ErrNotFound) {
				continue
			}
			c.mmClient.LogError("unable to get DM channel for bot", "error", err, "bot_id", bot.GetMMBot().UserId)
			continue
		}
		if !c.mmClient.HasPermissionToChannel(userID, botDMChannel.Id, model.PermissionReadChannel) {
			c.mmClient.LogDebug("user doesn't have permission to read channel", "user_id", userID, "channel_id", botDMChannel.Id, "bot_id", bot.GetMMBot().UserId)
			continue
		}
		dmChannelIDs = append(dmChannelIDs, botDMChannel.Id)
	}
	return c.getAIThreads(dmChannelIDs)
}

const defaultMaxFileSize = int64(1024 * 1024 * 5) // 5MB

func (c *Conversations) BotCreateNonResponsePost(botid string, requesterUserID string, post *model.Post) error {
	streaming.ModifyPostForBot(botid, requesterUserID, post, "")
	post.AddProp(streaming.NoRegen, true)
	if err := c.mmClient.CreatePost(post); err != nil {
		return err
	}
	return nil
}

func isImageMimeType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

func (c *Conversations) PostToAIPost(bot *bots.Bot, post *model.Post) llm.Post {
	var filesForUpstream []llm.File
	message := format.PostBody(post)
	var extractedFileContents []string
	maxFileSize := defaultMaxFileSize
	if bot.GetConfig().MaxFileSize > 0 {
		maxFileSize = bot.GetConfig().MaxFileSize
	}
	for _, fileID := range post.FileIds {
		fileInfo, err := c.mmClient.GetFileInfo(fileID)
		if err != nil {
			c.mmClient.LogError("Error getting file info", "error", err)
			continue
		}
		content := ""
		if trimmedContent := strings.TrimSpace(fileInfo.Content); trimmedContent != "" {
			content = trimmedContent
		} else if strings.HasPrefix(fileInfo.MimeType, "text/") {
			file, err := c.mmClient.GetFile(fileID)
			if err != nil {
				c.mmClient.LogError("Error getting file", "error", err)
				continue
			}
			contentBytes, err := io.ReadAll(io.LimitReader(file, maxFileSize))
			if err != nil {
				c.mmClient.LogError("Error reading file content", "error", err)
				continue
			}
			content = string(contentBytes)
			if int64(len(contentBytes)) == maxFileSize {
				content += "\n... (content truncated due to size limit)"
			}
		}
		if content != "" {
			fileContent := fmt.Sprintf("File Name: %s\nContent: %s", fileInfo.Name, content)
			extractedFileContents = append(extractedFileContents, fileContent)
		}
		if bot.GetConfig().EnableVision && isImageMimeType(fileInfo.MimeType) {
			file, err := c.mmClient.GetFile(fileID)
			if err != nil {
				c.mmClient.LogError("Error getting file", "error", err)
				continue
			}
			filesForUpstream = append(filesForUpstream, llm.File{Reader: file, MimeType: fileInfo.MimeType, Size: fileInfo.Size})
		}
	}
	if len(extractedFileContents) > 0 {
		message += "\nAttached File Contents:\n" + strings.Join(extractedFileContents, "\n\n")
	}
	role := llm.PostRoleUser
	if c.bots.IsAnyBot(post.UserId) {
		role = llm.PostRoleBot
	}
	pendingToolsProp := post.GetProp(streaming.ToolCallProp)
	tools := []llm.ToolCall{}
	pendingTools, ok := pendingToolsProp.(string)
	if ok {
		var toolCalls []llm.ToolCall
		if err := json.Unmarshal([]byte(pendingTools), &toolCalls); err != nil {
			c.mmClient.LogError("Error unmarshalling tool calls", "error", err)
		} else {
			for _, toolCall := range toolCalls {
				if toolCall.Status == llm.ToolCallStatusRejected {
					continue
				}
				tools = append(tools, toolCall)
			}
		}
	}
	reasoning := ""
	if reasoningProp := post.GetProp(streaming.ReasoningSummaryProp); reasoningProp != nil {
		if reasoningStr, ok := reasoningProp.(string); ok {
			reasoning = reasoningStr
		}
	}
	reasoningSignature := ""
	if signatureProp := post.GetProp(streaming.ReasoningSignatureProp); signatureProp != nil {
		if signatureStr, ok := signatureProp.(string); ok {
			reasoningSignature = signatureStr
		}
	}
	return llm.Post{
		Role: role, Message: message, Files: filesForUpstream,
		ToolUse: tools, Reasoning: reasoning, ReasoningSignature: reasoningSignature,
	}
}

func (c *Conversations) ThreadToLLMPosts(bot *bots.Bot, threadData *mmapi.ThreadData) []llm.Post {
	result := make([]llm.Post, 0, len(threadData.Posts))
	for _, post := range threadData.Posts {
		aiPost := c.PostToAIPost(bot, post)
		if aiPost.Role == llm.PostRoleUser {
			if user, exists := threadData.UsersByID[post.UserId]; exists {
				aiPost.Message = "@" + user.Username + ": " + aiPost.Message
			}
		}
		result = append(result, aiPost)
	}
	return result
}

func (c *Conversations) sendOAuthNotifications(bot *bots.Bot, userID, channelID, rootID string, authErrors []llm.ToolAuthError) {
	if len(authErrors) == 0 {
		return
	}
	var message strings.Builder
	message.WriteString("**Authentication Required**\n\n")
	message.WriteString("The following MCP servers require authentication:\n\n")
	for _, authErr := range authErrors {
		message.WriteString(fmt.Sprintf("• **%s**: [Click here to authenticate](%s)\n", authErr.ServerName, authErr.AuthURL))
	}
	message.WriteString("\nPlease authenticate with the required servers and try again.")
	post := &model.Post{RootId: rootID, UserId: bot.GetMMBot().UserId, ChannelId: channelID, Message: message.String()}
	c.mmClient.SendEphemeralPost(userID, post)
}
