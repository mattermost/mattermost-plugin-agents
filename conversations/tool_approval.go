// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

// ErrStaleToolClick is returned when a tool-approval click cannot be resolved
// because the pending tool state no longer matches the request. Typical
// causes: another browser tab already approved/rejected, the post is not an
// approval post, or the approval has expired. The HTTP layer maps this to
// 400 Bad Request rather than 500 Internal Server Error.
var ErrStaleToolClick = errors.New("stale or duplicate tool-approval click")

// ErrPostMissingConversationID is returned when a tool-approval request
// arrives for a post that has no conversation_id prop. The HTTP layer maps
// this to 400 Bad Request.
var ErrPostMissingConversationID = errors.New("post missing conversation_id")

// ErrNotRequester is returned when a user other than the original conversation
// requester attempts to approve or reject tool calls/results. The HTTP layer
// maps this to 403 Forbidden.
var ErrNotRequester = errors.New("only the original requester can approve/reject tool calls")

// ErrRemoteMCPNotLicensed is returned when a tool decision would execute, or
// share the output of, a tool served by a remote/external MCP server on a
// server whose license does not include MCP support. Built-in tools (empty
// ServerOrigin) and embedded Mattermost MCP tools (mcp.EmbeddedClientKey) are
// basic tool integrations and never require a license. The HTTP layer maps
// this to 403 Forbidden.
//
// This gate is the decision-time backstop for pending remote tool calls
// persisted before a license change; the primary enforcement is at supply
// time, where llmcontext.Builder drops remote MCP tools from the LLM context
// entirely on unlicensed servers.
var ErrRemoteMCPNotLicensed = errors.New("tools from remote MCP servers require a license with MCP support")

// isRemoteMCPLicensed reports whether the server license covers remote MCP
// servers. A nil license checker fails closed.
func (c *Conversations) isRemoteMCPLicensed() bool {
	return c.licenseChecker != nil && c.licenseChecker.IsBasicsLicensed()
}

