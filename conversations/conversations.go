// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversation"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
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
	toolPolicyChecker mcp.ToolPolicyChecker
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
func (c *Conversations) SetToolPolicyChecker(checker mcp.ToolPolicyChecker) {
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

func (c *Conversations) BotCreateNonResponsePost(botid string, requesterUserID string, post *model.Post) error {
	streaming.ModifyPostForBot(botid, requesterUserID, post, "")
	post.AddProp(streaming.NoRegen, true)
	if err := c.mmClient.CreatePost(post); err != nil {
		return err
	}
	return nil
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
