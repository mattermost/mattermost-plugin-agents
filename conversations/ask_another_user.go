// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
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

// v2 card prop keys (V2-C2), all written at dispatch time and frozen at ask
// time — no re-computation later. Mirrored as literals in the webapp; every
// v2 prop is optional on the read side (pre-v2 cards render the v1 layout).
const (
	AskUserRequesterKindProp                 = "ask_user_requester_kind"
	AskUserRequesterUsernameProp             = "ask_user_requester_username"
	AskUserRequesterDisplayNameProp          = "ask_user_requester_display_name"
	AskUserRequesterPositionProp             = "ask_user_requester_position"
	AskUserAgentDisplayNameProp              = "ask_user_agent_display_name"
	AskUserDestinationTypeProp               = "ask_user_destination_type"
	AskUserDestinationChannelDisplayNameProp = "ask_user_destination_channel_display_name"
	AskUserDestinationMemberCountProp        = "ask_user_destination_member_count"
	AskUserDestinationPolicyEnforcedProp     = "ask_user_destination_policy_enforced"
)

// ask_user_requester_kind values (V2-C2).
const (
	AskUserRequesterKindUser    = "user"
	AskUserRequesterKindBot     = "bot"
	AskUserRequesterKindUnknown = "unknown"
)

// ask_user_destination_type values (V2-C2). GM gets its own type because the
// "~name" channel rendering is wrong for a group message's member-name list.
const (
	AskUserDestinationTypeDM      = "dm"
	AskUserDestinationTypeGM      = "gm"
	AskUserDestinationTypeChannel = "channel"
)

const (
	AskUserStatusPending  = "pending"
	AskUserStatusAnswered = "answered"
	AskUserStatusDeclined = "declined"
	// AskUserStatusCanceled is the v2 terminal card state written only by
	// HandleAskUserCancel: "This question is no longer needed." (V2-C6).
	AskUserStatusCanceled = "canceled"

	AskUserActionAnswer  = "answer"
	AskUserActionDecline = "decline"
)

// askCardKVPrefix keys the dispatch-time pointer from a tool_use id to the
// target's card post id, giving the cancel path its reverse lookup (V2-C4).
// KV is already the home of the ask claims; this is not a new store.
const askCardKVPrefix = "askcard_"

