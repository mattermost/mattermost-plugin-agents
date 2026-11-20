// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
)

// OperationTools supplies the tools a conversation operation adds to every
// one of its turns: the first one, replies, regenerations, and tool
// decisions. They are added after the agent's own catalog, so they are
// present even when the agent's tools are turned off, and the turn's policy
// (approval, unattended filtering) still applies to them.
type OperationTools interface {
	OperationTools(ctx context.Context, bot *bots.Bot, llmCtx *llm.Context) []llm.Tool
}

// RegisterOperation makes conversations recorded with operation get tools
// on every turn and regenerate like ordinary conversations. tools may be nil.
// Register operations before the plugin starts serving requests.
func (c *Conversations) RegisterOperation(operation string, tools OperationTools) {
	if operation == "" {
		panic("conversations: RegisterOperation requires an operation")
	}
	c.operationsMu.Lock()
	defer c.operationsMu.Unlock()
	if c.operations == nil {
		c.operations = make(map[string]OperationTools)
	}
	c.operations[operation] = tools
}

func (c *Conversations) registeredOperation(operation string) (OperationTools, bool) {
	c.operationsMu.RLock()
	defer c.operationsMu.RUnlock()
	tools, ok := c.operations[operation]
	return tools, ok
}

// AgentThreadRequest starts a new thread with an agent on behalf of User.
type AgentThreadRequest struct {
	Bot *bots.Bot
	// User is who the turn runs for: access to the agent, the channel, and
	// tools is checked as them, exactly as for a message they posted.
	User *model.User
	// Channel is where the thread starts. Nil means User's DM with the agent.
	Channel *model.Channel
	// Operation is recorded on the conversation; see RegisterOperation.
	Operation string
	// Message is the thread's root post.
	Message string
	// PostAsUser posts Message as User, who is then present to answer
	// approvals and questions, like a message they sent. It is only allowed in
	// User's DM with the agent. Otherwise the agent posts Message and the
	// first turn runs unattended.
	PostAsUser bool
	// Instruction is the first user turn the agent answers when the agent
	// posts the root. It is never posted. Ignored with PostAsUser, where the
	// root post is the first user turn.
	Instruction string
	// RootProps are added to the root post.
	RootProps map[string]any
	// SystemPrompt renders the conversation's system prompt from the first
	// turn's context. Nil uses the direct message system prompt.
	SystemPrompt func(llmCtx *llm.Context) (string, error)
}

// AgentThread is a started agent thread. Its first turn keeps running in the
// background after StartAgentThread returns.
type AgentThread struct {
	RootPost *model.Post
}

// ErrAgentThreadPostAsUserOutsideDM is returned when PostAsUser is requested
// for a channel other than the user's DM with the agent.
var ErrAgentThreadPostAsUserOutsideDM = errors.New("an agent thread can only be posted as the user in their direct message with the agent")