// HandleToolCall handles user approval/rejection of pending tool calls via conversation entities.
// It looks up pending tool_use blocks in the conversation turns, executes approved tools,
// writes results back as turns, and streams a follow-up LLM response.
func (c *Conversations) HandleToolCall(userID string, post *model.Post, channel *model.Channel, acceptedToolIDs []string) error {
	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		return fmt.Errorf("unable to get bot")
	}

	convID, ok := post.GetProp(streaming.ConversationIDProp).(string)
	if !ok || convID == "" {
		return ErrPostMissingConversationID
	}

	c.mmClient.LogDebug("HandleToolCall",
		"post_id", post.Id,
		"conv_id", convID,
		"accepted_count", len(acceptedToolIDs),
	)

	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		return fmt.Errorf("failed to get conversation: %w", err)
	}

	if conv.UserID != userID {
		return ErrNotRequester
	}

	turns, err := c.convService.GetTurns(convID)
	if err != nil {
		return fmt.Errorf("failed to get turns: %w", err)
	}

	pendingTurn, pendingBlocks, err := findPendingToolTurn(turns, post.Id)
	if err != nil {
		return err
	}

	// Accepting tools from remote/external MCP servers is part of the
	// licensed "MCP Support" feature. Built-in and embedded Mattermost MCP
	// tools are basic tool integrations with no license requirement.
	// Rejections are always allowed — they execute nothing.
	for _, b := range pendingBlocks {
		if b.Type != conversation.BlockTypeToolUse || !mcp.IsRemoteServerOrigin(b.ServerOrigin) {
			continue
		}
		if b.Status != conversation.StatusPending && b.Status != conversation.StatusAccepted {
			continue
		}
		if slices.Contains(acceptedToolIDs, b.ID) && !c.isRemoteMCPLicensed() {
			return ErrRemoteMCPNotLicensed
		}
	}

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return fmt.Errorf("unable to get user: %w", err)
	}

	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)

	// Build LLM context with tools for execution.
	contextOpts := []llm.ContextOption{
		c.contextBuilder.WithLLMContextDefaultTools(bot),
	}
	llmContext := c.contextBuilder.BuildLLMContextUserRequest(bot, user, channel, contextOpts...)

	// Apply user-disabled-provider filtering for DM/group channels.
	if isDM || channel.Type == model.ChannelTypeGroup {
		prefs, prefsErr := mcp.LoadUserPreferences(c.mmClient, user.Id)
		if prefsErr != nil {
			c.mmClient.LogWarn("Failed to load user tool preferences for tool approval", "error", prefsErr.Error())
		} else if len(prefs.DisabledServers) > 0 && llmContext.Tools != nil {
			llmContext.Tools.RemoveToolsByServerOrigin(prefs.DisabledServers)
		}
	}

	// Execute approved tools and build results.
	var toolResults []toolrunner.ToolResult
	for i := range pendingBlocks {
		block := &pendingBlocks[i]
		if block.Type != conversation.BlockTypeToolUse {
			continue
		}
		if block.Status != conversation.StatusPending && block.Status != conversation.StatusAccepted {
			// Preserve previously resolved statuses (e.g., auto-approved).
			continue
		}

		if slices.Contains(acceptedToolIDs, block.ID) {
			result, resolveErr := llmContext.Tools.ResolveTool(block.Name, func(args any) error {
				return json.Unmarshal(block.Input, args)
			}, llmContext)
			if resolveErr != nil {
				block.Status = conversation.StatusError
				toolResults = append(toolResults, toolrunner.ToolResult{
					ToolCallID: block.ID,
					Name:       block.Name,
					Result:     resolveErr.Error(),
					IsError:    true,
				})
			} else {
				block.Status = conversation.StatusSuccess
				toolResults = append(toolResults, toolrunner.ToolResult{
					ToolCallID: block.ID,
					Name:       block.Name,
					Result:     result,
					IsError:    false,
				})
			}
		} else {
			block.Status = conversation.StatusRejected
			toolResults = append(toolResults, toolrunner.ToolResult{
				ToolCallID: block.ID,
				Name:       block.Name,
				Result:     "Tool call rejected by user",
				IsError:    true,
			})
		}
	}

	// Update the assistant turn with resolved statuses.
	updatedContent, err := json.Marshal(pendingBlocks)
	if err != nil {
		return fmt.Errorf("failed to marshal updated blocks: %w", err)
	}
	if updateErr := c.convService.UpdateTurnContent(pendingTurn.ID, updatedContent); updateErr != nil {
		return fmt.Errorf("failed to update turn with resolved statuses: %w", updateErr)
	}

	// Write tool results as a tool_result turn. DecidedAt is set when the
	// result is terminal — no share/keep-private decision remains. That
	// applies to every DM result (auto-shared) and every rejected tool
	// (nothing was produced to share). Channel results from accepted tools
	// stay undecided until the requester clicks Share or Keep Private.
	shared := isDM
	toolUseStatusByID := make(map[string]string, len(pendingBlocks))
	for _, b := range pendingBlocks {
		if b.Type == conversation.BlockTypeToolUse {
			toolUseStatusByID[b.ID] = b.Status
		}
	}
	now := model.GetMillis()
	resultBlocks := make([]conversation.ContentBlock, 0, len(toolResults))
	for _, tr := range toolResults {
		status := conversation.StatusSuccess
		if tr.IsError {
			status = conversation.StatusError
		}
		rb := conversation.ContentBlock{
			Type:      conversation.BlockTypeToolResult,
			ToolUseID: tr.ToolCallID,
			Content:   tr.Result,
			Status:    status,
			Shared:    conversation.BoolPtr(shared),
		}
		if isDM || toolUseStatusByID[tr.ToolCallID] == conversation.StatusRejected {
			rb.DecidedAt = conversation.Int64Ptr(now)
		}
		resultBlocks = append(resultBlocks, rb)
	}
	resultContent, err := json.Marshal(resultBlocks)
	if err != nil {
		return fmt.Errorf("failed to marshal tool result blocks: %w", err)
	}
	resultTurn := &store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "tool_result",
		Content:        resultContent,
		CreatedAt:      model.GetMillis(),
	}
	if err := c.convService.CreateTurnAutoSequence(resultTurn); err != nil {
		return fmt.Errorf("failed to create tool result turn: %w", err)
	}

	hasExecuted := slices.ContainsFunc(pendingBlocks, func(b conversation.ContentBlock) bool {
		return b.Type == conversation.BlockTypeToolUse &&
			(b.Status == conversation.StatusSuccess || b.Status == conversation.StatusError)
	})
	if !hasExecuted {
		return nil
	}

	// In channels the follow-up is a channel-visible post that may paraphrase tool
	// output, so it must not stream until the requester approves sharing in
	// HandleToolResult.
	if !isDM {
		return nil
	}

	return c.streamToolFollowUp(bot, user, channel, post, conv, isDM)
}

