// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// AskUserPostType is the custom post type of the target-side question card.
const AskUserPostType = "custom_llm_ask_user"

// Card post prop keys (C4). Mirrored as literals in the webapp.
const (
	AskUserStatusProp         = "ask_user_status"
	AskUserQuestionProp       = "ask_user_question"
	AskUserContextProp        = "ask_user_context"
	AskUserOptionsProp        = "ask_user_options"
	AskUserMultiSelectProp    = "ask_user_multi_select"
	AskUserAllowFreeFormProp  = "ask_user_allow_free_form"
	AskUserRequesterIDProp    = "ask_user_requester_id"
	AskUserTargetIDProp       = "ask_user_target_id"
	AskUserConversationIDProp = "ask_user_conversation_id"
	AskUserToolUseIDProp      = "ask_user_tool_use_id"
	AskUserSourcePostIDProp   = "ask_user_source_post_id"
	AskUserAnsweredAtProp     = "ask_user_answered_at"
	AskUserAnswerPreviewProp  = "ask_user_answer_preview"
)

const (
	AskUserStatusPending  = "pending"
	AskUserStatusAnswered = "answered"
	AskUserStatusDeclined = "declined"

	AskUserActionAnswer  = "answer"
	AskUserActionDecline = "decline"
)

// askUserAnswerPreviewMaxLen caps the card's answer preview prop (C4).
const askUserAnswerPreviewMaxLen = 200

// ErrNotAskTarget is returned when a user other than the asked target attempts
// to answer an ask-another-user question. The HTTP layer maps this to 403
// Forbidden.
var ErrNotAskTarget = errors.New("only the asked user can answer this question")

// ErrAskConversationGone is returned when the conversation behind an
// ask-another-user card no longer exists (deleted, or the waiting tool call
// was superseded by a regenerate). The HTTP layer maps this to 404 Not Found.
var ErrAskConversationGone = errors.New("the conversation for this question no longer exists")

// ErrAskNotPending is returned when the question was already answered or
// declined. Repeat submissions are safe and cheap. The HTTP layer maps this
// to 409 Conflict.
var ErrAskNotPending = errors.New("this question is no longer awaiting an answer")

// ErrInvalidAskAnswer is returned when the submitted answer fails validation
// against the original question. The waiting state is left untouched so the
// target can answer again. The HTTP layer maps this to 400 Bad Request.
var ErrInvalidAskAnswer = errors.New("invalid answer for ask-another-user question")

// AskUserResponse is the request body of POST /post/{postid}/ask_user_response (C5).
type AskUserResponse struct {
	Action   string   `json:"action"` // AskUserActionAnswer | AskUserActionDecline
	Selected []string `json:"selected"`
	FreeForm string   `json:"free_form"`
}