// askCardPointerTTL bounds card-pointer accumulation. A question outstanding
// longer than this can still be canceled — the block resolves, only the
// card patch degrades to the target's neutral 409 path.
const askCardPointerTTL = 30 * 24 * time.Hour

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

	// F1 master-switch backstop: pending blocks persisted before an admin
	// flipped the toggle off must resolve to an error result the model can
	// react to, instead of silently DMing a card from a disabled feature.
	// Fails closed on a nil provider.
	if c.configProvider == nil || !c.configProvider.EnableAskAnotherUser() {
		return errors.New("the AskAnotherUser feature is disabled by the administrator")
	}

	var args mmtools.AskAnotherUserArgs
	if unmarshalErr := json.Unmarshal(rawArgs, &args); unmarshalErr != nil {
		return fmt.Errorf("invalid AskAnotherUser arguments: %v", unmarshalErr)
	}

	if validateErr := mmtools.ValidateAskAnotherUserArgs(args); validateErr != nil {
		return validateErr
	}

	// Anti-impersonation strip/reject (V2-C3). The sanitized args feed BOTH
	// the card props and the plaintext fallback below — model text must
	// never be able to fake the card's system chrome.
	args, sanitizeErr := mmtools.SanitizeAskAnotherUserArgs(args)
	if sanitizeErr != nil {
		return sanitizeErr
	}

	target, lookupErr := c.mmClient.GetUserByUsername(mmtools.CanonicalAskUsername(args.Username))
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

	// Requester attribution is best-effort display data (C4/V2-C2): a human
	// requester is fully identified (kind "user" + username/display/position
	// props); an autonomous bot invoker is kind "bot"; a failed lookup is
	// kind "unknown". Only kind "user" carries the v1 requester_id prop —
	// bot and unknown stay consistently unattributed everywhere (webapp
	// props and fallback message) instead of half-attributed.
	requesterKind := AskUserRequesterKindUnknown
	requesterID := ""
	requesterUsername := ""
	requesterDisplayName := ""
	requesterPosition := ""
	if requester, requesterErr := c.mmClient.GetUser(conv.UserID); requesterErr == nil {
		if requester.IsBot {
			requesterKind = AskUserRequesterKindBot
		} else {
			requesterKind = AskUserRequesterKindUser
			requesterID = conv.UserID
			requesterUsername = requester.Username
			requesterDisplayName = requester.GetDisplayName(model.ShowFullName)
			if requesterDisplayName == requesterUsername {
				// The webapp skips duplicates anyway; keep the prop clean.
				requesterDisplayName = ""
			}
			requesterPosition = requester.Position
		}
	}

	// F4c: the unattended/unknown attribution variants name the agent.
	agentDisplayName := bot.GetMMBot().DisplayName
	if agentDisplayName == "" {
		agentDisplayName = bot.GetMMBot().Username
	}

	dest := c.resolveAskDestination(conv, anchorPostID)

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

	// The plaintext/mobile fallback carries the same attribution, question,
	// and destination disclosure as the webapp card (F-001/V2-C8), assembled
	// as attribution + question + destination [+ policy] + hint. The
	// attribution line comes FIRST so LLM-authored question text cannot
	// spoof it.
	message := c.buildAskUserCardFallback(target.Locale, args.Question, requesterKind, requesterUsername, agentDisplayName, dest)

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
	post.AddProp(AskUserRequesterKindProp, requesterKind)
	post.AddProp(AskUserRequesterUsernameProp, requesterUsername)
	post.AddProp(AskUserRequesterDisplayNameProp, requesterDisplayName)
	post.AddProp(AskUserRequesterPositionProp, requesterPosition)
	post.AddProp(AskUserAgentDisplayNameProp, agentDisplayName)
	post.AddProp(AskUserDestinationTypeProp, dest.Type)
	post.AddProp(AskUserDestinationChannelDisplayNameProp, dest.ChannelName)
	post.AddProp(AskUserDestinationMemberCountProp, dest.MemberCount)
	post.AddProp(AskUserDestinationPolicyEnforcedProp, dest.PolicyEnforced)

	// The card send is irreversible; if the initiating request was canceled
	// while we validated, stop before DMing the target.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("dispatch canceled before sending the question card: %w", ctxErr)
	}

	if dmErr := c.mmClient.DM(bot.GetMMBot().UserId, target.Id, post); dmErr != nil {
		return fmt.Errorf("failed to open a direct message with %q", args.Username)
	}

	// Reverse pointer for the cancel path (V2-C4). Never a dispatch
	// failure: a lost pointer degrades to "cancel resolves the block but
	// cannot patch the card".
	if kvErr := c.mmClient.KVSetWithExpiry(askCardKVPrefix+toolUseID, post.Id, askCardPointerTTL); kvErr != nil {
		c.mmClient.LogWarn("Failed to store ask-user card pointer",
			"tool_use_id", toolUseID,
			"post_id", post.Id,
			"error", kvErr.Error(),
		)
	}

	return nil
}

// askDestination is the dispatch-time snapshot of where the target's answer
// may end up (V2-C2). ChannelName and MemberCount are zero-valued for DMs
// and on lookup failures; MemberCount <= 0 means unknown.
type askDestination struct {
	Type           string
	ChannelName    string
	MemberCount    int64
	PolicyEnforced bool
}

