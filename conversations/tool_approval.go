// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/trace"
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

// ErrInvalidToolAnswer is returned when an accepted user-interaction tool call
// (e.g. AskUserQuestion) arrives without a valid answer. The pending state is
// left untouched so the user can answer again. The HTTP layer maps this to
// 400 Bad Request.
var ErrInvalidToolAnswer = errors.New("invalid answer for user interaction tool call")

// HandleToolCall handles user approval/rejection of pending tool calls via conversation entities.
// It looks up pending tool_use blocks in the conversation turns, executes approved tools,
// writes results back as turns, and streams a follow-up LLM response.
//
// toolAnswers carries the user's answers for accepted user-interaction tool
// calls (e.g. AskUserQuestion), keyed by tool_use block ID. Those blocks are
// not executed; the validated answer becomes the tool result.
func (c *Conversations) HandleToolCall(ctx context.Context, userID string, post *model.Post, channel *model.Channel, acceptedToolIDs []string, toolAnswers map[string]mmtools.UserInteractionAnswer) error {
	// Resume: chain into the originating run's trace if we can find it. If
	// the post or its assistant turn is missing, fall back to a fresh trace.
	ctx = c.rehydrateRunTrace(ctx, post)

	ctx, span := telemetry.Tracer().Start(ctx, "handle tool call",
		trace.WithNewRoot(),
		trace.WithAttributes(telemetry.PostID.String(post.Id)),
	)
	defer span.End()

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

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return fmt.Errorf("unable to get user: %w", err)
	}

	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)

	// Build the execution context bound to this conversation. A tool-approval
	// click is by definition an interactive user action. Delegation
	// conversations additionally exclude ask_agent from the rebuilt store.
	contextOpts := []llm.ContextOption{c.contextBuilder.WithLLMContextInteractive()}
	contextOpts = append(contextOpts, c.delegationConversationContextOptions(conv)...)
	llmContext := c.buildConversationContextWithTools(
		ctx,
		bot, user, channel,
		"Failed to load user tool preferences for tool approval",
		contextOpts...,
	)

	conversation.RestoreLoadedMCPToolsFromTurns(llmContext.Tools, turns)

	// Validate answers for accepted interaction blocks before mutating any
	// state, so a malformed or missing answer leaves the question pending and
	// answerable instead of burning it as an error result.
	interactionResults, err := resolveInteractionAnswers(pendingBlocks, acceptedToolIDs, toolAnswers)
	if err != nil {
		return err
	}

	// Delegation calls can run (and wait on the user) for a very long time,
	// so they must not execute inside this HTTP request. Persist the accepted
	// decisions first — a duplicate click then fails the pending lookup as a
	// stale click instead of double-executing — and run the batch in a
	// detached goroutine. All other batches keep the synchronous path.
	if batchWillExecuteDelegationCall(pendingBlocks, acceptedToolIDs, c.shouldAutoExecuteTool(llmContext, isDM)) {
		claimedContent, err := c.persistAsyncToolDecisions(pendingTurn, pendingBlocks, acceptedToolIDs, c.shouldAutoExecuteTool(llmContext, isDM))
		if err != nil {
			return err
		}
		detachedCtx := telemetry.DetachContext(ctx)
		go func() {
			if execErr := c.executeResolvedToolBatch(detachedCtx, bot, user, channel, post, conv, convID, pendingTurn, pendingBlocks, acceptedToolIDs, interactionResults, llmContext, isDM, true); execErr != nil {
				c.handleAsyncToolBatchFailure(conv, pendingTurn.ID, claimedContent, post, execErr)
			}
		}()
		return nil
	}

	return c.executeResolvedToolBatch(ctx, bot, user, channel, post, conv, convID, pendingTurn, pendingBlocks, acceptedToolIDs, interactionResults, llmContext, isDM, false)
}

// isDelegationToolUseBlock reports whether a persisted tool_use block is the
// embedded ask_agent delegation call.
func isDelegationToolUseBlock(block conversation.ContentBlock) bool {
	if llm.NormalizeMCPServerOrigin(block.ServerOrigin) != mcp.EmbeddedClientKey {
		return false
	}
	if block.MCPBareName != "" {
		return block.MCPBareName == DelegationAskAgentToolName
	}
	return llm.BareMCPToolName(block.Name) == DelegationAskAgentToolName
}