// StartAgentThread posts the root of a new thread and answers it with the
// agent in the background, under the same turn policy as a message the user
// posted there. Later replies in the thread continue the conversation like
// any other. ctx must outlive the request and carry the user's session ID.
func (c *Conversations) StartAgentThread(ctx context.Context, req AgentThreadRequest) (thread *AgentThread, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "start agent thread",
		trace.WithAttributes(telemetry.LLMOperation.String(req.Operation)),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to start agent thread")
		}
		span.End()
	}()

	if req.Bot == nil || req.User == nil {
		return nil, errors.New("agent thread is missing its agent or user")
	}
	if req.Operation == "" || req.Message == "" {
		return nil, errors.New("agent thread is missing its operation or message")
	}
	if c.convService == nil {
		return nil, errors.New("conversation service not configured")
	}
	botUserID := req.Bot.GetMMBot().UserId
	span.SetAttributes(telemetry.AgentID.String(botUserID), telemetry.UserID.String(req.User.Id))

	channel := req.Channel
	if channel == nil {
		// Getting the DM creates it, so a user who may not use the agent is
		// refused first.
		if restrictionErr := c.bots.CheckUsageRestrictionsForUser(ctx, req.Bot, req.User.Id); restrictionErr != nil {
			return nil, restrictionErr
		}
		dm, dmErr := c.mmClient.GetDirectChannel(req.User.Id, botUserID)
		if dmErr != nil {
			return nil, fmt.Errorf("failed to get DM channel: %w", dmErr)
		}
		channel = dm
	}
	if req.PostAsUser && !mmapi.IsDMWith(botUserID, channel) {
		return nil, ErrAgentThreadPostAsUserOutsideDM
	}

	root := &model.Post{ChannelId: channel.Id, Message: req.Message}
	for key, value := range req.RootProps {
		root.AddProp(key, value)
	}
	turnReq := turnRequest{bot: req.Bot, user: req.User, channel: channel, operation: req.Operation}
	if req.PostAsUser {
		root.UserId = req.User.Id
		turnReq.trigger = root
	} else {
		root.UserId = botUserID
	}
	policy, err := c.resolveTurnPolicy(ctx, turnReq)
	if err != nil {
		return nil, err
	}

	if req.PostAsUser {
		err = c.mmClient.DM(req.User.Id, botUserID, root)
	} else {
		err = c.mmClient.CreatePost(root)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create agent thread root post: %w", err)
	}
	span.SetAttributes(telemetry.ThreadRootPostID.String(root.Id))

	go func() {
		if runErr := c.runAgentThread(ctx, policy, root, req); runErr != nil {
			c.mmClient.LogError("Failed to answer agent thread", "error", runErr, "post_id", root.Id, "operation", req.Operation)
		}
	}()

	return &AgentThread{RootPost: root}, nil
}

func (c *Conversations) runAgentThread(ctx context.Context, policy *turnPolicy, root *model.Post, req AgentThreadRequest) (err error) {
	bot, user, channel := policy.bot, policy.user, policy.channel
	responsePost := &model.Post{ChannelId: channel.Id, RootId: root.Id}
	if placeholderErr := c.createResponsePlaceholder(bot.GetMMBot().UserId, responsePost, root.Id); placeholderErr != nil {
		return fmt.Errorf("unable to create response placeholder: %w", placeholderErr)
	}
	progress := newResponseProgressReporter(ctx, c.mmClient, responsePost)
	defer progress.Flush()
	progress.Advance(responseProgressCheckingMCP)
	defer func() {
		if err != nil {
			c.failResponsePlaceholder(responsePost, c.responseLocale(user, channel))
		}
	}()

	llmContext, err := policy.buildContext(ctx, "Failed to load user tool preferences for agent thread")
	if err != nil {
		return err
	}
	progress.Advance(responseProgressLoadingConversation)

	var systemPrompt string
	if req.SystemPrompt != nil {
		systemPrompt, err = req.SystemPrompt(llmContext)
	} else {
		systemPrompt, err = c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, llmContext)
	}
	if err != nil {
		return fmt.Errorf("failed to format agent thread system prompt: %w", err)
	}

	userMessage := req.Instruction
	var userPostID *string
	if req.PostAsUser {
		userMessage = root.Message
		userPostID = &root.Id
	}
	channelID := channel.Id
	rootID := root.Id
	created, err := c.convService.CreateConversation(conversation.CreateConversationParams{
		UserID:       user.Id,
		SessionID:    auth.SessionIDFromContext(ctx),
		BotID:        bot.GetMMBot().UserId,
		ChannelID:    &channelID,
		RootPostID:   &rootID,
		Operation:    req.Operation,
		SystemPrompt: systemPrompt,
		UserMessage:  userMessage,
		UserPostID:   userPostID,
	})
	if err != nil {
		return fmt.Errorf("failed to create agent thread conversation: %w", err)
	}

	return c.streamDMConversation(ctx, policy, root, responsePost, progress, llmContext, &DMConversationResult{
		ConversationID: created.ConversationID,
		IsNew:          true,
		UserTurnID:     created.UserTurnID,
	})
}