// dispatchAskAnotherUser validates the tool arguments and target user and
// sends the question card as a DM from the bot to the target. Any returned
// error becomes the error tool result fed back to the model.
func (c *Conversations) dispatchAskAnotherUser(ctx context.Context, bot *bots.Bot, conv *store.Conversation, anchorPostID string, toolUseID string, rawArgs json.RawMessage) (err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "dispatch ask another user",
		trace.WithAttributes(
			telemetry.ToolName.String(mmtools.AskAnotherUserToolName),
			telemetry.ToolID.String(toolUseID),
		),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(telemetry.ToolStatus.String("error"))
		} else {
			span.SetAttributes(telemetry.ToolStatus.String("success"))
		}
		span.End()
	}()

	var args mmtools.AskAnotherUserArgs
	if unmarshalErr := json.Unmarshal(rawArgs, &args); unmarshalErr != nil {
		return fmt.Errorf("invalid AskAnotherUser arguments: %v", unmarshalErr)
	}

	if validateErr := mmtools.ValidateAskAnotherUserArgs(args); validateErr != nil {
		return validateErr
	}

	target, lookupErr := c.mmClient.GetUserByUsername(strings.TrimPrefix(args.Username, "@"))
	if lookupErr != nil {
		return fmt.Errorf("user %q not found", args.Username)
	}
	if target.IsBot {
		return fmt.Errorf("%q is a bot and cannot be asked", args.Username)
	}
	if target.DeleteAt != 0 {
		return fmt.Errorf("%q is deactivated", args.Username)
	}
	if target.Id == conv.UserID {
		return errors.New("the requesting user cannot be the target; use the AskUserQuestion tool to ask them instead")
	}
	if accessErr := c.bots.CheckUsageRestrictionsForUser(bot, target.Id); accessErr != nil {
		return fmt.Errorf("%q does not have access to this agent", args.Username)
	}

	// Requester attribution is best-effort display data: an autonomous (bot)
	// invoker gets an empty requester (C4); lookup failures keep conv.UserID
	// for the card props but leave the fallback message unattributed.
	requesterID := conv.UserID
	requesterUsername := ""
	if requester, requesterErr := c.mmClient.GetUser(conv.UserID); requesterErr == nil {
		if requester.IsBot {
			requesterID = ""
		} else {
			requesterUsername = requester.Username
		}
	}

	sourcePostID := anchorPostID
	if sourcePostID == "" && conv.RootPostID != nil {
		sourcePostID = *conv.RootPostID
	}

	options := make([]any, 0, len(args.Options))
	for _, opt := range args.Options {
		// Props round-trip through JSON; write JSON-primitive shapes so
		// reads see the same []any of maps that persistence returns.
		options = append(options, map[string]any{
			"label":       opt.Label,
			"description": opt.Description,
		})
	}

	// The plaintext/mobile fallback must carry the same attribution as the
	// webapp card (F-001): a human requester is named so the target never
	// sees un-attributed bot-authored text. The attribution line comes FIRST
	// so LLM-authored question text cannot spoof it. Autonomous (bot)
	// invocations stay unattributed, matching the empty requester prop.
	const cardFallbackDefault = "%s\n\n(Interactive answer card — open Mattermost in a browser or the desktop app to respond.)"
	const cardFallbackAttributedDefault = "Asked on behalf of @%[1]s:\n\n%[2]s\n\n(Interactive answer card — open Mattermost in a browser or the desktop app to respond.)"
	var message string
	if requesterUsername != "" {
		message = fmt.Sprintf(cardFallbackAttributedDefault, requesterUsername, args.Question)
	} else {
		message = fmt.Sprintf(cardFallbackDefault, args.Question)
	}
	if c.i18n != nil {
		T := i18n.LocalizerFunc(c.i18n, c.fallbackLocale(target.Locale))
		if requesterUsername != "" {
			message = T("agents.ask_another_user_card_fallback_attributed", cardFallbackAttributedDefault, requesterUsername, args.Question)
		} else {
			message = T("agents.ask_another_user_card_fallback", cardFallbackDefault, args.Question)
		}
	}

	post := &model.Post{
		Type:    AskUserPostType,
		Message: message,
	}
	post.AddProp(AskUserStatusProp, AskUserStatusPending)
	post.AddProp(AskUserQuestionProp, args.Question)
	post.AddProp(AskUserContextProp, args.Context)
	post.AddProp(AskUserOptionsProp, options)
	post.AddProp(AskUserMultiSelectProp, args.MultiSelect)
	post.AddProp(AskUserAllowFreeFormProp, args.FreeFormEnabled())
	post.AddProp(AskUserRequesterIDProp, requesterID)
	post.AddProp(AskUserTargetIDProp, target.Id)
	post.AddProp(AskUserConversationIDProp, conv.ID)
	post.AddProp(AskUserToolUseIDProp, toolUseID)
	post.AddProp(AskUserSourcePostIDProp, sourcePostID)

	// The card send is irreversible; if the initiating request was canceled
	// while we validated, stop before DMing the target.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("dispatch canceled before sending the question card: %w", ctxErr)
	}

	if dmErr := c.mmClient.DM(bot.GetMMBot().UserId, target.Id, post); dmErr != nil {
		return fmt.Errorf("failed to open a direct message with %q", args.Username)
	}

	return nil
}

