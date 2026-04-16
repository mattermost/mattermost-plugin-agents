// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/i18n"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost-plugin-agents/subtitles"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
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

// DMConversationResult is the return value of CreateOrGetDMConversation.
type DMConversationResult struct {
	ConversationID string
	IsNew          bool
}

// CreateOrGetDMConversation creates or retrieves a conversation for a DM.
// This is separated from ProcessDMRequest so the conversation_id can be
// set on the response post before it is created.
func (c *Conversations) CreateOrGetDMConversation(
	botID string,
	postingUser *model.User,
	channel *model.Channel,
	post *model.Post,
	llmCtx *llm.Context,
) (*DMConversationResult, error) {
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
		return &DMConversationResult{ConversationID: result.ConversationID, IsNew: true}, nil
	}

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
	return &DMConversationResult{ConversationID: result.Conversation.ID, IsNew: result.IsNew}, nil
}

// DMStreamResult is the return value of ProcessDMRequest.
type DMStreamResult struct {
	Stream *llm.TextStreamResult
}

// ProcessDMRequest builds a completion request from the conversation and
// runs the tool loop, returning the final stream. The conversation must
// already exist (created via CreateOrGetDMConversation).
func (c *Conversations) ProcessDMRequest(
	convID string,
	lm llm.LanguageModel,
	llmCtx *llm.Context,
) (*DMStreamResult, error) {
	if c.convService == nil {
		return nil, fmt.Errorf("conversation service not configured")
	}
	if llmCtx == nil {
		llmCtx = &llm.Context{}
	}

	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	completionReq, err := c.convService.BuildCompletionRequest(conv, llmCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to build completion request: %w", err)
	}

	runner := toolrunner.New(lm)
	runResult, err := runner.Run(*completionReq, c.shouldAutoExecuteTool(llmCtx), func(turns []toolrunner.ToolTurn) {
		if writeErr := c.convService.WriteToolTurns(convID, turns, true); writeErr != nil {
			c.mmClient.LogError("Failed to write tool turns", "error", writeErr, "conversation_id", convID)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("tool runner failed: %w", err)
	}

	stream := runResult.Stream
	if webSearchData := mmtools.ConsumeWebSearchContexts(llmCtx); len(webSearchData) > 0 {
		stream = mmtools.DecorateStreamWithAnnotations(stream, webSearchData, nil)
	}

	return &DMStreamResult{Stream: stream}, nil
}

// shouldAutoExecuteTool returns a callback that decides whether a tool call
// should be auto-executed based on the tool policy.
func (c *Conversations) shouldAutoExecuteTool(llmCtx *llm.Context) func(llm.ToolCall) bool {
	return func(tc llm.ToolCall) bool {
		if c.toolPolicyChecker == nil {
			return false
		}
		origin := tc.ServerOrigin
		if origin == "" && llmCtx.Tools != nil {
			origin = llmCtx.Tools.GetServerOrigin(tc.Name)
		}
		policy, enabled := c.toolPolicyChecker.GetToolPolicy(origin, tc.Name)
		return mcp.IsToolPolicyAutoRun(policy) && enabled
	}
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