// HandleToolResult handles user approval of the second-stage tool-result sharing.
// It flips shared flags for accepted results and, in channels, streams the LLM
// follow-up with unshared content redacted so private tool output cannot leak
// into the channel-visible reply.
func (c *Conversations) HandleToolResult(userID string, post *model.Post, channel *model.Channel, acceptedToolIDs []string) error {
	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		return fmt.Errorf("unable to get bot")
	}

	convID, ok := post.GetProp(streaming.ConversationIDProp).(string)
	if !ok || convID == "" {
		return ErrPostMissingConversationID
	}

	c.mmClient.LogDebug("HandleToolResult",
		"post_id", post.Id,
		"conv_id", convID,
		"accepted_count", len(acceptedToolIDs),
	)

	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		return fmt.Errorf("failed to get conversation: %w", err)
	}

	if conv.UserID != userID {
		return ErrNotRequester
	}

	acceptedSet := make(map[string]bool, len(acceptedToolIDs))
	for _, id := range acceptedToolIDs {
		acceptedSet[id] = true
	}

	turns, err := c.convService.GetTurns(conv.ID)
	if err != nil {
		return fmt.Errorf("failed to get turns: %w", err)
	}

	// Classify the clicked post's tool_use blocks. DecidedAt applies to the
	// matching tool_result blocks; we also need to know whether any tool
	// actually executed to decide whether a follow-up stream is warranted.
	clickedPostToolUseIDs := make(map[string]struct{})
	clickedPostHasExecutedTool := false
	acceptedRemoteMCPTool := false
	for _, turn := range turns {
		if turn.Role != "assistant" || turn.PostID == nil || *turn.PostID != post.Id {
			continue
		}
		var blocks []conversation.ContentBlock
		if unmarshalErr := json.Unmarshal(turn.Content, &blocks); unmarshalErr != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != conversation.BlockTypeToolUse || b.ID == "" {
				continue
			}
			clickedPostToolUseIDs[b.ID] = struct{}{}
			if b.Status == conversation.StatusSuccess ||
				b.Status == conversation.StatusError ||
				b.Status == conversation.StatusAutoApproved {
				clickedPostHasExecutedTool = true
			}
			if acceptedSet[b.ID] && mcp.IsRemoteServerOrigin(b.ServerOrigin) {
				acceptedRemoteMCPTool = true
			}
		}
	}

	// Idempotency: if every tool_result for this post already has
	// DecidedAt, the decision was already recorded and no further work is
	// needed. Returning early makes repeat clicks safe and cheap — the
	// webapp no longer needs to defend against this but the server still
	// should.
	alreadyDecided := true
	sawMatchingResult := false
	for _, turn := range turns {
		var blocks []conversation.ContentBlock
		if unmarshalErr := json.Unmarshal(turn.Content, &blocks); unmarshalErr != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != conversation.BlockTypeToolResult {
				continue
			}
			if _, ok := clickedPostToolUseIDs[b.ToolUseID]; !ok {
				continue
			}
			sawMatchingResult = true
			if b.DecidedAt == nil {
				alreadyDecided = false
			}
		}
	}
	if sawMatchingResult && alreadyDecided {
		return nil
	}

	// Sharing output from remote/external MCP tools is part of the licensed
	// "MCP Support" feature (see HandleToolCall). Keep-private decisions are
	// always allowed — they disclose nothing.
	if acceptedRemoteMCPTool && !c.isRemoteMCPLicensed() {
		return ErrRemoteMCPNotLicensed
	}

	now := model.GetMillis()
	for _, turn := range turns {
		var blocks []conversation.ContentBlock
		if unmarshalErr := json.Unmarshal(turn.Content, &blocks); unmarshalErr != nil {
			continue
		}

		modified := false
		for i := range blocks {
			switch blocks[i].Type {
			case conversation.BlockTypeToolUse:
				if acceptedSet[blocks[i].ID] {
					blocks[i].Shared = conversation.BoolPtr(true)
					modified = true
				}
			case conversation.BlockTypeToolResult:
				if acceptedSet[blocks[i].ToolUseID] {
					blocks[i].Shared = conversation.BoolPtr(true)
					modified = true
				}
				if _, ok := clickedPostToolUseIDs[blocks[i].ToolUseID]; ok && blocks[i].DecidedAt == nil {
					blocks[i].DecidedAt = conversation.Int64Ptr(now)
					modified = true
				}
			}
		}

		if modified {
			updatedContent, marshalErr := json.Marshal(blocks)
			if marshalErr != nil {
				return fmt.Errorf("failed to marshal updated blocks: %w", marshalErr)
			}
			if updateErr := c.convService.UpdateTurnContent(turn.ID, updatedContent); updateErr != nil {
				return fmt.Errorf("failed to update turn shared flags: %w", updateErr)
			}
		}
	}

	// DMs stream the follow-up from HandleToolCall.
	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)
	if isDM {
		return nil
	}

	// Only stream a follow-up when there is something to follow up on:
	// at least one executed tool_result exists on this post. Rejected-only
	// posts produce no output worth streaming.
	if !clickedPostHasExecutedTool {
		return nil
	}

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return fmt.Errorf("unable to get user: %w", err)
	}

	return c.streamToolFollowUp(bot, user, channel, post, conv, false)
}

