// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	searchStartChannelIDParam   = "SearchChannelID"
	searchStartChannelNameParam = "SearchChannelName"
)

// searchMCPTools are the embedded Mattermost tools a search conversation
// needs from its first turn, even when the agent loads MCP tools dynamically.
var searchMCPTools = []llm.EnabledMCPTool{
	{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "search_posts"},
	{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "read_post"},
	{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "read_channel"},
}

// ErrSearchToolsUnavailable means the agent cannot call search_posts, so a
// search request would silently turn into a plain chat.
var ErrSearchToolsUnavailable = errors.New("the search_posts tool from the embedded Mattermost MCP server is not available to this agent")

// FormatSearchSystemPrompt renders the system prompt for a search
// conversation. startChannel, when set, is the channel the agent should
// search first; the agent may still search beyond it.
func FormatSearchSystemPrompt(p *llm.Prompts, llmContext *llm.Context, startChannel *model.Channel) (string, error) {
	if llmContext.Parameters == nil {
		llmContext.Parameters = make(map[string]any)
	}
	if startChannel != nil {
		llmContext.Parameters[searchStartChannelIDParam] = startChannel.Id
		llmContext.Parameters[searchStartChannelNameParam] = startChannel.DisplayName
	}
	return p.Format(prompts.PromptSearchSystem, llmContext)
}

// SearchRequest is a search started from the search bar or /ask-channel.
// Callers must have checked that the user can access Team and Channel.
type SearchRequest struct {
	Query string
	// Team, when set, is used to build citation permalinks.
	Team *model.Team
	// Channel, when set, is where the agent starts searching. It is a hint
	// about what the user means, not a boundary: the agent can search more
	// broadly when the answer isn't there.
	Channel *model.Channel
}

// HandleSearch posts the query to the user's DM with the agent and answers it
// in the background with a tool-calling conversation that searches
// Mattermost. ctx must outlive the request and carry the user's session ID.
func (c *Conversations) HandleSearch(ctx context.Context, bot *bots.Bot, user *model.User, req SearchRequest) (*model.Post, error) {
	questionPost := &model.Post{
		UserId:  user.Id,
		Message: req.Query,
	}
	if err := c.mmClient.DM(user.Id, bot.GetMMBot().UserId, questionPost); err != nil {
		return nil, fmt.Errorf("failed to create question post: %w", err)
	}

	channel, err := c.mmClient.GetChannel(questionPost.ChannelId)
	if err != nil {
		return nil, fmt.Errorf("failed to get DM channel: %w", err)
	}

	go func() {
		if searchErr := c.runSearch(ctx, bot, user, channel, questionPost, req); searchErr != nil {
			c.mmClient.LogError("Failed to answer search request", "error", searchErr, "post_id", questionPost.Id)
		}
	}()

	return questionPost, nil
}

func (c *Conversations) runSearch(ctx context.Context, bot *bots.Bot, user *model.User, channel *model.Channel, questionPost *model.Post, req SearchRequest) (err error) {
	responsePost := &model.Post{
		ChannelId: channel.Id,
		RootId:    questionPost.Id,
	}
	if placeholderErr := c.createResponsePlaceholder(bot.GetMMBot().UserId, user.Id, responsePost, questionPost.Id); placeholderErr != nil {
		return fmt.Errorf("unable to create response placeholder: %w", placeholderErr)
	}
	progress := newResponseProgressReporter(ctx, c.mmClient, responsePost)
	progress.Advance(responseProgressCheckingMCP)
	defer func() {
		switch {
		case errors.Is(err, ErrSearchToolsUnavailable):
			c.setPlaceholderMessage(responsePost, user.Locale, "agents.search_tools_unavailable",
				"I can't search Mattermost because the Mattermost search tool isn't available to this agent. Ask your system admin to enable the embedded Mattermost MCP server and the search_posts tool for this agent.")
		case err != nil:
			c.failResponsePlaceholder(responsePost, user.Locale)
		}
	}()

	llmContext := c.buildConversationContextWithTools(
		ctx,
		bot, user, channel,
		"Failed to load user tool preferences for search",
		c.contextBuilder.WithLLMContextInteractive(),
		c.contextBuilder.WithLLMContextResponseFiles(),
		c.searchToolsContextOption(bot),
	)
	progress.Advance(responseProgressLoadingConversation)

	if _, ok := llmContext.Tools.LookupTool("search_posts", mcp.EmbeddedClientKey); !ok {
		return ErrSearchToolsUnavailable
	}

	if req.Team != nil {
		llmContext.Team = req.Team
	}
	systemPrompt, err := FormatSearchSystemPrompt(c.prompts, llmContext, req.Channel)
	if err != nil {
		return fmt.Errorf("failed to format search system prompt: %w", err)
	}

	channelID := channel.Id
	questionPostID := questionPost.Id
	created, err := c.convService.CreateConversation(conversation.CreateConversationParams{
		UserID:       user.Id,
		SessionID:    auth.SessionIDFromContext(ctx),
		BotID:        bot.GetMMBot().UserId,
		ChannelID:    &channelID,
		RootPostID:   &questionPostID,
		Operation:    llm.OperationSearch,
		SystemPrompt: systemPrompt,
		UserMessage:  questionPost.Message,
		UserPostID:   &questionPostID,
	})
	if err != nil {
		return fmt.Errorf("failed to create search conversation: %w", err)
	}

	return c.streamDMConversation(ctx, bot, channel, user, questionPost, responsePost, progress, llmContext, &DMConversationResult{
		ConversationID: created.ConversationID,
		IsNew:          true,
		UserTurnID:     created.UserTurnID,
	})
}

// searchToolsContextOption preloads the search tools. Agents that expose MCP
// tools directly already have them, so preloading there would duplicate them.
func (c *Conversations) searchToolsContextOption(bot *bots.Bot) llm.ContextOption {
	if !bot.GetConfig().MCPDynamicToolLoading {
		return func(*llm.Context) {}
	}
	return c.contextBuilder.WithLLMContextPreloadedMCPTools(searchMCPTools)
}

// conversationToolOptions returns the context options every turn of conv
// needs, so follow-ups, regenerations, and tool approvals keep the tools the
// conversation started with.
func (c *Conversations) conversationToolOptions(bot *bots.Bot, conv *store.Conversation) []llm.ContextOption {
	if conv == nil || conv.Operation != llm.OperationSearch {
		return nil
	}
	return []llm.ContextOption{c.searchToolsContextOption(bot)}
}

// threadConversationToolOptions is conversationToolOptions for a thread reply
// whose conversation has not been loaded yet.
func (c *Conversations) threadConversationToolOptions(bot *bots.Bot, user *model.User, post *model.Post) []llm.ContextOption {
	if post.RootId == "" || c.convService == nil {
		return nil
	}
	conv, err := c.convService.GetConversationByThread(post.RootId, bot.GetMMBot().UserId, user.Id)
	if err != nil {
		c.mmClient.LogWarn("Failed to look up thread conversation", "error", err, "root_id", post.RootId)
		return nil
	}
	return c.conversationToolOptions(bot, conv)
}
