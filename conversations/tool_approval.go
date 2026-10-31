// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
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
// requester tries to regenerate a response or decide on its tool calls or
// results. The HTTP layer maps this to 403 Forbidden.
var ErrNotRequester = errors.New("only the original requester can act on this conversation")

// ErrInvalidToolAnswer is returned when an accepted user-interaction tool call
// (e.g. AskUserQuestion) arrives without a valid answer. The pending state is
// left untouched so the user can answer again. The HTTP layer maps this to
// 400 Bad Request.
var ErrInvalidToolAnswer = errors.New("invalid answer for user interaction tool call")

// ErrRemoteMCPNotLicensed is returned when a tool decision would execute, or
// share the output of, a tool served by a remote/external MCP server.
// Remote and plugin MCP servers are available at Enterprise and above.
// Built-in tools (empty ServerOrigin) and embedded Mattermost MCP tools
// (mcp.EmbeddedClientKey) are available at every license level. The HTTP
// layer maps this to 403 Forbidden.
//
// This gate is the decision-time backstop for pending remote tool calls
// persisted before a license change; the primary enforcement is at supply
// time, where llmcontext.Builder drops remote MCP tools from the LLM context
// when CapRemoteMCP is not available.
var ErrRemoteMCPNotLicensed = errors.New("tools from remote MCP servers are available at Enterprise and above")

// Canned tool_result content for non-executing resolutions. These are shown
// to the LLM (and, when terminal, in channels); they must not include tool
// arguments or blame the user for an administrative policy/license change.
const (
	toolCallRejectedByUserResult = "Tool call rejected by user"
	toolCallPolicyDeniedResult   = "Tool call was not executed because it is no longer permitted by policy or license"
)

// toolRejectionGuidance is appended as a final user message to follow-ups
// after a user rejection. It must not include tool arguments.
const toolRejectionGuidance = "The user rejected one or more of your tool calls; their results read \"" +
	toolCallRejectedByUserResult + "\". Do not repeat a rejected call with the same arguments. " +
	"Ask the user for clarification or take a different approach."

// isUserRejectedToolUse reports whether block was rejected by the user, as
// opposed to a skipped question (UserInteraction) or a policy/license denial
// of a deferred auto-run (WouldAutoExecute).
func isUserRejectedToolUse(block conversation.ContentBlock) bool {
	return block.Type == conversation.BlockTypeToolUse &&
		block.Status == conversation.StatusRejected &&
		block.UserInteraction == "" &&
		!block.WouldAutoExecute
}

// toolDecision carries the shared state resolved by beginToolDecision for a
// tool-approval handler: the resumed trace context and its open span, the
// conversation behind the clicked post, the request's audit record, the
// loaded conversation turns, and the policy of the run being resumed.
type toolDecision struct {
	ctx      context.Context
	span     trace.Span
	auditRec *model.AuditRecord
	convID   string
	conv     *store.Conversation
	turns    []store.Turn
	policy   *turnPolicy
}

// beginToolDecision performs the preamble shared by HandleToolCall and
// HandleToolResult: resume into the originating run's trace (falling back to a
// fresh trace when the post or its assistant turn is missing), open a new-root
// span named spanName, resolve the bot and conversation for the clicked post,
// verify the caller is the conversation requester, load the turns, resolve
// the run's policy, and refuse decisions on a run whose tools are disabled.
// On error the span is ended before returning; on success the caller must
// defer span.End().
func (c *Conversations) beginToolDecision(ctx context.Context, spanName, logMsg, userID string, post *model.Post, channel *model.Channel, acceptedCount int) (*toolDecision, error) {
	ctx = c.rehydrateRunTrace(ctx, post)

	ctx, span := telemetry.Tracer().Start(ctx, spanName,
		trace.WithNewRoot(),
		trace.WithAttributes(telemetry.PostID.String(post.Id)),
	)
	fail := func(err error) (*toolDecision, error) {
		span.End()
		return nil, err
	}

	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		return fail(fmt.Errorf("unable to get bot"))
	}

	convID, ok := post.GetProp(streaming.ConversationIDProp).(string)
	if !ok || convID == "" {
		return fail(ErrPostMissingConversationID)
	}

	c.mmClient.LogDebug(logMsg,
		"post_id", post.Id,
		"conv_id", convID,
		"accepted_count", acceptedCount,
	)

	conv, err := c.convService.GetConversationIncludingArchived(convID)
	if err != nil {
		return fail(fmt.Errorf("failed to get conversation: %w", err))
	}

	if conv.UserID != userID {
		return fail(ErrNotRequester)
	}

	turns, err := c.convService.GetTurns(convID)
	if err != nil {
		return fail(fmt.Errorf("failed to get turns: %w", err))
	}

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return fail(fmt.Errorf("unable to get user: %w", err))
	}
	policy, err := c.resolveTurnPolicy(ctx, turnRequest{bot: bot, user: user, channel: channel, conv: conv, post: post})
	if err != nil {
		return fail(err)
	}
	if !policy.toolsEnabled {
		return fail(ErrToolCallingDisabled)
	}
	if err := c.activateTurnConversation(policy); err != nil {
		return fail(err)
	}
	conv = policy.conv

	return &toolDecision{
		ctx:      ctx,
		span:     span,
		auditRec: audit.RecordFromContext(ctx),
		convID:   convID,
		conv:     conv,
		turns:    turns,
		policy:   policy,
	}, nil
}

