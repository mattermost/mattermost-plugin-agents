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
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/format"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost-plugin-ai/subtitles"
	"github.com/mattermost/mattermost-plugin-ai/threads"
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

type Conversations struct {
	prompts          *llm.Prompts
	mmClient         mmapi.Client
	streamingService streaming.Service
	contextBuilder   *llmcontext.Builder
	bots             *bots.MMBots
	db               *mmapi.DBClient
	licenseChecker   *enterprise.LicenseChecker
	i18n             *i18n.Bundle
	meetingsService  MeetingsService
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
	}
}

// SetMeetingsService sets the meetings service (used to break circular dependency during initialization)
func (c *Conversations) SetMeetingsService(meetingsService MeetingsService) {
	c.meetingsService = meetingsService
}

// ProcessUserRequestWithContext is an internal helper that uses an existing context to process a message
func (c *Conversations) ProcessUserRequestWithContext(bot *bots.Bot, postingUser *model.User, channel *model.Channel, post *model.Post, context *llm.Context) (*llm.TextStreamResult, error) {
	var posts []llm.Post
	if post.RootId == "" {
		// A new conversation
		prompt, err := c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, context)
		if err != nil {
			return nil, fmt.Errorf("failed to format prompt: %w", err)
		}
		posts = []llm.Post{
			{
				Role:    llm.PostRoleSystem,
				Message: prompt,
			},
		}
	} else {
		// Continuing an existing conversation
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

	completionRequest := llm.CompletionRequest{
		Posts:   posts,
		Context: context,
	}
	result, err := bot.LLM().ChatCompletion(completionRequest)
	if err != nil {
		return nil, err
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
func (c *Conversations) ProcessUserRequest(bot *bots.Bot, postingUser *model.User, channel *model.Channel, post *model.Post) (*llm.TextStreamResult, error) {
	// Create a context with default tools
	context := c.contextBuilder.BuildLLMContextUserRequest(
		bot,
		postingUser,
		channel,
		c.contextBuilder.WithLLMContextDefaultTools(bot, mmapi.IsDMWith(bot.GetMMBot().UserId, channel)),
	)

	// Check for auth errors in the tool store
	if context.Tools != nil {
		authErrors := context.Tools.GetAuthErrors()
		if len(authErrors) > 0 {
			c.sendOAuthNotifications(bot, postingUser.Id, channel.Id, post.Id, authErrors)
		}
	}

	return c.ProcessUserRequestWithContext(bot, postingUser, channel, post, context)
}

func (c *Conversations) GenerateTitle(bot *bots.Bot, request string, postID string, context *llm.Context) error {
	titleRequest := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: request}},
		Context: context,
	}

	conversationTitle, err := bot.LLM().ChatCompletionNoStream(titleRequest, llm.WithMaxGeneratedTokens(25))
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
func (c *Conversations) existingConversationToLLMPosts(bot *bots.Bot, conversation *mmapi.ThreadData, context *llm.Context) ([]llm.Post, error) {
	// Handle thread summarization requests
	originalThreadID, ok := conversation.Posts[0].GetProp(ThreadIDProp).(string)
	if ok && originalThreadID != "" && conversation.Posts[0].UserId == bot.GetMMBot().UserId {
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

		analysisType, ok := conversation.Posts[0].GetProp(AnalysisTypeProp).(string)
		if !ok {
			return nil, fmt.Errorf("missing analysis type")
		}

		posts, err := threads.New(bot.LLM(), c.prompts, c.mmClient).FollowUpAnalyze(originalThreadID, context, analysisType)
		if err != nil {
			return nil, err
		}
		posts = append(posts, c.ThreadToLLMPosts(bot, conversation)...)
		return posts, nil
	}

	// Plain DM conversation
	prompt, err := c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format prompt: %w", err)
	}
	posts := []llm.Post{
		{
			Role:    llm.PostRoleSystem,
			Message: prompt,
		},
	}
	posts = append(posts, c.ThreadToLLMPosts(bot, conversation)...)

	return posts, nil
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
				// Channel doesn't exist yet, so we'll skip it
				continue
			}
			c.mmClient.LogError("unable to get DM channel for bot", "error", err, "bot_id", bot.GetMMBot().UserId)
			continue
		}

		// Extra permissions checks are not totally necessary since a user should always have permission to read their own DMs
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

		// Check for files that have been interpreted already by the server or are text files.
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
			filesForUpstream = append(filesForUpstream, llm.File{
				Reader:   file,
				MimeType: fileInfo.MimeType,
				Size:     fileInfo.Size,
			})
		}
	}

	// Add structured file contents to the message
	if len(extractedFileContents) > 0 {
		message += "\nAttached File Contents:\n" + strings.Join(extractedFileContents, "\n\n")
	}

	role := llm.PostRoleUser
	if c.bots.IsAnyBot(post.UserId) {
		role = llm.PostRoleBot
	}

	// Check for tools
	pendingToolsProp := post.GetProp(streaming.ToolCallProp)
	tools := []llm.ToolCall{}
	pendingTools, ok := pendingToolsProp.(string)
	if ok {
		var toolCalls []llm.ToolCall
		if err := json.Unmarshal([]byte(pendingTools), &toolCalls); err != nil {
			c.mmClient.LogError("Error unmarshalling tool calls", "error", err)
		} else {
			tools = toolCalls
		}
	}

	// Check for reasoning/thinking content
	reasoning := ""
	if reasoningProp := post.GetProp(streaming.ReasoningSummaryProp); reasoningProp != nil {
		if reasoningStr, ok := reasoningProp.(string); ok {
			reasoning = reasoningStr
		}
	}

	// Check for reasoning signature (opaque verification field)
	reasoningSignature := ""
	if signatureProp := post.GetProp(streaming.ReasoningSignatureProp); signatureProp != nil {
		if signatureStr, ok := signatureProp.(string); ok {
			reasoningSignature = signatureStr
		}
	}

	return llm.Post{
		Role:               role,
		Message:            message,
		Files:              filesForUpstream,
		ToolUse:            tools,
		Reasoning:          reasoning,
		ReasoningSignature: reasoningSignature,
	}
}