// batchWillExecuteDelegationCall reports whether resolving this batch will
// execute an embedded ask_agent call (directly accepted, or auto-resumed via
// WouldAutoExecute + policy).
func batchWillExecuteDelegationCall(blocks []conversation.ContentBlock, acceptedToolIDs []string, autoExec func(llm.ToolCall) bool) bool {
	for _, block := range blocks {
		if block.Type != conversation.BlockTypeToolUse || block.UserInteraction != "" {
			continue
		}
		if block.Status != conversation.StatusPending && block.Status != conversation.StatusAccepted {
			continue
		}
		if !isDelegationToolUseBlock(block) {
			continue
		}
		if slices.Contains(acceptedToolIDs, block.ID) {
			return true
		}
		if block.WouldAutoExecute && autoExec(llm.ToolCall{Name: block.Name, ServerOrigin: block.ServerOrigin}) {
			return true
		}
	}
	return false
}

// persistAsyncToolDecisions records every decision in an asynchronously
// executed batch: calls that will run become accepted and every other pending
// call becomes rejected. The entire batch is persisted with one atomic
// compare-and-set against the content this request originally read. Two
// concurrent approval clicks (on any node) can therefore never both execute.
// The in-memory snapshot stays pending so the winning request can execute it.
func (c *Conversations) persistAsyncToolDecisions(pendingTurn *store.Turn, blocks []conversation.ContentBlock, acceptedToolIDs []string, autoExec func(llm.ToolCall) bool) (json.RawMessage, error) {
	persisted := slices.Clone(blocks)
	changed := false
	for i := range persisted {
		block := &persisted[i]
		if block.Type != conversation.BlockTypeToolUse || block.Status != conversation.StatusPending {
			continue
		}
		willExecute := slices.Contains(acceptedToolIDs, block.ID) ||
			(block.UserInteraction == "" && block.WouldAutoExecute && autoExec(llm.ToolCall{Name: block.Name, ServerOrigin: block.ServerOrigin}))
		if willExecute {
			block.Status = conversation.StatusAccepted
		} else {
			block.Status = conversation.StatusRejected
		}
		changed = true
	}
	if !changed {
		return nil, nil
	}
	updatedContent, err := json.Marshal(persisted)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool decisions: %w", err)
	}
	claimed, err := c.convService.ClaimTurnContent(pendingTurn.ID, pendingTurn.Content, updatedContent)
	if err != nil {
		return nil, fmt.Errorf("failed to persist tool decisions: %w", err)
	}
	if !claimed {
		return nil, fmt.Errorf("another request already resolved these tool calls: %w", ErrStaleToolClick)
	}
	return updatedContent, nil
}

// handleAsyncToolBatchFailure terminalizes a claimed batch when detached
// execution fails before it can durably persist its own resolved statuses.
// The compare-and-set cannot overwrite a batch that already advanced; either
// way the delegation waiter is woken to re-read the database.
func (c *Conversations) handleAsyncToolBatchFailure(conv *store.Conversation, turnID string, claimedContent json.RawMessage, post *model.Post, executionErr error) {
	conversationID := ""
	if conv != nil {
		conversationID = conv.ID
	}
	postID := ""
	if post != nil {
		postID = post.Id
	}

	if c.mmClient != nil {
		c.mmClient.LogError("Async tool batch execution failed", "error", executionErr, "conversation_id", conversationID, "post_id", postID)
	}

	failedContent, changed, err := failedClaimedToolContent(claimedContent)
	switch {
	case err != nil:
		if c.mmClient != nil {
			c.mmClient.LogError("Failed to build terminal state for async tool batch", "error", err, "conversation_id", conversationID, "post_id", postID)
		}
	case changed:
		finalized, finalizeErr := c.convService.ClaimTurnContent(turnID, claimedContent, failedContent)
		if finalizeErr != nil && c.mmClient != nil {
			c.mmClient.LogError("Failed to persist terminal state for async tool batch", "error", finalizeErr, "conversation_id", conversationID, "post_id", postID)
		} else if !finalized && c.mmClient != nil {
			c.mmClient.LogDebug("Async tool batch already advanced before failure finalization", "conversation_id", conversationID, "post_id", postID)
		}
	}

	c.notifyDelegationSubTurnCompleted(conv)
	c.publishConversationUpdated(conv, post)
}