// HandleToolCall handles user approval/rejection of pending tool calls via conversation entities.
// It looks up pending tool_use blocks in the conversation turns, executes approved tools,
// writes results back as turns, and streams a follow-up LLM response.
//
// toolAnswers carries the user's answers for accepted user-interaction tool
// calls (e.g. AskUserQuestion), keyed by tool_use block ID. Those blocks are
// not executed; the validated answer becomes the tool result.
func (c *Conversations) HandleToolCall(ctx context.Context, userID string, post *model.Post, channel *model.Channel, acceptedToolIDs []string, toolAnswers map[string]mmtools.UserInteractionAnswer) error {
	d, err := c.beginToolDecision(ctx, "handle tool call", "HandleToolCall", userID, post, channel, len(acceptedToolIDs))
	if err != nil {
		return err
	}
	defer d.span.End()
	ctx = d.ctx
	auditRec, convID, conv, turns, policy := d.auditRec, d.convID, d.conv, d.turns, d.policy

	pendingTurn, pendingBlocks, err := findPendingToolTurn(turns, post.Id)
	if err != nil {
		return err
	}

	// Accepting tools from remote/external MCP servers is available at
	// Enterprise and above. Built-in and embedded Mattermost MCP tools are
	// available at every license level. Rejections are always allowed —
	// they execute nothing. Blocks marked WouldAutoExecute are not gated
	// here: their resume re-checks the policy's auto-execute decision, which
	// refuses them on the same license, so they resolve to a rejection
	// instead of blocking the whole submission on a possibly stale marker.
	for _, b := range pendingBlocks {
		if b.Type != conversation.BlockTypeToolUse || !slices.Contains(acceptedToolIDs, b.ID) {
			continue
		}
		if b.Status != conversation.StatusPending && b.Status != conversation.StatusAccepted {
			continue
		}
		if licenseErr := c.remoteMCPDecisionAllowed(b.ServerOrigin); licenseErr != nil {
			return licenseErr
		}
	}

	// The approval runs in the same context as the run it resumes, so a
	// click cannot reach tools the run itself could not have been offered.
	llmContext := policy.buildContext(ctx, "Failed to load user tool preferences for tool approval")
	llmContext.ResponsePostID = post.Id
	// The clicked post may already carry attachments from an earlier round.
	llmContext.SetResponseAttachmentBudget(maxResponseAttachments - len(post.FileIds))

	conversation.RestoreLoadedMCPToolsFromTurns(llmContext.Tools, turns)

	// Validate answers for accepted interaction blocks before mutating any
	// state, so a malformed or missing answer leaves the question pending and
	// answerable instead of burning it as an error result.
	interactionResults, err := resolveInteractionAnswers(pendingBlocks, acceptedToolIDs, toolAnswers, llmContext, policy.interactive)
	if err != nil {
		return err
	}

	claimedContent, err := c.claimToolDecision(pendingTurn, pendingBlocks)
	if err != nil {
		return err
	}

	// Execute approved tools and build results. Blocks resolved in a prior
	// call keep their status and are not part of this decision, so they enter
	// neither audit list; anything auto-executed this call counts as accepted.
	autoExec := policy.autoExecute(llmContext)
	autoExecutedNow := make(map[string]bool)
	userRejected := false
	acceptedToolNames := []string{}
	rejectedToolNames := []string{}
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

		switch {
		case slices.Contains(acceptedToolIDs, block.ID) && block.UserInteraction != "":
			acceptedToolNames = append(acceptedToolNames, block.Name)
			// Shared so the channel-visible follow-up may reference the answer.
			block.Status = conversation.StatusSuccess
			block.Shared = new(true)
			toolResults = append(toolResults, toolrunner.ToolResult{
				ToolCallID: block.ID,
				Name:       block.Name,
				Result:     interactionResults[block.ID],
				IsError:    false,
			})
		case slices.Contains(acceptedToolIDs, block.ID):
			acceptedToolNames = append(acceptedToolNames, block.Name)
			result, resolveErr := resolveApprovedToolUseBlock(ctx, llmContext, *block, telemetry.ToolExecutionApproved)
			if resolveErr != nil {
				block.Status = conversation.StatusError
				toolResults = append(toolResults, toolrunner.ToolResult{
					ToolCallID: block.ID,
					Name:       block.Name,
					Result:     llmcontext.MCPToolErrorResult(llmContext, resolveErr),
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
			rejectedToolNames = append(rejectedToolNames, block.Name)
			// Skipped question: record the decline as the result and stream a
			// follow-up so the model can proceed without the answer, per the
			// tool contract. Shared because the decline is user-authored, not
			// private tool output. UserInteraction stays on the block so the
			// follow-up can distinguish this from a regular tool rejection.
			block.Status = conversation.StatusRejected
			block.Shared = new(true)
			toolResults = append(toolResults, toolrunner.ToolResult{
				ToolCallID: block.ID,
				Name:       block.Name,
				Result:     skippedInteractionResult(*block),
				IsError:    true,
			})
		case block.WouldAutoExecute && autoExec(llm.ToolCall{Name: block.Name, ServerOrigin: block.ServerOrigin}):
			acceptedToolNames = append(acceptedToolNames, block.Name)
			// Runs on resume without a click. Requiring both the marker and a
			// fresh policy check means a mid-turn policy flip can neither
			// auto-run a tool the user was asked to approve (or rejected) nor
			// run one whose policy was since revoked. Shared and terminal, like
			// any auto-run round.
			result, resolveErr := resolveApprovedToolUseBlock(ctx, llmContext, *block, telemetry.ToolExecutionAuto)
			autoExecutedNow[block.ID] = true
			block.Shared = new(true)
			if resolveErr != nil {
				block.Status = conversation.StatusError
				toolResults = append(toolResults, toolrunner.ToolResult{
					ToolCallID: block.ID,
					Name:       block.Name,
					Result:     llmcontext.MCPToolErrorResult(llmContext, resolveErr),
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
		case block.WouldAutoExecute:
			// Fresh policy/license check failed. Keep the WouldAutoExecute
			// marker so isUserRejectedToolUse does not treat this as a user
			// rejection, and record a result that does not blame the user
			// for an admin change.
			rejectedToolNames = append(rejectedToolNames, block.Name)
			block.Status = conversation.StatusRejected
			toolResults = append(toolResults, toolrunner.ToolResult{
				ToolCallID: block.ID,
				Name:       block.Name,
				Result:     toolCallPolicyDeniedResult,
				IsError:    true,
			})
		default:
			rejectedToolNames = append(rejectedToolNames, block.Name)
			block.Status = conversation.StatusRejected
			userRejected = true
			toolResults = append(toolResults, toolrunner.ToolResult{
				ToolCallID: block.ID,
				Name:       block.Name,
				Result:     toolCallRejectedByUserResult,
				IsError:    true,
			})
		}
	}

	// Unlike HandleToolResult (which defers these until after its idempotency
	// gate), the names here describe tools that already executed above — real
	// side effects, not persisted state — so they stay on the record even if
	// persisting the resolved turn below fails.
	audit.AddParam(auditRec, "accepted_tools", acceptedToolNames)
	audit.AddParam(auditRec, "rejected_tools", rejectedToolNames)

	// Update the assistant turn with resolved statuses.
	updatedContent, err := json.Marshal(pendingBlocks)
	if err != nil {
		return fmt.Errorf("failed to marshal updated blocks: %w", err)
	}
	swapped, err := c.convService.CompareAndSwapTurnContent(pendingTurn.ID, claimedContent, updatedContent)
	if err != nil {
		return fmt.Errorf("failed to update turn with resolved statuses: %w", err)
	}
	if !swapped {
		return fmt.Errorf("turn %s changed while its tool calls ran", pendingTurn.ID)
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
		// Rejected results (user rejection or policy/license denial) share only
		// the canned reason; tool_use arguments stay unshared so they are not
		// paraphrased into a channel reply.
		rejected := toolUseStatusByID[tr.ToolCallID] == conversation.StatusRejected
		terminal := policy.isDM || interactionByID[tr.ToolCallID] || autoExecutedNow[tr.ToolCallID] || rejected
		rb := conversation.ContentBlock{
			Type:      conversation.BlockTypeToolResult,
			ToolUseID: tr.ToolCallID,
			Content:   tr.Result,
			Status:    status,
			Shared:    new(terminal),
		}
		if terminal {
			rb.DecidedAt = new(now)
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

	if len(toolResults) == 0 {
		return nil
	}

	// In channels the follow-up is a channel-visible post that may paraphrase tool
	// output, so it must not stream until the requester approves sharing in
	// HandleToolResult. When no share decision remains (every result is
	// rejected, a user-interaction answer, or otherwise terminal),
	// HandleToolResult will never fire, so stream the follow-up now.
	if !policy.isDM && needsShareDecision {
		return nil
	}

	return c.streamToolFollowUp(ctx, policy, post, conv, followUpOptions{UserRejected: userRejected, ApprovalContext: llmContext})
}

// resolveInteractionAnswers validates the user's answers for every accepted
// pending user-interaction block and returns the tool result content keyed by
// block ID. Any invalid or missing answer fails the whole request with
// ErrInvalidToolAnswer before any state is mutated. Connection requests carry
// no answer: when the run is interactive their result reports whether
// llmContext, built after the user's decision, has the requested server's
// tools; otherwise the run cannot use them, which the result says.
func resolveInteractionAnswers(blocks []conversation.ContentBlock, acceptedToolIDs []string, toolAnswers map[string]mmtools.UserInteractionAnswer, llmContext *llm.Context, interactive bool) (map[string]string, error) {
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
		var result string
		var err error
		switch {
		case b.UserInteraction == llm.UserInteractionConnect && interactive:
			result, err = mcp.ResolveConnectionRequest(b.Input, llmContext)
		case b.UserInteraction == llm.UserInteractionConnect:
			result, err = mcp.ConnectionRequestUnusableResult(b.Input)
		default:
			result, err = mmtools.ResolveUserInteractionAnswer(b.UserInteraction, b.Input, toolAnswers[b.ID])
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidToolAnswer, err.Error())
		}
		results[b.ID] = result
	}
	return results, nil
}

func skippedInteractionResult(block conversation.ContentBlock) string {
	if block.UserInteraction == llm.UserInteractionConnect {
		return mcp.ConnectionRequestDeclinedResult(block.Input)
	}
	return "User skipped the question"
}

// HandleToolResult handles user approval of the second-stage tool-result sharing.
// It flips shared flags for accepted results and, in channels, streams the LLM
// follow-up with unshared content redacted so private tool output cannot leak
// into the channel-visible reply.
func (c *Conversations) HandleToolResult(ctx context.Context, userID string, post *model.Post, channel *model.Channel, acceptedToolIDs []string) error {
	d, err := c.beginToolDecision(ctx, "handle tool result", "HandleToolResult", userID, post, channel, len(acceptedToolIDs))
	if err != nil {
		return err
	}
	defer d.span.End()
	ctx = d.ctx
	auditRec, conv, turns, policy := d.auditRec, d.conv, d.turns, d.policy

	acceptedSet := make(map[string]bool, len(acceptedToolIDs))
	for _, id := range acceptedToolIDs {
		acceptedSet[id] = true
	}

	// Decode each turn's blocks once; the classification, idempotency, and
	// mutation passes below all work over the same decoded slices. A turn
	// whose content fails to decode is skipped by every pass.
	decoded := make([][]conversation.ContentBlock, len(turns))
	for i := range turns {
		if blocks, unmarshalErr := conversation.UnmarshalBlocks(turns[i].Content); unmarshalErr == nil {
			decoded[i] = blocks
		}
	}

	// Classify the clicked post's tool_use blocks. DecidedAt applies to the
	// matching tool_result blocks; we also need to know whether any tool
	// actually executed to decide whether a follow-up stream is warranted.
	clickedPostToolUseIDs := make(map[string]struct{})
	clickedPostHasExecutedTool := false
	clickedPostUserRejected := false
	acceptedToolNames := []string{}
	rejectedToolNames := []string{}
	var shareLicenseErr error
	for i, turn := range turns {
		if turn.Role != "assistant" || turn.PostID == nil || *turn.PostID != post.Id {
			continue
		}
		for _, b := range decoded[i] {
			if b.Type != conversation.BlockTypeToolUse || b.ID == "" {
				continue
			}
			clickedPostToolUseIDs[b.ID] = struct{}{}
			if acceptedSet[b.ID] {
				acceptedToolNames = append(acceptedToolNames, b.Name)
			} else {
				rejectedToolNames = append(rejectedToolNames, b.Name)
			}
			if b.Status == conversation.StatusSuccess ||
				b.Status == conversation.StatusError ||
				b.Status == conversation.StatusAutoApproved {
				clickedPostHasExecutedTool = true
			}
			if isUserRejectedToolUse(b) {
				clickedPostUserRejected = true
			}
			if acceptedSet[b.ID] && shareLicenseErr == nil {
				shareLicenseErr = c.remoteMCPDecisionAllowed(b.ServerOrigin)
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
	for i := range turns {
		for _, b := range decoded[i] {
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

	// Audit the decision only after the idempotency gate: a repeat click
	// changes nothing, so its record must not claim a share resolution. The
	// license gate below may still deny the share — that record then carries
	// the attempted names with a fail status, which is the point.
	audit.AddParam(auditRec, "accepted_tools", acceptedToolNames)
	audit.AddParam(auditRec, "rejected_tools", rejectedToolNames)

	// Sharing output from remote/external MCP tools is available at
	// Enterprise and above (see HandleToolCall). Keep-private decisions are
	// always allowed — they disclose nothing.
	if shareLicenseErr != nil {
		return shareLicenseErr
	}

	now := model.GetMillis()
	// Turns that record the decision (DecidedAt) are written first and
	// conditionally: concurrent clicks race on the same first turn, and the
	// loser fails before writing anything or streaming a second follow-up.
	var decisionUpdates, otherUpdates []turnUpdate
	for i := range turns {
		blocks := decoded[i]

		modified := false
		recordsDecision := false
		for j := range blocks {
			switch blocks[j].Type {
			case conversation.BlockTypeToolUse:
				if acceptedSet[blocks[j].ID] {
					if _, ok := clickedPostToolUseIDs[blocks[j].ID]; ok {
						blocks[j].Shared = new(true)
						modified = true
					}
				}
			case conversation.BlockTypeToolResult:
				if acceptedSet[blocks[j].ToolUseID] {
					if _, ok := clickedPostToolUseIDs[blocks[j].ToolUseID]; ok {
						blocks[j].Shared = new(true)
						modified = true
					}
				}
				if _, ok := clickedPostToolUseIDs[blocks[j].ToolUseID]; ok && blocks[j].DecidedAt == nil {
					blocks[j].DecidedAt = new(now)
					modified = true
					recordsDecision = true
				}
			}
		}

		if !modified {
			continue
		}
		updatedContent, marshalErr := json.Marshal(blocks)
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal updated blocks: %w", marshalErr)
		}
		update := turnUpdate{turn: &turns[i], content: updatedContent}
		if recordsDecision {
			decisionUpdates = append(decisionUpdates, update)
		} else {
			otherUpdates = append(otherUpdates, update)
		}
	}

	updates := slices.Concat(decisionUpdates, otherUpdates)
	for i, update := range updates {
		swapped, updateErr := c.convService.CompareAndSwapTurnContent(update.turn.ID, update.turn.Content, update.content)
		if updateErr == nil && swapped {
			continue
		}
		if i == 0 && updateErr == nil {
			return fmt.Errorf("share decision for post %s was already recorded: %w", post.Id, ErrStaleToolClick)
		}
		// A recorded decision without its companion writes would make a retry
		// return early as already decided and never stream the follow-up.
		c.undoTurnUpdates(updates[:i])
		if updateErr != nil {
			return fmt.Errorf("failed to update turn shared flags: %w", updateErr)
		}
		return fmt.Errorf("turn %s changed while recording the share decision", update.turn.ID)
	}

	// DMs stream the follow-up from HandleToolCall.
	if policy.isDM {
		return nil
	}

	// Only stream a follow-up when there is something to follow up on:
	// at least one executed tool_result exists on this post. Rejected-only
	// posts already streamed their continuation from HandleToolCall (no
	// share decision remains), so a second-stage click must not stream again.
	if !clickedPostHasExecutedTool {
		return nil
	}

	// Channel second-stage follow-up rebuilds a fresh llmContext without in-request
	// WebSearch data; citation decoration is DM-only via HandleToolCall's llmContext.
	return c.streamToolFollowUp(ctx, policy, post, conv, followUpOptions{UserRejected: clickedPostUserRejected})
}

// followUpOptions are the per-caller knobs of streamToolFollowUp.
type followUpOptions struct {
	// Unattended marks a scheduler resume: no interactive user is present.
	Unattended bool
	// UserRejected appends rejection guidance so the model does not retry.
	UserRejected bool
	// ApprovalContext is the in-request context whose created files and web
	// search data decorate the stream; nil on the channel second-stage path.
	ApprovalContext *llm.Context
}

// streamToolFollowUp rebuilds the completion request from the conversation and
// streams a follow-up LLM response after tool execution. The request redacts
// tool_result content the user kept private before reaching the LLM — for DMs
// this is a no-op (all tool_results are shared=true), for channels it is the
// privacy guarantee that keeps unshared tool output from leaking into a
// channel-visible reply.
func (c *Conversations) streamToolFollowUp(
	ctx context.Context,
	policy *turnPolicy,
	post *model.Post,
	conv *store.Conversation,
	opts followUpOptions,
) error {
	ctx, span := telemetry.Tracer().Start(ctx, "tool followup completion")
	defer span.End()

	if opts.Unattended {
		unattended := *policy
		unattended.interactive = false
		policy = &unattended
	}
	llmContext := policy.buildContext(ctx, "Failed to load user tool preferences for tool follow-up")
	llmContext.ResponsePostID = post.Id
	// The continuation post may already carry attachments from an earlier round.
	llmContext.SetResponseAttachmentBudget(maxResponseAttachments - len(post.FileIds))

	// Channel thread posts aren't stored as turns, so rebuild with thread context.
	completionReq, err := c.buildToolFollowUpRequest(conv, llmContext, policy.isDM, conversation.BuildOptions{
		SessionID:                          auth.SessionIDFromContext(ctx),
		ResponsePostID:                     post.Id,
		SkipThreadPostsAfterLatestUserTurn: opts.Unattended,
	})
	if err != nil {
		return fmt.Errorf("failed to build completion request for tool follow-up: %w", err)
	}
	if opts.UserRejected {
		completionReq.Posts = append(completionReq.Posts, llm.Post{Role: llm.PostRoleUser, Message: toolRejectionGuidance})
	}
	completionReq.Operation = llm.OperationConversationToolFollowup
	completionReq.OperationSubType = llm.SubTypeToolCall

	runResult, err := policy.runToolLoop(ctx, llmContext, conv.ID, *completionReq, "Failed to write tool turns on follow-up")

	if err != nil {
		return fmt.Errorf("tool runner failed on tool follow-up: %w", err)
	}

	stream := decorateStreamWithWebSearchAnnotations(runResult.Stream, opts.ApprovalContext)

	// Recover files created in earlier requests of this run (e.g. an
	// approved CreateFile whose share decision arrives in a later request,
	// where the in-memory registry is gone) from the persisted turns.
	// ApprovalContext is nil on the HandleToolResult path; the decorator
	// tolerates nil contexts.
	extraFileIDs := c.collectCreatedFileIDsFromTurns(conv.ID, post.Id)
	stream = c.decorateStreamWithCreatedFiles(ctx, policy.bot, stream, post, extraFileIDs, llmContext, llmContext, opts.ApprovalContext)

	// Stream onto the same post; finalize demotes the prior anchor so
	// resolved tool cards remain visible alongside the new round.
	if err := c.streamToExistingPost(ctx, stream, post, policy.user, policy.channel, true, nil); err != nil {
		return fmt.Errorf("failed to stream tool follow-up: %w", err)
	}

	return nil
}

// buildToolFollowUpRequest rebuilds the completion request for a tool follow-up.
// Channel conversations re-fetch the live thread so non-turn thread posts stay in
// context (matching the initial mention); DMs persist every post as a turn.
// Unattended follow-ups drop thread posts made after the latest user turn,
// since no one vetted them before tools may auto-run.
func (c *Conversations) buildToolFollowUpRequest(conv *store.Conversation, llmContext *llm.Context, isDM bool, buildOpts conversation.BuildOptions) (*llm.CompletionRequest, error) {
	if !isDM && conv.RootPostID != nil {
		// Best-effort: if the live thread can't be fetched (deleted root,
		// permissions, API blip), degrade to turns-only context rather than
		// failing the resume. BuildChannelMentionRequest does the same on
		// empty thread data.
		threadData, err := mmapi.GetThreadData(c.mmClient, *conv.RootPostID)
		if err != nil {
			c.mmClient.LogWarn("Failed to get thread data for tool follow-up, falling back to turns-only context", "error", err)
			return c.convService.BuildCompletionRequest(conv, llmContext, buildOpts)
		}
		return c.convService.BuildChannelMentionRequest(conv, llmContext, threadData, buildOpts)
	}
	return c.convService.BuildCompletionRequest(conv, llmContext, buildOpts)
}

// execution is telemetry.ToolExecutionApproved for a call the user approved
// or telemetry.ToolExecutionAuto for one that runs on resume by policy.
func resolveApprovedToolUseBlock(ctx context.Context, llmContext *llm.Context, block conversation.ContentBlock, execution string) (string, error) {
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

	return llmContext.Tools.ResolveToolCall(ctx, lookup.RuntimeName,
		llm.ToolCallInfo{ID: block.ID, Execution: execution},
		func(args any) error { return json.Unmarshal(block.Input, args) },
		llmContext)
}

// turnUpdate is a conditional rewrite of turn from its read content to content.
type turnUpdate struct {
	turn    *store.Turn
	content json.RawMessage
}

// undoTurnUpdates restores applied updates, newest first. It is best effort:
// a restore that fails or finds the turn changed again is logged and skipped.
func (c *Conversations) undoTurnUpdates(applied []turnUpdate) {
	for _, update := range slices.Backward(applied) {
		swapped, err := c.convService.CompareAndSwapTurnContent(update.turn.ID, update.content, update.turn.Content)
		if err != nil || !swapped {
			c.mmClient.LogError("Failed to undo a partial tool decision write", "turn_id", update.turn.ID, "error", err)
		}
	}
}

// claimToolDecision marks the turn's pending tool_use blocks accepted, but only
// while the turn is unchanged since it was read, so a double click, a retry, or
// an HA peer handling the same click cannot execute the calls twice. It returns
// the claimed content for the final conditional write.
func (c *Conversations) claimToolDecision(turn *store.Turn, blocks []conversation.ContentBlock) (json.RawMessage, error) {
	claimed := slices.Clone(blocks)
	for i := range claimed {
		if claimed[i].Type == conversation.BlockTypeToolUse && claimed[i].Status == conversation.StatusPending {
			claimed[i].Status = conversation.StatusAccepted
		}
	}
	content, err := json.Marshal(claimed)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal claimed blocks: %w", err)
	}
	swapped, err := c.convService.CompareAndSwapTurnContent(turn.ID, turn.Content, content)
	if err != nil {
		return nil, fmt.Errorf("failed to claim tool decision: %w", err)
	}
	if !swapped {
		return nil, fmt.Errorf("tool calls on turn %s were already decided: %w", turn.ID, ErrStaleToolClick)
	}
	return content, nil
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

		blocks, err := conversation.UnmarshalBlocks(turns[i].Content)
		if err != nil {
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