// resolveAskDestination implements the V2-C2 destination-resolution
// algorithm. Disclosure must never UNDERSTATE the audience, so every lookup
// failure degrades toward the broader claim: the generic "channel"
// destination with no name and no count.
func (c *Conversations) resolveAskDestination(conv *store.Conversation, anchorPostID string) askDestination {
	channelID := ""
	if conv.ChannelID != nil {
		channelID = *conv.ChannelID
	}
	if channelID == "" && anchorPostID != "" {
		if anchorPost, postErr := c.mmClient.GetPost(anchorPostID); postErr == nil {
			channelID = anchorPost.ChannelId
		}
	}
	if channelID == "" && conv.RootPostID != nil && *conv.RootPostID != "" {
		if rootPost, postErr := c.mmClient.GetPost(*conv.RootPostID); postErr == nil {
			channelID = rootPost.ChannelId
		}
	}
	if channelID == "" {
		return askDestination{Type: AskUserDestinationTypeChannel}
	}

	channel, channelErr := c.mmClient.GetChannel(channelID)
	if channelErr != nil {
		return askDestination{Type: AskUserDestinationTypeChannel}
	}

	switch channel.Type {
	case model.ChannelTypeDirect:
		return askDestination{Type: AskUserDestinationTypeDM}
	case model.ChannelTypeGroup:
		return askDestination{
			Type:        AskUserDestinationTypeGM,
			ChannelName: channel.DisplayName,
			MemberCount: c.askDestinationMemberCount(channelID),
		}
	default:
		return askDestination{
			Type:           AskUserDestinationTypeChannel,
			ChannelName:    channel.DisplayName,
			MemberCount:    c.askDestinationMemberCount(channelID),
			PolicyEnforced: channel.PolicyEnforced,
		}
	}
}

// askDestinationMemberCount returns the channel's member count at dispatch
// time, or 0 (= unknown) when the stats read fails.
func (c *Conversations) askDestinationMemberCount(channelID string) int64 {
	stats, statsErr := c.mmClient.GetChannelStats(channelID)
	if statsErr != nil || stats == nil {
		return 0
	}
	return stats.MemberCount
}