// streamToolFollowUp rebuilds the completion request from the conversation and
// streams a follow-up LLM response after tool execution. The request redacts
// tool_result content the user kept private before reaching the LLM — for DMs
// this is a no-op (all tool_results are shared=true), for channels it is the
// privacy guarantee that keeps unshared tool output from leaking into a
// channel-visible reply.
func (c *Conversations) streamToolFollowUp(
	bot *bots.Bot,
	user *model.User,
	channel *model.Channel,
	post *model.Post,
	conv *store.Conversation,
	isDM bool,
) error {
	contextOpts := []llm.ContextOption{
		c.contextBuilder.WithLLMContextDefaultTools(bot),
	}
	llmContext := c.contextBuilder.BuildLLMContextUserRequest(bot, user, channel, contextOpts...)

	// Apply user-disabled-provider filtering for DM/group channels.
	if isDM || channel.Type == model.ChannelTypeGroup {
		prefs, prefsErr := mcp.LoadUserPreferences(c.mmClient, user.Id)
		if prefsErr != nil {
			c.mmClient.LogWarn("Failed to load user tool preferences for tool follow-up", "error", prefsErr.Error())
		} else if len(prefs.DisabledServers) > 0 && llmContext.Tools != nil {
			llmContext.Tools.RemoveToolsByServerOrigin(prefs.DisabledServers)
		}
	}

	toolsDisabled := !isDM
	if !isDM && c.configProvider != nil && c.configProvider.EnableChannelMentionToolCalling() {
		toolsDisabled = false
	}
	if toolsDisabled && llmContext.Tools != nil {
		llmContext.DisabledToolsInfo = llmContext.Tools.GetToolsInfo()
	}

	if !isDM && !toolsDisabled && conv.RootPostID != nil {
		if rootPost, rootErr := c.mmClient.GetPost(*conv.RootPostID); rootErr == nil {
			if rootUser, userErr := c.mmClient.GetUser(rootPost.UserId); userErr == nil {
				configEnabled := c.configProvider != nil && c.configProvider.EnableChannelMentionToolCalling()
				if configEnabled && isBotActivateAI(rootPost, rootUser) && c.toolPolicyChecker != nil {
					c.applyBotChannelAutoEverywhereToolFilter(llmContext)
				}
			}
		}
	}

	// Channel thread posts aren't stored as turns, so rebuild with thread context.
	completionReq, err := c.buildToolFollowUpRequest(conv, llmContext, isDM)
	if err != nil {
		return fmt.Errorf("failed to build completion request for tool follow-up: %w", err)
	}
	completionReq.Operation = llm.OperationConversationToolFollowup
	completionReq.OperationSubType = llm.SubTypeToolCall

	var opts []llm.LanguageModelOption
	if toolsDisabled {
		opts = append(opts, llm.WithToolsDisabled())
		if c.configProvider != nil && c.configProvider.AllowNativeWebSearchInChannels() && bot.HasNativeWebSearchEnabled() {
			opts = append(opts, llm.WithNativeWebSearchAllowed())
		}
	}

	runner := toolrunner.New(bot.LLM())
	runResult, err := runner.Run(*completionReq, c.shouldAutoExecuteTool(llmContext, isDM), func(turns []toolrunner.ToolTurn) {
		shared := isDM || c.allToolsAutoRunEverywhere(turns, llmContext)
		if writeErr := c.convService.WriteToolTurns(conv.ID, turns, shared); writeErr != nil {
			c.mmClient.LogError("Failed to write tool turns on follow-up", "error", writeErr)
		}
	}, opts...)

	if err != nil {
		return fmt.Errorf("tool runner failed on tool follow-up: %w", err)
	}

	responsePost := &model.Post{
		ChannelId: channel.Id,
		RootId:    responseRootIDFromPost(post),
	}
	responsePost.AddProp(streaming.ConversationIDProp, conv.ID)
	if err := c.streamingService.StreamToNewPost(context.Background(), bot.GetMMBot().UserId, user.Id, runResult.Stream, responsePost, post.Id); err != nil {
		return fmt.Errorf("failed to stream tool follow-up: %w", err)
	}

	return nil
}