// newDeferredDispatcherForConversation builds the runner dispatcher for
// deferred-result tools in this conversation. anchorPostID may be empty on
// the initial-run path (the anchor post is not persisted as a turn yet);
// dispatch then falls back to conv.RootPostID for the card's permalink.
func (c *Conversations) newDeferredDispatcherForConversation(bot *bots.Bot, conv *store.Conversation, anchorPostID string) toolrunner.DeferredDispatcher {
	return func(ctx context.Context, call llm.ToolCall) error {
		if call.Name != mmtools.AskAnotherUserToolName {
			return fmt.Errorf("no deferred dispatch implemented for tool %s", call.Name)
		}
		return c.dispatchAskAnotherUser(ctx, bot, conv, anchorPostID, call.ID, call.Arguments)
	}
}

// publishConversationUpdated tells webapp clients to refetch the
// conversation. The webapp already listens for this event
// (custom_mattermost-ai_conversation_updated); this is its first publisher.
func (c *Conversations) publishConversationUpdated(convID, channelID string) {
	c.mmClient.PublishWebSocketEvent("conversation_updated",
		map[string]interface{}{"conversation_id": convID},
		&model.WebsocketBroadcast{ChannelId: channelID, ReliableClusterSend: true})
}

// HandleAskUserResponse processes the target user's answer (or decline) to an
// ask-another-user question card. It validates the caller is the asked
// target, resolves the answer into the C7 tool-result JSON, flips the waiting
// tool_use block, writes the tool_result turn, patches the card post, and —
// when no unresolved tool calls remain on the anchor turn — streams the
// follow-up LLM response in the original conversation.
//
// The card's DM channel (loaded by the HTTP middleware) is unused: target
// authorization runs against the card's ask_user_target_id prop instead.
func (c *Conversations) HandleAskUserResponse(ctx context.Context, userID string, cardPost *model.Post, _ *model.Channel, req AskUserResponse) error {
	if req.Action != AskUserActionAnswer && req.Action != AskUserActionDecline {
		return fmt.Errorf("%w: unknown action %q", ErrInvalidAskAnswer, req.Action)
	}
	if cardPost.Type != AskUserPostType {
		return fmt.Errorf("%w: post is not an ask-user card", ErrInvalidAskAnswer)
	}

	targetID, _ := cardPost.GetProp(AskUserTargetIDProp).(string)
	if targetID == "" || targetID != userID {
		return ErrNotAskTarget
	}

	bot := c.bots.GetBotByID(cardPost.UserId)
	if bot == nil {
		return fmt.Errorf("unable to get bot")
	}

	convID, _ := cardPost.GetProp(AskUserConversationIDProp).(string)
	if convID == "" {
		return fmt.Errorf("%w: card is missing its conversation reference", ErrInvalidAskAnswer)
	}
	toolUseID, _ := cardPost.GetProp(AskUserToolUseIDProp).(string)
	if toolUseID == "" {
		return fmt.Errorf("%w: card is missing its tool call reference", ErrInvalidAskAnswer)
	}

	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		if errors.Is(err, store.ErrConversationNotFound) {
			return ErrAskConversationGone
		}
		return fmt.Errorf("failed to get conversation: %w", err)
	}
	if conv.DeleteAt != 0 {
		return ErrAskConversationGone
	}

	turns, err := c.convService.GetTurns(convID)
	if err != nil {
		return fmt.Errorf("failed to get turns: %w", err)
	}

	turn, blocks, blockIdx := findToolUseBlock(turns, toolUseID)
	if turn == nil {
		// The waiting call vanished — e.g. superseded by a regenerate.
		return ErrAskConversationGone
	}
	block := &blocks[blockIdx]
	if block.Status != conversation.StatusWaiting {
		return ErrAskNotPending
	}

	// Chain into the originating run's trace when possible (mirrors
	// rehydrateRunTrace, which cannot be reused: the card post carries
	// ask_user_conversation_id rather than conversation_id).
	if turn.PostID != nil {
		if userTurn, turnErr := c.convService.GetInitiatingUserTurn(convID, *turn.PostID); turnErr == nil && userTurn != nil {
			ctx = telemetry.WithTurnID(ctx, userTurn.ID)
		}
	}
	ctx, span := telemetry.Tracer().Start(ctx, "handle ask user response",
		trace.WithNewRoot(),
		trace.WithAttributes(
			telemetry.PostID.String(cardPost.Id),
			telemetry.ToolID.String(toolUseID),
			telemetry.UserID.String(userID),
		),
	)
	defer span.End()

	// C7 result JSON needs the target's username; fall back to the asked
	// username from the original arguments if the lookup fails.
	targetUsername := ""
	if targetUser, userErr := c.mmClient.GetUser(userID); userErr == nil {
		targetUsername = targetUser.Username
	} else {
		var args mmtools.AskAnotherUserArgs
		if unmarshalErr := json.Unmarshal(block.Input, &args); unmarshalErr == nil {
			targetUsername = strings.TrimPrefix(args.Username, "@")
		}
	}

	declined := req.Action == AskUserActionDecline
	resultJSON, resolveErr := mmtools.ResolveAskAnotherUserAnswer(block.Input, targetUsername, mmtools.AskAnotherUserAnswer{
		Selected: req.Selected,
		FreeForm: req.FreeForm,
	}, declined)
	if resolveErr != nil {
		// State untouched: the question stays waiting and answerable.
		return fmt.Errorf("%w: %s", ErrInvalidAskAnswer, resolveErr.Error())
	}

	// Flip the block BEFORE side effects so a concurrent second submission
	// hits ErrAskNotPending instead of double-writing results (C5).
	if declined {
		block.Status = conversation.StatusRejected
	} else {
		block.Status = conversation.StatusSuccess
	}
	block.Shared = conversation.BoolPtr(true)
	if persistErr := c.persistBlocks(turn.ID, blocks); persistErr != nil {
		return fmt.Errorf("failed to persist answered status: %w", persistErr)
	}

	// The result turn is StatusSuccess for declines too: the decline marker
	// is a valid tool result the model must consume, not an error (C6).
	now := model.GetMillis()
	resultBlocks := []conversation.ContentBlock{{
		Type:      conversation.BlockTypeToolResult,
		ToolUseID: toolUseID,
		Content:   resultJSON,
		Status:    conversation.StatusSuccess,
		Shared:    conversation.BoolPtr(true),
		DecidedAt: conversation.Int64Ptr(now),
	}}
	resultContent, marshalErr := json.Marshal(resultBlocks)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal tool result blocks: %w", marshalErr)
	}
	resultTurn := &store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "tool_result",
		Content:        resultContent,
		CreatedAt:      now,
	}
	if createErr := c.convService.CreateTurnAutoSequence(resultTurn); createErr != nil {
		return fmt.Errorf("failed to create tool result turn: %w", createErr)
	}

	// Everything below is best-effort: the answer is recorded, so failures
	// are logged rather than surfaced as request errors.
	c.patchAskUserCard(cardPost.Id, declined, req)

	var anchorPost *model.Post
	if turn.PostID != nil {
		if p, postErr := c.mmClient.GetPost(*turn.PostID); postErr == nil {
			anchorPost = p
		} else {
			c.mmClient.LogError("Failed to get anchor post for ask-user follow-up", "error", postErr, "post_id", *turn.PostID)
		}
	}

	anchorChannelID := ""
	switch {
	case anchorPost != nil:
		anchorChannelID = anchorPost.ChannelId
	case conv.ChannelID != nil && *conv.ChannelID != "":
		anchorChannelID = *conv.ChannelID
	}
	if anchorChannelID != "" {
		c.publishConversationUpdated(convID, anchorChannelID)
	}

	// C3 resume invariant: never stream while any pending, accepted, or
	// waiting tool_use remains on the anchor turn (mixed batches resume via
	// HandleToolCall or a later answer).
	if hasUnresolvedToolUse(blocks) {
		return nil
	}

	// Channel mixed batches stage executed tool output behind the requester's
	// Share/Keep-Private decision. While any of the anchor turn's results is
	// still undecided, the resume belongs to HandleToolResult — streaming now
	// would demote the anchor turn and orphan the pending share click. (DM
	// results are always decided at creation, so this only gates channels.)
	if anchorHasUndecidedResults(turns, blocks) {
		return nil
	}

	if anchorPost == nil {
		c.mmClient.LogError("Cannot stream ask-user follow-up without the anchor post", "conversation_id", convID)
		return nil
	}
	anchorChannel, channelErr := c.mmClient.GetChannel(anchorPost.ChannelId)
	if channelErr != nil {
		c.mmClient.LogError("Failed to get anchor channel for ask-user follow-up", "error", channelErr, "channel_id", anchorPost.ChannelId)
		return nil
	}
	initiator, initiatorErr := c.mmClient.GetUser(conv.UserID)
	if initiatorErr != nil {
		c.mmClient.LogError("Failed to get initiating user for ask-user follow-up", "error", initiatorErr, "user_id", conv.UserID)
		return nil
	}

	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, anchorChannel)
	if followErr := c.streamToolFollowUp(ctx, bot, initiator, anchorChannel, anchorPost, conv, isDM, nil); followErr != nil {
		c.mmClient.LogError("Failed to stream ask-user follow-up", "error", followErr, "conversation_id", convID)
	}

	return nil
}