// buildAskUserCardFallback assembles the card's plaintext/mobile fallback
// message from the V2-C8 composable parts:
// attribution + "\n\n" + question + "\n\n" + destination [+ "\n" + policy] +
// "\n\n" + hint. Attribution first, so model text can never precede it.
func (c *Conversations) buildAskUserCardFallback(targetLocale, question, requesterKind, requesterUsername, agentDisplayName string, dest askDestination) string {
	T := func(_ string, defaultMessage string, params ...any) string {
		if len(params) == 0 {
			return defaultMessage
		}
		return fmt.Sprintf(defaultMessage, params...)
	}
	if c.i18n != nil {
		T = i18n.LocalizerFunc(c.i18n, c.fallbackLocale(targetLocale))
	}

	var attribution string
	switch requesterKind {
	case AskUserRequesterKindUser:
		attribution = T("agents.ask_user_fallback_attrib_user", "Asked on behalf of @%s:", requesterUsername)
	case AskUserRequesterKindBot:
		attribution = T("agents.ask_user_fallback_attrib_bot", "Asked by the %s agent running unattended (no human requester):", agentDisplayName)
	default:
		attribution = T("agents.ask_user_fallback_attrib_unknown", "Asked via the %s agent (requester identity unavailable):", agentDisplayName)
	}

	var destination string
	switch dest.Type {
	case AskUserDestinationTypeDM:
		switch requesterKind {
		case AskUserRequesterKindUser:
			destination = T("agents.ask_user_fallback_dest_dm", "Your answer will be shared with @%s.", requesterUsername)
		case AskUserRequesterKindBot:
			destination = T("agents.ask_user_fallback_dest_dm_agent", "Your answer will be shared with the %s agent.", agentDisplayName)
		default:
			destination = T("agents.ask_user_fallback_dest_dm_unknown", "Your answer will be shared with the person who asked the agent.")
		}
	case AskUserDestinationTypeGM:
		if dest.MemberCount > 0 {
			destination = T("agents.ask_user_fallback_dest_gm", "Your answer may be shared with the %d members of a group message.", dest.MemberCount)
		} else {
			destination = T("agents.ask_user_fallback_dest_gm_no_count", "Your answer may be shared with the members of a group message.")
		}
	default:
		switch {
		case dest.ChannelName != "" && dest.MemberCount > 0:
			destination = T("agents.ask_user_fallback_dest_channel", "Your answer may be shared with the %[1]d members of ~%[2]s.", dest.MemberCount, dest.ChannelName)
		case dest.ChannelName != "":
			destination = T("agents.ask_user_fallback_dest_channel_no_count", "Your answer may be shared with the members of ~%s.", dest.ChannelName)
		default:
			destination = T("agents.ask_user_fallback_dest_channel_unknown", "Your answer may be shared in the channel where the agent was asked.")
		}
	}

	// The policy line requires a known channel name — the flag comes from
	// the same channel read (V2-C2). Unreachable access data is OMITTED,
	// never rendered as "no restrictions".
	if dest.PolicyEnforced && dest.ChannelName != "" {
		destination += "\n" + T("agents.ask_user_fallback_policy", "Access to ~%s is restricted by an attribute-based access policy.", dest.ChannelName)
	}

	hint := T("agents.ask_user_fallback_hint", "(Interactive answer card — open Mattermost in a browser or the desktop app to respond.)")

	return attribution + "\n\n" + question + "\n\n" + destination + "\n\n" + hint
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

// Ask claim stages. Each one-shot transition of a tool_use block gets its own
// claim key: the pending→waiting dispatch (HandleToolCall) and the
// waiting→resolved answer (HandleAskUserResponse) are independent
// transitions, so sharing one key would let the dispatch claim starve the
// later answer.
const (
	askClaimStageDispatch = "dispatch"
	askClaimStageAnswer   = "answer"
)

// claimAskToolUse atomically claims the given one-shot transition for a
// tool_use block via KV compare-and-set (write-if-absent), returning whether
// THIS caller won the claim. The plain read-check-write on turn content is
// not atomic, so two concurrent submissions (two tabs/devices, or two HA
// nodes) can both pass the status check; exactly one of them wins this claim.
// A KV error counts as a failed claim: it is logged here and returned so the
// caller can surface it instead of proceeding to a possible double-write.
func (c *Conversations) claimAskToolUse(stage, toolUseID string) (bool, error) {
	won, err := c.mmClient.KVCompareAndSet("askclaim_"+stage+"_"+toolUseID, nil, []byte("1"))
	if err != nil {
		c.mmClient.LogError("Failed to claim ask tool_use transition",
			"stage", stage,
			"tool_use_id", toolUseID,
			"error", err.Error(),
		)
		return false, err
	}
	return won, nil
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
// follow-up LLM response in the original conversation. It returns the
// terminal card status; when a durable cancel result already won, it returns
// canceled without writing, patching, or streaming again.
//
// The card's DM channel (loaded by the HTTP middleware) is unused: target
// authorization runs against the card's ask_user_target_id prop instead.
func (c *Conversations) HandleAskUserResponse(ctx context.Context, userID string, cardPost *model.Post, _ *model.Channel, req AskUserResponse) (string, error) {
	if req.Action != AskUserActionAnswer && req.Action != AskUserActionDecline {
		return "", fmt.Errorf("%w: unknown action %q", ErrInvalidAskAnswer, req.Action)
	}
	if cardPost.Type != AskUserPostType {
		return "", fmt.Errorf("%w: post is not an ask-user card", ErrInvalidAskAnswer)
	}

	targetID, _ := cardPost.GetProp(AskUserTargetIDProp).(string)
	if targetID == "" || targetID != userID {
		return "", ErrNotAskTarget
	}

	bot := c.bots.GetBotByID(cardPost.UserId)
	if bot == nil {
		return "", fmt.Errorf("unable to get bot")
	}

	convID, _ := cardPost.GetProp(AskUserConversationIDProp).(string)
	if convID == "" {
		return "", fmt.Errorf("%w: card is missing its conversation reference", ErrInvalidAskAnswer)
	}
	toolUseID, _ := cardPost.GetProp(AskUserToolUseIDProp).(string)
	if toolUseID == "" {
		return "", fmt.Errorf("%w: card is missing its tool call reference", ErrInvalidAskAnswer)
	}

	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		if errors.Is(err, store.ErrConversationNotFound) {
			return "", ErrAskConversationGone
		}
		return "", fmt.Errorf("failed to get conversation: %w", err)
	}
	if conv.DeleteAt != 0 {
		return "", ErrAskConversationGone
	}

	turns, err := c.convService.GetTurns(convID)
	if err != nil {
		return "", fmt.Errorf("failed to get turns: %w", err)
	}

	turn, blocks, blockIdx := findToolUseBlock(turns, toolUseID)
	if turn == nil {
		// The waiting call vanished — e.g. superseded by a regenerate.
		return "", ErrAskConversationGone
	}
	block := &blocks[blockIdx]
	if block.Status != conversation.StatusWaiting {
		if askUserResultWasCanceled(turns, toolUseID) {
			return AskUserStatusCanceled, nil
		}
		return "", ErrAskNotPending
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
		return "", fmt.Errorf("%w: %s", ErrInvalidAskAnswer, resolveErr.Error())
	}

	// Atomic claim: exactly one submission may resolve this question. The
	// waiting-status check above stays as the first-line guard; this CAS
	// closes the concurrent window in which two submissions both read the
	// block as waiting and double-write the result turn. The claim comes
	// AFTER answer validation so an invalid answer never burns it — the
	// question must stay answerable after a validation error.
	won, claimErr := c.claimAskToolUse(askClaimStageAnswer, toolUseID)
	if claimErr != nil {
		return "", fmt.Errorf("failed to claim the question: %w", claimErr)
	}
	if !won {
		// The shared claim only says another resolution won. Re-read the
		// durable result to distinguish a cancel (successful no-op for this
		// stale response) from a duplicate answer/decline (conflict).
		latestTurns, turnsErr := c.convService.GetTurns(convID)
		if turnsErr != nil {
			return "", fmt.Errorf("failed to inspect the resolved question: %w", turnsErr)
		}
		if askUserResultWasCanceled(latestTurns, toolUseID) {
			return AskUserStatusCanceled, nil
		}
		return "", ErrAskNotPending
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
		return "", fmt.Errorf("failed to persist answered status: %w", persistErr)
	}

	if writeErr := c.writeAskResultTurn(convID, toolUseID, resultJSON); writeErr != nil {
		return "", writeErr
	}

	// Everything below is best-effort: the answer is recorded, so failures
	// are logged rather than surfaced as request errors.
	c.patchAskUserCard(cardPost.Id, declined, req)

	c.resumeAfterAskResolution(ctx, bot, conv, turns, blocks, turn)

	if declined {
		return AskUserStatusDeclined, nil
	}
	return AskUserStatusAnswered, nil
}

// writeAskResultTurn persists the tool_result turn for a resolved
// ask-another-user question. The result turn is StatusSuccess for declines
// and cancels too: both are valid tool results the model must consume, not
// errors (C6, V2-C5). The result is an initiator-visible terminal decision,
// so it is created Shared with DecidedAt set — no Share/Keep-Private stage.
func (c *Conversations) writeAskResultTurn(convID, toolUseID, resultJSON string) error {
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
	return nil
}

// askUserResultWasCanceled checks the persisted tool result for the terminal
// resolution. A successful tool_use block alone is ambiguous because answers
// and cancels share that status and the same one-shot claim.
func askUserResultWasCanceled(turns []store.Turn, toolUseID string) bool {
	for _, turn := range turns {
		if turn.Role != "tool_result" {
			continue
		}
		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turn.Content, &blocks); err != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type != conversation.BlockTypeToolResult || block.ToolUseID != toolUseID {
				continue
			}
			var result struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal([]byte(block.Content), &result); err == nil && result.Status == AskUserStatusCanceled {
				return true
			}
		}
	}
	return false
}