func (c *Conversations) ThreadToLLMPosts(bot *bots.Bot, threadData *mmapi.ThreadData) []llm.Post {
	result := make([]llm.Post, 0, len(threadData.Posts))

	for _, post := range threadData.Posts {
		aiPost := c.PostToAIPost(bot, post)

		// Add username prefix for user messages in multi-user threads
		if aiPost.Role == llm.PostRoleUser {
			if user, exists := threadData.UsersByID[post.UserId]; exists {
				aiPost.Message = "@" + user.Username + ": " + aiPost.Message
			}
		}

		result = append(result, aiPost)
	}

	return result
}

// sendOAuthNotifications sends an ephemeral post to notify the user about MCP servers that require authentication
func (c *Conversations) sendOAuthNotifications(bot *bots.Bot, userID, channelID, rootID string, authErrors []llm.ToolAuthError) {
	if len(authErrors) == 0 {
		return
	}

	// Build the message
	var message strings.Builder
	message.WriteString("**Authentication Required**\n\n")
	message.WriteString("The following MCP servers require authentication:\n\n")

	for _, authErr := range authErrors {
		message.WriteString(fmt.Sprintf("• **%s**: [Click here to authenticate](%s)\n", authErr.ServerName, authErr.AuthURL))
	}

	message.WriteString("\nPlease authenticate with the required servers and try again.")

	// Create the ephemeral post
	post := &model.Post{
		RootId:    rootID,
		UserId:    bot.GetMMBot().UserId,
		ChannelId: channelID,
		Message:   message.String(),
	}

	// Send the ephemeral post
	c.mmClient.SendEphemeralPost(userID, post)
}