// patchAskUserCard updates the card post's props to reflect the recorded
// answer. Best-effort: failures are logged, never surfaced.
func (c *Conversations) patchAskUserCard(cardPostID string, declined bool, req AskUserResponse) {
	patched, getErr := c.mmClient.GetPost(cardPostID)
	if getErr != nil {
		c.mmClient.LogError("Failed to get ask-user card post for patching", "error", getErr, "post_id", cardPostID)
		return
	}

	status := AskUserStatusAnswered
	preview := ""
	if declined {
		status = AskUserStatusDeclined
	} else {
		preview = askUserAnswerPreview(req.Selected, req.FreeForm)
	}
	patched.AddProp(AskUserStatusProp, status)
	patched.AddProp(AskUserAnsweredAtProp, model.GetMillis())
	patched.AddProp(AskUserAnswerPreviewProp, preview)

	if updateErr := c.mmClient.UpdatePost(patched); updateErr != nil {
		c.mmClient.LogError("Failed to patch ask-user card post", "error", updateErr, "post_id", cardPostID)
	}
}

// askUserAnswerPreview renders the card's short answer summary: selected
// labels joined with ", ", then " — " and the free-form text when both are
// present, truncated to askUserAnswerPreviewMaxLen runes.
func askUserAnswerPreview(selected []string, freeForm string) string {
	preview := strings.Join(selected, ", ")
	freeForm = strings.TrimSpace(freeForm)
	if freeForm != "" {
		if preview != "" {
			preview += " — " + freeForm
		} else {
			preview = freeForm
		}
	}
	runes := []rune(preview)
	if len(runes) > askUserAnswerPreviewMaxLen {
		return string(runes[:askUserAnswerPreviewMaxLen])
	}
	return preview
}