// resumeAfterAskResolution is the shared tail of the answer and cancel
// paths: publish the conversation update, then stream the follow-up LLM
// response — but only when the resume invariant allows it. Best-effort by
// design: the resolution is already persisted, so failures here are logged,
// never surfaced. turns/blocks/turn are the caller's pre-resolution
// snapshot of the anchor turn.
func (c *Conversations) resumeAfterAskResolution(ctx context.Context, bot *bots.Bot, conv *store.Conversation, turns []store.Turn, blocks []conversation.ContentBlock, turn *store.Turn) {
	convID := conv.ID

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
		return
	}

	// Channel mixed batches stage executed tool output behind the requester's
	// Share/Keep-Private decision. While any of the anchor turn's results is
	// still undecided, the resume belongs to HandleToolResult — streaming now
	// would demote the anchor turn and orphan the pending share click. (DM
	// results are always decided at creation, so this only gates channels.)
	if anchorHasUndecidedResults(turns, blocks) {
		return
	}

	if anchorPost == nil {
		c.mmClient.LogError("Cannot stream ask-user follow-up without the anchor post", "conversation_id", convID)
		return
	}
	anchorChannel, channelErr := c.mmClient.GetChannel(anchorPost.ChannelId)
	if channelErr != nil {
		c.mmClient.LogError("Failed to get anchor channel for ask-user follow-up", "error", channelErr, "channel_id", anchorPost.ChannelId)
		return
	}
	initiator, initiatorErr := c.mmClient.GetUser(conv.UserID)
	if initiatorErr != nil {
		c.mmClient.LogError("Failed to get initiating user for ask-user follow-up", "error", initiatorErr, "user_id", conv.UserID)
		return
	}

	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, anchorChannel)
	if followErr := c.streamToolFollowUp(ctx, bot, initiator, anchorChannel, anchorPost, conv, isDM, nil); followErr != nil {
		c.mmClient.LogError("Failed to stream ask-user follow-up", "error", followErr, "conversation_id", convID)
	}
}