// buildToolFollowUpRequest rebuilds the completion request for a tool follow-up.
// Channel conversations re-fetch the live thread so non-turn thread posts stay in
// context (matching the initial mention); DMs persist every post as a turn.
func (c *Conversations) buildToolFollowUpRequest(conv *store.Conversation, llmContext *llm.Context, isDM bool) (*llm.CompletionRequest, error) {
	if !isDM && conv.RootPostID != nil {
		// Best-effort: if the live thread can't be fetched (deleted root,
		// permissions, API blip), degrade to turns-only context rather than
		// failing the resume. BuildChannelMentionRequest does the same on
		// empty thread data.
		threadData, err := mmapi.GetThreadData(c.mmClient, *conv.RootPostID)
		if err != nil {
			c.mmClient.LogWarn("Failed to get thread data for tool follow-up, falling back to turns-only context", "error", err)
			return c.convService.BuildCompletionRequest(conv, llmContext)
		}
		return c.convService.BuildChannelMentionRequest(conv, llmContext, threadData)
	}
	return c.convService.BuildCompletionRequest(conv, llmContext)
}

// findPendingToolTurn returns the assistant turn linked to clickedPostID along
// with its blocks, provided the turn contains pending tool_use blocks.
func findPendingToolTurn(turns []store.Turn, clickedPostID string) (*store.Turn, []conversation.ContentBlock, error) {
	for i := range turns {
		if turns[i].Role != "assistant" {
			continue
		}
		if turns[i].PostID == nil || *turns[i].PostID != clickedPostID {
			continue
		}

		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turns[i].Content, &blocks); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal turn %s content: %w", turns[i].ID, err)
		}

		hasPending := slices.ContainsFunc(blocks, func(b conversation.ContentBlock) bool {
			return b.Type == conversation.BlockTypeToolUse && b.Status == conversation.StatusPending
		})
		if !hasPending {
			return nil, nil, fmt.Errorf("clicked post has no pending tool calls: %w", ErrStaleToolClick)
		}
		return &turns[i], blocks, nil
	}

	return nil, nil, fmt.Errorf("no pending tool calls found for clicked post: %w", ErrStaleToolClick)
}

// responseRootIDFromPost returns the root ID for responding in a thread.
func responseRootIDFromPost(post *model.Post) string {
	if post.RootId != "" {
		return post.RootId
	}
	return post.Id
}