// anchorHasUndecidedResults reports whether any tool_result belonging to one
// of the anchor turn's tool_use blocks still awaits its channel
// Share/Keep-Private decision (DecidedAt unset). turns may be a snapshot
// taken before this request's own tool_result turn was written — that result
// is created already decided, so its absence never gates.
func anchorHasUndecidedResults(turns []store.Turn, anchorBlocks []conversation.ContentBlock) bool {
	anchorToolUseIDs := make(map[string]struct{}, len(anchorBlocks))
	for _, b := range anchorBlocks {
		if b.Type == conversation.BlockTypeToolUse && b.ID != "" {
			anchorToolUseIDs[b.ID] = struct{}{}
		}
	}
	for _, turn := range turns {
		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turn.Content, &blocks); err != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != conversation.BlockTypeToolResult {
				continue
			}
			if _, ok := anchorToolUseIDs[b.ToolUseID]; !ok {
				continue
			}
			if b.DecidedAt == nil {
				return true
			}
		}
	}
	return false
}

// findToolUseBlock locates the assistant turn containing the tool_use block
// with the given ID, returning the turn, its unmarshaled blocks, and the
// block's index. Returns a nil turn when no assistant turn carries the block.
func findToolUseBlock(turns []store.Turn, toolUseID string) (*store.Turn, []conversation.ContentBlock, int) {
	for i := range turns {
		if turns[i].Role != "assistant" {
			continue
		}
		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turns[i].Content, &blocks); err != nil {
			continue
		}
		for j := range blocks {
			if blocks[j].Type == conversation.BlockTypeToolUse && blocks[j].ID == toolUseID {
				return &turns[i], blocks, j
			}
		}
	}
	return nil, nil, -1
}