// HandleAskUserCancel processes the conversation initiator's cancellation of
// an outstanding ask-another-user question (V2-C4). It resolves the waiting
// tool_use with a valid non-error {"status":"canceled",...} tool result,
// patches the target's card to the canceled terminal state (best-effort via
// the askcard_ KV pointer written at dispatch), and resumes the conversation
// under the same gates as the answer path. Cancel contends for the SAME
// one-shot claim as answer/decline, so exactly one resolution ever wins; the
// cancel loser surfaces ErrAskNotPending (409), while a response that loses
// to a durable cancel result returns a successful canceled no-op.
//
// The clicked post is the initiator's anchor post; its channel (loaded by
// the HTTP middleware) is unused — the resume path re-reads it.
func (c *Conversations) HandleAskUserCancel(ctx context.Context, userID string, post *model.Post, _ *model.Channel, toolUseID string) error {
	convID, ok := post.GetProp(streaming.ConversationIDProp).(string)
	if !ok || convID == "" {
		return ErrPostMissingConversationID
	}

	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		return fmt.Errorf("unable to get bot")
	}

	// Enrich the request's audit record (nil outside an audited request)
	// with which agent's question this human decision cancels. Question
	// text, target identity, and result content never enter the record.
	audit.AddParam(audit.RecordFromContext(ctx), audit.KeyAgentID, bot.GetMMBot().UserId)

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
	// Only the conversation initiator can cancel; the API layer's
	// isConversationOwner gate is re-checked here so the invariant holds for
	// every caller of this method.
	if conv.UserID != userID {
		return ErrNotRequester
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
	// Only an AskAnotherUser deferred block still waiting on the clicked
	// anchor post is cancelable; anything else is already resolved or not
	// this endpoint's business (V2-C4).
	if turn.PostID == nil || *turn.PostID != post.Id ||
		block.Name != mmtools.AskAnotherUserToolName ||
		!block.DeferredResult ||
		block.Status != conversation.StatusWaiting {
		return ErrAskNotPending
	}

	// Chain into the originating run's trace when possible (mirrors the
	// answer path).
	if userTurn, turnErr := c.convService.GetInitiatingUserTurn(convID, post.Id); turnErr == nil && userTurn != nil {
		ctx = telemetry.WithTurnID(ctx, userTurn.ID)
	}
	ctx, span := telemetry.Tracer().Start(ctx, "handle ask user cancel",
		trace.WithNewRoot(),
		trace.WithAttributes(
			telemetry.PostID.String(post.Id),
			telemetry.ToolID.String(toolUseID),
			telemetry.UserID.String(userID),
		),
	)
	defer span.End()

	// Resolve BEFORE claiming, mirroring the answer path: the resolve is
	// side-effect-free, so a failure here must never burn the one-shot
	// claim and strand the block in a permanently-conflicting state.
	resultJSON, resolveErr := mmtools.ResolveAskAnotherUserCancel(block.Input)
	if resolveErr != nil {
		return fmt.Errorf("failed to build cancel result: %w", resolveErr)
	}

	// Atomic claim on the SAME key as answer/decline: exactly one of
	// {answer, decline, cancel} resolves this question. Losing means the
	// other resolution already happened.
	won, claimErr := c.claimAskToolUse(askClaimStageAnswer, toolUseID)
	if claimErr != nil {
		return fmt.Errorf("failed to claim the question: %w", claimErr)
	}
	if !won {
		return ErrAskNotPending
	}

	// Flip the block BEFORE side effects, mirroring the answer path. No new
	// content-block status (V2-C5): the canceled call completed with a
	// valid result — "canceled" lives in the result JSON and the card prop.
	block.Status = conversation.StatusSuccess
	block.Shared = conversation.BoolPtr(true)
	if persistErr := c.persistBlocks(turn.ID, blocks); persistErr != nil {
		return fmt.Errorf("failed to persist canceled status: %w", persistErr)
	}

	if writeErr := c.writeAskResultTurn(convID, toolUseID, resultJSON); writeErr != nil {
		return writeErr
	}

	// Everything below is best-effort: the cancel is recorded, so failures
	// are logged rather than surfaced as request errors.
	c.patchAskUserCardCanceled(toolUseID)

	c.resumeAfterAskResolution(ctx, bot, conv, turns, blocks, turn)

	return nil
}