func failedClaimedToolContent(claimedContent json.RawMessage) (json.RawMessage, bool, error) {
	var blocks []conversation.ContentBlock
	if err := json.Unmarshal(claimedContent, &blocks); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal claimed tool decisions: %w", err)
	}

	changed := false
	for i := range blocks {
		if blocks[i].Type == conversation.BlockTypeToolUse && blocks[i].Status == conversation.StatusAccepted {
			blocks[i].Status = conversation.StatusError
			changed = true
		}
	}
	if !changed {
		return claimedContent, false, nil
	}

	failedContent, err := json.Marshal(blocks)
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal failed tool decisions: %w", err)
	}
	return failedContent, true, nil
}

// executeResolvedToolBatch executes the user's approval decisions: it runs
// approved tools, persists resolved statuses and results, and streams the
// follow-up. It is the continuation of HandleToolCall, either inline or (for
// delegation batches) on a detached goroutine. isAsync marks the detached
// case: clients refetch on the HTTP response for synchronous batches, but an
// asynchronous batch that settles without a stream must nudge them itself.
func (c *Conversations) executeResolvedToolBatch(
	ctx context.Context,
	bot *bots.Bot,
	user *model.User,
	channel *model.Channel,
	post *model.Post,
	conv *store.Conversation,
	convID string,
	pendingTurn *store.Turn,
	pendingBlocks []conversation.ContentBlock,
	acceptedToolIDs []string,
	interactionResults map[string]string,
	llmContext *llm.Context,
	isDM bool,
	isAsync bool,
) error {
	// Execute approved tools and build results.
	autoExec := c.shouldAutoExecuteTool(llmContext, isDM)
	autoExecutedNow := make(map[string]bool)
	executedAny := false
	var toolResults []toolrunner.ToolResult
	for i := range pendingBlocks {
		block := &pendingBlocks[i]
		if block.Type != conversation.BlockTypeToolUse {
			continue
		}
		if block.Status != conversation.StatusPending {
			// Preserve previously resolved statuses (e.g., auto-approved).
			// An accepted status here means another request already claimed
			// the block for asynchronous execution — never execute it twice.
			continue
		}

		switch {
		case slices.Contains(acceptedToolIDs, block.ID) && block.UserInteraction != "":
			// Shared so the channel-visible follow-up may reference the answer.
			block.Status = conversation.StatusSuccess
			block.Shared = conversation.BoolPtr(true)
			executedAny = true
			toolResults = append(toolResults, toolrunner.ToolResult{
				ToolCallID: block.ID,
				Name:       block.Name,
				Result:     interactionResults[block.ID],
				IsError:    false,
			})
		case slices.Contains(acceptedToolIDs, block.ID):
			result, resolveErr := resolveApprovedToolUseBlock(ctx, llmContext, *block)
			executedAny = true
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
		case block.UserInteraction != "":
			// Skipped question: record the decline as the result and stream a
			// follow-up so the model can proceed without the answer, per the
			// tool contract. Shared because the decline is user-authored, not
			// private tool output.
			block.Status = conversation.StatusRejected
			block.Shared = conversation.BoolPtr(true)
			executedAny = true
			toolResults = append(toolResults, toolrunner.ToolResult{
				ToolCallID: block.ID,
				Name:       block.Name,
				Result:     "User skipped the question",
				IsError:    true,
			})
		case block.WouldAutoExecute && autoExec(llm.ToolCall{Name: block.Name, ServerOrigin: block.ServerOrigin}):
			// Runs on resume without a click. Requiring both the marker and a
			// fresh policy check means a mid-turn policy flip can neither
			// auto-run a tool the user was asked to approve (or rejected) nor
			// run one whose policy was since revoked. Shared and terminal, like
			// any auto-run round.
			result, resolveErr := resolveApprovedToolUseBlock(ctx, llmContext, *block)
			autoExecutedNow[block.ID] = true
			executedAny = true
			block.Shared = conversation.BoolPtr(true)
			if resolveErr != nil {
				block.Status = conversation.StatusError
				toolResults = append(toolResults, toolrunner.ToolResult{
					ToolCallID: block.ID,
					Name:       block.Name,
					Result:     resolveErr.Error(),
					IsError:    true,
				})
			} else {
				block.Status = conversation.StatusAutoApproved
				toolResults = append(toolResults, toolrunner.ToolResult{
					ToolCallID: block.ID,
					Name:       block.Name,
					Result:     result,
					IsError:    false,
				})
			}
		default:
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

	// Write tool results as a tool_result turn. DecidedAt is set when no
	// share/keep-private decision remains (see terminal below); other channel
	// results stay undecided until the requester clicks Share or Keep Private.
	toolUseStatusByID := make(map[string]string, len(pendingBlocks))
	interactionByID := make(map[string]bool, len(pendingBlocks))
	for _, b := range pendingBlocks {
		if b.Type == conversation.BlockTypeToolUse {
			toolUseStatusByID[b.ID] = b.Status
			interactionByID[b.ID] = b.UserInteraction != ""
		}
	}
	now := model.GetMillis()
	needsShareDecision := false
	resultBlocks := make([]conversation.ContentBlock, 0, len(toolResults))
	for _, tr := range toolResults {
		status := conversation.StatusSuccess
		if tr.IsError {
			status = conversation.StatusError
		}
		// Interaction results (answered or skipped) are user-authored, so they
		// are terminal and shared with no separate share/keep-private step.
		terminal := isDM || interactionByID[tr.ToolCallID] || autoExecutedNow[tr.ToolCallID]
		rb := conversation.ContentBlock{
			Type:      conversation.BlockTypeToolResult,
			ToolUseID: tr.ToolCallID,
			Content:   tr.Result,
			Status:    status,
			Shared:    conversation.BoolPtr(terminal),
		}
		if terminal || toolUseStatusByID[tr.ToolCallID] == conversation.StatusRejected {
			rb.DecidedAt = conversation.Int64Ptr(now)
		} else {
			needsShareDecision = true
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

	if !executedAny {
		// Rejection-only batches produce no follow-up stream; a waiting
		// delegation parent must still be told the sub-turn settled, and
		// (async only) open clients need a refetch nudge since neither a
		// stream event nor an HTTP completion will trigger one.
		c.notifyDelegationSubTurnCompleted(conv)
		if isAsync {
			c.publishConversationUpdated(conv, post)
		}
		return nil
	}

	// In channels the follow-up is a channel-visible post that may paraphrase tool
	// output, so it must not stream until the requester approves sharing in
	// HandleToolResult. When no share decision remains (every executed result
	// was a user-interaction answer), HandleToolResult will never fire, so
	// stream the follow-up now. Batches executed asynchronously (delegations)
	// have long since answered their HTTP request, so nudge open clients to
	// refetch the conversation — that is what reveals the Share decision.
	if !isDM && needsShareDecision {
		if isAsync {
			c.publishConversationUpdated(conv, post)
		}
		return nil
	}

	if err := c.streamToolFollowUp(ctx, bot, user, channel, post, conv, isDM, llmContext); err != nil {
		// The follow-up never started, so its onDone hook will not fire;
		// wake any waiting delegation parent to observe the failed state.
		c.notifyDelegationSubTurnCompleted(conv)
		return err
	}
	return nil
}

// resolveInteractionAnswers validates the user's answers for every accepted
// pending user-interaction block and returns the tool result content keyed by
// block ID. Any invalid or missing answer fails the whole request with
// ErrInvalidToolAnswer before any state is mutated.
func resolveInteractionAnswers(blocks []conversation.ContentBlock, acceptedToolIDs []string, toolAnswers map[string]mmtools.UserInteractionAnswer) (map[string]string, error) {
	results := make(map[string]string)
	for _, b := range blocks {
		if b.Type != conversation.BlockTypeToolUse || b.UserInteraction == "" {
			continue
		}
		if b.Status != conversation.StatusPending && b.Status != conversation.StatusAccepted {
			continue
		}
		if !slices.Contains(acceptedToolIDs, b.ID) {
			continue
		}
		result, err := mmtools.ResolveUserInteractionAnswer(b.UserInteraction, b.Input, toolAnswers[b.ID])
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidToolAnswer, err.Error())
		}
		results[b.ID] = result
	}
	return results, nil
}

// HandleToolResult handles user approval of the second-stage tool-result sharing.
// It flips shared flags for accepted results and, in channels, streams the LLM
// follow-up with unshared content redacted so private tool output cannot leak
// into the channel-visible reply.
func (c *Conversations) HandleToolResult(ctx context.Context, userID string, post *model.Post, channel *model.Channel, acceptedToolIDs []string) error {
	ctx = c.rehydrateRunTrace(ctx, post)

	ctx, span := telemetry.Tracer().Start(ctx, "handle tool result",
		trace.WithNewRoot(),
		trace.WithAttributes(telemetry.PostID.String(post.Id)),
	)
	defer span.End()

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
					if _, ok := clickedPostToolUseIDs[blocks[i].ID]; ok {
						blocks[i].Shared = conversation.BoolPtr(true)
						modified = true
					}
				}
			case conversation.BlockTypeToolResult:
				if acceptedSet[blocks[i].ToolUseID] {
					if _, ok := clickedPostToolUseIDs[blocks[i].ToolUseID]; ok {
						blocks[i].Shared = conversation.BoolPtr(true)
						modified = true
					}
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

	// Channel second-stage follow-up rebuilds a fresh llmContext without in-request
	// WebSearch data; citation decoration is DM-only via HandleToolCall's llmContext.
	return c.streamToolFollowUp(ctx, bot, user, channel, post, conv, false, nil)
}

// streamToolFollowUp rebuilds the completion request from the conversation and
// streams a follow-up LLM response after tool execution. The request redacts
// tool_result content the user kept private before reaching the LLM — for DMs
// this is a no-op (all tool_results are shared=true), for channels it is the
// privacy guarantee that keeps unshared tool output from leaking into a
// channel-visible reply.
func (c *Conversations) streamToolFollowUp(
	ctx context.Context,
	bot *bots.Bot,
	user *model.User,
	channel *model.Channel,
	post *model.Post,
	conv *store.Conversation,
	isDM bool,
	approvalContext *llm.Context,
) error {
	ctx, span := telemetry.Tracer().Start(ctx, "tool followup completion")
	defer span.End()

	channelToolFilterOpts, channelToolsAutoRunEverywhereOnly := c.channelFollowUpMCPToolFilterContextOptions(isDM, conv)
	// The follow-up runs because a user clicked an approval control, so the
	// requester is interactively present — unless the conversation root was a
	// bot activate_ai flow, which stays constrained to unattended tools.
	if !channelToolsAutoRunEverywhereOnly {
		channelToolFilterOpts = append(channelToolFilterOpts, c.contextBuilder.WithLLMContextInteractive())
	}
	// Delegation conversations never regain ask_agent on resume.
	channelToolFilterOpts = append(channelToolFilterOpts, c.delegationConversationContextOptions(conv)...)
	// Build the execution context bound to this conversation.
	llmContext := c.buildConversationContextWithTools(
		ctx,
		bot, user, channel,
		"Failed to load user tool preferences for tool follow-up",
		channelToolFilterOpts...,
	)

	toolsDisabled := !isDM
	if !isDM && c.configProvider != nil && c.configProvider.EnableChannelMentionToolCalling() {
		toolsDisabled = false
	}
	if toolsDisabled && llmContext.Tools != nil {
		llmContext.DisabledToolsInfo = llmContext.Tools.GetToolsInfo()
	}

	if !isDM && !toolsDisabled && channelToolsAutoRunEverywhereOnly {
		c.applyBotChannelAutoEverywhereToolFilter(llmContext)
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

	maxRounds := maxToolTurnsForConversation(bot.GetConfig().EffectiveMaxToolTurns(), conv)
	runner := toolrunner.New(bot.LLM(), toolrunner.WithMaxRounds(maxRounds))
	runResult, err := runner.Run(ctx, *completionReq, c.shouldAutoExecuteTool(llmContext, isDM), func(turns []toolrunner.ToolTurn) {
		shared := isDM || c.allToolsAutoRunEverywhere(turns, llmContext)
		if writeErr := c.convService.WriteToolTurns(conv.ID, turns, shared); writeErr != nil {
			c.mmClient.LogError("Failed to write tool turns on follow-up", "error", writeErr)
		}
	}, opts...)

	if err != nil {
		return fmt.Errorf("tool runner failed on tool follow-up: %w", err)
	}

	stream := decorateStreamWithWebSearchAnnotations(runResult.Stream, approvalContext)

	// Delegated sub-turns signal the waiting parent once the resumed round
	// has fully streamed (the DB then holds the final answer or the next
	// pending approval).
	var onDone func()
	if conv != nil && conv.Operation == llm.OperationDelegation {
		convForNotify := conv
		onDone = func() { c.notifyDelegationSubTurnCompleted(convForNotify) }
	}

	// Stream onto the same post; finalize demotes the prior anchor so
	// resolved tool cards remain visible alongside the new round.
	if err := c.streamContinuationToExistingPost(ctx, stream, post, user, channel, onDone); err != nil {
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

func resolveApprovedToolUseBlock(ctx context.Context, llmContext *llm.Context, block conversation.ContentBlock) (string, error) {
	if llmContext == nil || llmContext.Tools == nil {
		return "", fmt.Errorf("tool %s is no longer available", block.Name)
	}

	lookup, ok := llmContext.Tools.LookupTool(block.Name, block.ServerOrigin)
	if !ok {
		if existing, found := llmContext.Tools.LookupTool(block.Name, ""); found {
			if block.ServerOrigin != "" && existing.ServerOrigin != block.ServerOrigin {
				return "", fmt.Errorf("tool %s no longer matches the approved tool metadata", block.Name)
			}
			if block.MCPBareName != "" && existing.BareName != block.MCPBareName {
				return "", fmt.Errorf("tool %s no longer matches the approved tool metadata", block.Name)
			}
		}
		if llmContext.Tools.IsUnloadedMCPTool(block.Name) {
			return "", errors.New(mcp.UnloadedMCPToolUserHint(block.Name))
		}
		return "", fmt.Errorf("tool %s is no longer available", block.Name)
	}

	if block.MCPBareName != "" && lookup.BareName != block.MCPBareName {
		return "", fmt.Errorf("tool %s no longer matches the approved tool metadata", block.Name)
	}

	// Stamp the tool call ID so per-call consumers (e.g. embedded MCP call
	// metadata) can key state on it, matching the tool runner's behavior.
	ctx = llm.ContextWithToolCallID(ctx, block.ID)

	return llmContext.Tools.ResolveTool(ctx, lookup.RuntimeName, func(args any) error {
		return json.Unmarshal(block.Input, args)
	}, llmContext)
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

// rehydrateRunTrace stamps ctx with the user-turn ID that initiated the run
// associated with post, so a span started under WithNewRoot lands in the
// originating run's deterministic trace. Best-effort: any lookup miss leaves
// ctx unchanged and the resume gets a fresh trace.
func (c *Conversations) rehydrateRunTrace(ctx context.Context, post *model.Post) context.Context {
	if post == nil || c.convService == nil {
		return ctx
	}
	convID, ok := post.GetProp(streaming.ConversationIDProp).(string)
	if !ok || convID == "" {
		return ctx
	}
	userTurn, err := c.convService.GetInitiatingUserTurn(convID, post.Id)
	if err != nil || userTurn == nil {
		return ctx
	}
	return telemetry.WithTurnID(ctx, userTurn.ID)
}
