// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/genui"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// GenerativeUISubmissionProp is set on the user post a generative UI
// submission creates. It records which reply, block, and form the post
// answers, and the submitted values, so the webapp can show the form as
// submitted. Only this plugin sets it on a post that also carries
// from_plugin, which regular clients cannot forge.
const GenerativeUISubmissionProp = "ai_genui_submission"

// ErrStaleUIAction is returned when a generated form can no longer be
// submitted: the reply is still streaming or waiting on tool calls, the
// conversation has moved on, or the reply was already submitted. The HTTP
// layer maps it to 409.
var ErrStaleUIAction = errors.New("this generated form is no longer active")

// ErrGenerativeUIDisabled is returned when a submission arrives while
// generative UI is off for the reply's agent, server-wide or for that
// agent. The HTTP layer maps it to 403.
var ErrGenerativeUIDisabled = errors.New("generative UI is disabled")

// uiActionClaimTTL only has to cover the window between claiming a reply and
// persisting the submission's user turn; after that, the latest-reply rule
// rejects any further submission permanently.
const uiActionClaimTTL = 10 * time.Minute

// UIActionRequest is a validated request to submit a generated form.
type UIActionRequest struct {
	Submission     genui.Submission
	IdempotencyKey string
}

// UIActionResult identifies the user post a submission created. PostID is
// empty when the request repeated an accepted submission's idempotency key.
type UIActionResult struct {
	PostID string
}

type uiSubmissionRecord struct {
	SourcePostID string `json:"source_post_id"`
	genui.Submission
}

// HandleUIAction submits a form, or clicks a button, in the generative UI of
// an agent reply. It checks that the clicker is the conversation requester and
// the reply is the conversation's latest, finished turn, claims the reply for
// a single submission, posts the submission in the thread as the clicker, and
// runs a turn on that post exactly as if the user had typed it. A click never
// runs a tool by itself: the follow-up turn has the same tools and approvals
// as a typed message.
func (c *Conversations) HandleUIAction(ctx context.Context, userID string, post *model.Post, channel *model.Channel, req UIActionRequest) (*UIActionResult, error) {
	ctx = c.rehydrateRunTrace(ctx, post)
	ctx, span := telemetry.Tracer().Start(ctx, "handle ui action",
		trace.WithNewRoot(),
		trace.WithAttributes(telemetry.PostID.String(post.Id)),
	)
	defer span.End()

	result, err := c.handleUIAction(ctx, userID, post, channel, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "ui action failed")
	}
	return result, err
}

func (c *Conversations) handleUIAction(ctx context.Context, userID string, post *model.Post, channel *model.Channel, req UIActionRequest) (*UIActionResult, error) {
	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		return nil, errors.New("unable to get bot")
	}
	if c.configProvider == nil || !c.configProvider.GenerativeUIEnabledForAgent(bot.GetConfig()) {
		return nil, ErrGenerativeUIDisabled
	}
	if c.convService == nil {
		return nil, errors.New("conversation service not configured")
	}

	convID, _ := post.GetProp(streaming.ConversationIDProp).(string)
	if convID == "" {
		return nil, ErrPostMissingConversationID
	}
	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	if conv.UserID != userID {
		return nil, ErrNotRequester
	}
	if post.UserId != conv.BotID {
		return nil, errors.New("post was not authored by the conversation's bot")
	}

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return nil, fmt.Errorf("unable to get user: %w", err)
	}
	// The turn re-resolves its policy; resolving it here first keeps a
	// request the turn would refuse from leaving a post behind.
	if _, err = c.resolveTurnPolicy(ctx, turnRequest{bot: bot, user: user, channel: channel, conv: conv, post: post}); err != nil {
		return nil, err
	}

	// A retry of an accepted submission finds the reply no longer latest, so
	// it is recognized by its idempotency key before that check.
	claimKey := uiActionClaimKey(conv.ID, post.Id)
	accepted, err := c.isAcceptedUIAction(claimKey, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if accepted {
		return &UIActionResult{}, nil
	}

	turns, err := c.convService.GetTurns(convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get turns: %w", err)
	}
	replyText, err := latestReplyText(turns, post.Id)
	if err != nil {
		return nil, err
	}

	submission, err := req.Submission.Normalize()
	if err != nil {
		return nil, err
	}
	blocks := genui.ClosedBlocks(replyText)
	if submission.Block >= len(blocks) {
		return nil, fmt.Errorf("%w: the reply has no block %d", genui.ErrInvalidSubmission, submission.Block)
	}
	if err = submission.MatchProgram(blocks[submission.Block]); err != nil {
		return nil, err
	}

	claimed, err := c.claimUIAction(claimKey, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return &UIActionResult{}, nil
	}

	rootID := post.RootId
	if rootID == "" {
		rootID = post.Id
	}
	submissionPost := &model.Post{
		UserId:    userID,
		ChannelId: channel.Id,
		RootId:    rootID,
		Message:   format.GenerativeUISubmission(submission),
	}
	record, err := submissionRecordProp(post.Id, submission)
	if err != nil {
		c.releaseUIAction(claimKey, req.IdempotencyKey)
		return nil, err
	}
	submissionPost.AddProp(GenerativeUISubmissionProp, record)
	if err = c.mmClient.CreatePost(submissionPost); err != nil {
		c.releaseUIAction(claimKey, req.IdempotencyKey)
		return nil, fmt.Errorf("failed to create submission post: %w", err)
	}

	if runErr := c.runUIActionTurn(ctx, bot, submissionPost, user, channel); runErr != nil {
		c.rollBackUIAction(conv.ID, submissionPost, claimKey, req.IdempotencyKey)
		return nil, runErr
	}
	return &UIActionResult{PostID: submissionPost.Id}, nil
}