// patchAskUserCardCanceled flips the target's question card to the canceled
// terminal state ("This question is no longer needed."), located via the
// dispatch-time KV pointer. Best-effort: failures are logged, never
// surfaced — the conversation-side resolution has already happened, and a
// stale card still resolves late target responses through the durable
// canceled tool result. The plain message is rewritten too so pre-v2 webapps
// and plaintext clients see the terminal state (V2-C2 back-compat rule).
func (c *Conversations) patchAskUserCardCanceled(toolUseID string) {
	var cardPostID string
	if kvErr := c.mmClient.KVGet(askCardKVPrefix+toolUseID, &cardPostID); kvErr != nil || cardPostID == "" {
		c.mmClient.LogWarn("No card pointer for canceled ask-user question; target card left unpatched",
			"tool_use_id", toolUseID,
		)
		return
	}

	patched, getErr := c.mmClient.GetPost(cardPostID)
	if getErr != nil {
		c.mmClient.LogError("Failed to get ask-user card post for cancel patching", "error", getErr, "post_id", cardPostID)
		return
	}

	// Localize for the target (best-effort lookup for their locale).
	locale := ""
	if targetID, _ := patched.GetProp(AskUserTargetIDProp).(string); targetID != "" {
		if target, targetErr := c.mmClient.GetUser(targetID); targetErr == nil {
			locale = target.Locale
		}
	}
	const canceledDefault = "This question is no longer needed."
	message := canceledDefault
	if c.i18n != nil {
		T := i18n.LocalizerFunc(c.i18n, c.fallbackLocale(locale))
		message = T("agents.ask_user_card_canceled", canceledDefault)
	}

	patched.AddProp(AskUserStatusProp, AskUserStatusCanceled)
	patched.Message = message
	if updateErr := c.mmClient.UpdatePost(patched); updateErr != nil {
		c.mmClient.LogError("Failed to patch ask-user card post to canceled", "error", updateErr, "post_id", cardPostID)
	}
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
