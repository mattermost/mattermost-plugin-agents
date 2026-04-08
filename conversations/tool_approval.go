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
		return errors.New("post missing conversation_id")
	}

	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		return fmt.Errorf("failed to get conversation: %w", err)
	}

	if conv.UserID != userID {
		return errors.New("only the original requester can approve/reject tool calls")
	}

	// Find the latest assistant turn with pending tool calls.
	turns, err := c.convService.GetTurns(convID)
	if err != nil {
		return fmt.Errorf("failed to get turns: %w", err)
	}

	pendingTurn, pendingBlocks, err := findPendingToolTurn(turns)
	if err != nil {
		return err
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

	// Write tool results as a tool_result turn.
	shared := isDM
	maxSeq, err := c.convService.GetMaxSequence(convID)
	if err != nil {
		return fmt.Errorf("failed to get max sequence: %w", err)
	}

	resultBlocks := make([]conversation.ContentBlock, 0, len(toolResults))
	for _, tr := range toolResults {
		status := conversation.StatusSuccess
		if tr.IsError {
			status = conversation.StatusError
		}
		resultBlocks = append(resultBlocks, conversation.ContentBlock{
			Type:      conversation.BlockTypeToolResult,
			ToolUseID: tr.ToolCallID,
			Content:   tr.Result,
			Status:    status,
			Shared:    conversation.BoolPtr(shared),
		})
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
		Sequence:       maxSeq + 1,
		CreatedAt:      model.GetMillis(),
	}
	if err := c.convService.CreateTurn(resultTurn); err != nil {
		return fmt.Errorf("failed to create tool result turn: %w", err)
	}

	// Continue when there is any executed tool result (success or error).
	// Error results are included because the agent may recover on the next turn.
	// Only skip continuation when all tools were rejected.
	hasExecuted := slices.ContainsFunc(pendingBlocks, func(b conversation.ContentBlock) bool {
		return b.Type == conversation.BlockTypeToolUse &&
			(b.Status == conversation.StatusSuccess || b.Status == conversation.StatusError)
	})
	if !hasExecuted {
		return nil
	}

	// Continue the LLM loop: rebuild from conversation and stream follow-up.
	return c.streamToolFollowUp(bot, user, channel, post, conv, isDM)
}

// HandleToolResult handles user approval of tool results in channel contexts.
// In DMs, tool results are auto-shared. In channels, this handles the second
// approval step after tool execution.
func (c *Conversations) HandleToolResult(userID string, post *model.Post, channel *model.Channel, acceptedToolIDs []string) error {
	// In the conversation entity model, tool results are already written
	// to the conversation turns during HandleToolCall. The result approval
	// step updates the shared flag on tool blocks.
	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		return fmt.Errorf("unable to get bot")
	}

	convID, ok := post.GetProp(streaming.ConversationIDProp).(string)
	if !ok || convID == "" {
		return errors.New("post missing conversation_id")
	}

	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		return fmt.Errorf("failed to get conversation: %w", err)
	}

	if conv.UserID != userID {
		return errors.New("only the original requester can approve/reject tool results")
	}

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return fmt.Errorf("unable to get user: %w", err)
	}

	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)

	// Build a set of accepted tool IDs for quick lookup.
	acceptedSet := make(map[string]bool, len(acceptedToolIDs))
	for _, id := range acceptedToolIDs {
		acceptedSet[id] = true
	}

	// Update shared flags on tool_use and tool_result blocks for accepted tools.
	turns, err := c.convService.GetTurns(conv.ID)
	if err != nil {
		return fmt.Errorf("failed to get turns: %w", err)
	}

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

	// Continue the LLM loop with the conversation context.
	return c.streamToolFollowUp(bot, user, channel, post, conv, isDM)
}

// streamToolFollowUp rebuilds the completion request from the conversation entity
// and streams a follow-up LLM response after tool execution.
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

	completionReq, err := c.convService.BuildCompletionRequest(conv, llmContext)
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
	runResult, err := runner.Run(*completionReq, func(tc llm.ToolCall) bool {
		if c.toolPolicyChecker == nil {
			return false
		}
		// LLM-returned tool calls may lack ServerOrigin; resolve from tool store.
		origin := tc.ServerOrigin
		if origin == "" && llmContext.Tools != nil {
			origin = llmContext.Tools.GetServerOrigin(tc.Name)
		}
		policy, enabled := c.toolPolicyChecker.GetToolPolicy(origin, tc.Name)
		return mcp.IsToolPolicyAutoRun(policy) && enabled
	}, opts...)
	if err != nil {
		return fmt.Errorf("tool runner failed on tool follow-up: %w", err)
	}

	if len(runResult.ToolTurns) > 0 {
		shared := isDM
		if writeErr := c.convService.WriteToolTurns(conv.ID, runResult.ToolTurns, shared); writeErr != nil {
			c.mmClient.LogError("Failed to write tool turns on follow-up", "error", writeErr)
		}
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

// findPendingToolTurn finds the latest assistant turn with pending tool_use blocks.
func findPendingToolTurn(turns []store.Turn) (*store.Turn, []conversation.ContentBlock, error) {
	// Walk backward to find the last assistant turn with pending tool calls.
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role != "assistant" {
			continue
		}

		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turns[i].Content, &blocks); err != nil {
			continue
		}

		hasPending := slices.ContainsFunc(blocks, func(b conversation.ContentBlock) bool {
			return b.Type == conversation.BlockTypeToolUse && b.Status == conversation.StatusPending
		})
		if hasPending {
			return &turns[i], blocks, nil
		}
	}

	return nil, nil, errors.New("no pending tool calls found in conversation")
}

// responseRootIDFromPost returns the root ID for responding in a thread.
func responseRootIDFromPost(post *model.Post) string {
	if post.RootId != "" {
		return post.RootId
	}
	return post.Id
}