// runUIActionTurn answers the submission post through the entry point a typed
// message would take: the DM flow in a DM with the agent, the mention flow
// anywhere else.
func (c *Conversations) runUIActionTurn(ctx context.Context, bot *bots.Bot, post *model.Post, user *model.User, channel *model.Channel) error {
	if mmapi.IsDMWith(bot.GetMMBot().UserId, channel) {
		return c.handleDMs(ctx, bot, channel, user, post)
	}
	return c.handleMentions(ctx, bot, post, user, channel)
}

// latestReplyText returns the text of the reply anchored to postID when that
// reply is the conversation's latest turn and is finished: persisted (so not
// streaming, on any node) and not waiting on tool decisions. Turns that
// precede the anchor in the same response (tool rounds) are included, the way
// the webapp renders them.
func latestReplyText(turns []store.Turn, postID string) (string, error) {
	sorted := slices.Clone(turns)
	slices.SortFunc(sorted, func(a, b store.Turn) int { return a.Sequence - b.Sequence })

	anchor := slices.IndexFunc(sorted, func(t store.Turn) bool {
		return t.Role == "assistant" && t.PostID != nil && *t.PostID == postID
	})
	if anchor == -1 {
		return "", fmt.Errorf("%w: the reply is not finished", ErrStaleUIAction)
	}
	if anchor != len(sorted)-1 {
		return "", fmt.Errorf("%w: the conversation has moved on", ErrStaleUIAction)
	}

	start := anchor
	for start > 0 {
		prev := sorted[start-1]
		if prev.Role == "user" || (prev.PostID != nil && *prev.PostID != postID) {
			break
		}
		start--
	}

	var text strings.Builder
	for i := start; i <= anchor; i++ {
		if sorted[i].Role != "assistant" {
			continue
		}
		blocks, err := conversation.UnmarshalBlocks(sorted[i].Content)
		if err != nil {
			return "", fmt.Errorf("failed to unmarshal turn %s content: %w", sorted[i].ID, err)
		}
		for _, b := range blocks {
			if i == anchor && b.Type == conversation.BlockTypeToolUse && b.Status == conversation.StatusPending {
				return "", fmt.Errorf("%w: the reply is waiting on tool calls", ErrStaleUIAction)
			}
			if b.Type == conversation.BlockTypeText {
				text.WriteString(b.Text)
			}
		}
	}
	return text.String(), nil
}

// uiActionClaimKey is per conversation and reply: a reply takes one
// submission, since the submission's turn makes it no longer the latest.
func uiActionClaimKey(convID, postID string) string {
	sum := sha256.Sum256([]byte(convID + ":" + postID))
	return "genui_submit_" + hex.EncodeToString(sum[:16])
}

// claimUIAction claims the reply for one submission. A retry carrying the
// idempotency key of the accepted submission reports false with no error, so
// the caller answers it as a no-op success; any other second claim is stale.
func (c *Conversations) claimUIAction(key, idempotencyKey string) (bool, error) {
	claimed, err := c.mmClient.KVCompareAndSetWithExpiry(key, nil, []byte(idempotencyKey), uiActionClaimTTL)
	if err != nil {
		return false, fmt.Errorf("failed to claim the reply: %w", err)
	}
	if claimed {
		return true, nil
	}
	accepted, err := c.isAcceptedUIAction(key, idempotencyKey)
	if err != nil || accepted {
		return false, err
	}
	return false, fmt.Errorf("%w: the reply was already submitted", ErrStaleUIAction)
}

func (c *Conversations) isAcceptedUIAction(key, idempotencyKey string) (bool, error) {
	var holder []byte
	if err := c.mmClient.KVGet(key, &holder); err != nil {
		if mmapi.IsKVNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read the reply claim: %w", err)
	}
	return string(holder) == idempotencyKey, nil
}

func (c *Conversations) releaseUIAction(key, idempotencyKey string) {
	if _, err := c.mmClient.KVCompareAndSet(key, []byte(idempotencyKey), nil); err != nil {
		c.mmClient.LogError("Failed to release generative UI submission claim", "error", err.Error())
	}
}

// rollBackUIAction undoes a submission whose turn failed before its user turn
// was persisted, so the user can submit again. Once the user turn exists the
// submission stands: the reply is no longer the latest, and the failed
// response says so in the thread.
func (c *Conversations) rollBackUIAction(convID string, submissionPost *model.Post, claimKey, idempotencyKey string) {
	turns, err := c.convService.GetTurns(convID)
	if err != nil {
		c.mmClient.LogError("Failed to check generative UI submission turn", "error", err.Error())
		return
	}
	persisted := slices.ContainsFunc(turns, func(t store.Turn) bool {
		return t.Role == "user" && t.PostID != nil && *t.PostID == submissionPost.Id
	})
	if persisted {
		return
	}
	if err := c.mmClient.DeletePost(submissionPost.Id); err != nil {
		c.mmClient.LogError("Failed to delete generative UI submission post", "post_id", submissionPost.Id, "error", err.Error())
	}
	c.releaseUIAction(claimKey, idempotencyKey)
}

func submissionRecordProp(sourcePostID string, submission genui.Submission) (map[string]any, error) {
	raw, err := json.Marshal(uiSubmissionRecord{SourcePostID: sourcePostID, Submission: submission})
	if err != nil {
		return nil, fmt.Errorf("failed to encode submission: %w", err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("failed to encode submission: %w", err)
	}
	return record, nil
}

// isGenerativeUISubmission reports whether post is a submission this plugin
// created for the posting user. Its from_plugin marker only says the plugin
// created it; the user is present and acting, as for a typed message.
func isGenerativeUISubmission(post *model.Post) bool {
	return post != nil && post.GetProp(FromPluginProp) != nil && post.GetProp(GenerativeUISubmissionProp) != nil
}
