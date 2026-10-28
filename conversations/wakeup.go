// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// WakeJobKeyPrefix namespaces wait_for_async_work jobs in the shared
// cluster.JobOnceScheduler.
const WakeJobKeyPrefix = "wait_wake_"

// maxConsecutiveWakes bounds a wait chain the requester has not re-engaged
// with; the model-facing stop instruction in WakeUserMessage is advisory only.
const maxConsecutiveWakes = 20

// resumeLockKeyPrefix names the per-conversation cluster mutex that
// serializes wake resumes, so a wake scheduled by a still-running resume waits
// for it.
const resumeLockKeyPrefix = "wait_resume_"

// resumeLockMaxWait bounds how long a wake waits behind a running resume.
const resumeLockMaxWait = time.Hour

// lockResume blocks until it holds the conversation's resume mutex and returns
// the unlock func.
func (c *Conversations) lockResume(conversationID string) (func(), error) {
	mtx, err := cluster.NewMutex(c.mutexAPI, resumeLockKeyPrefix+conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to create resume mutex: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), resumeLockMaxWait)
	defer cancel()
	if err := mtx.LockWithContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to lock resume mutex: %w", err)
	}
	return mtx.Unlock, nil
}

// wakeMarkerKeyPrefix names the per-(thread, bot) KV marker of the pending
// wake. The marker is the single source of truth for "a wait is pending in
// this thread for this bot": ScheduleWake claims it, steering and firing
// wakes consume it.
const wakeMarkerKeyPrefix = "wait_thread_"

// wakeMarkerTTL outlives the longest wait (30 minutes) plus the time a fired
// wake may queue on the resume mutex (resumeLockMaxWait). A stale marker
// self-heals: any new live invocation in the thread consumes it before the
// model can call wait_for_async_work again.
const wakeMarkerTTL = 2 * time.Hour

// wakeMarker identifies one scheduled wake: its owning conversation and when
// it was scheduled. A wake only runs while the marker still holds its identity.
//
// The display fields (ResponsePostID, RequesterUserID, ResumeAt,
// DurationMinutes) let the webapp render a live "waiting" indicator and the
// requester's controls without a second store. They carry omitempty so a
// marker written before this change still compares equal under the
// create-if-absent / compare-and-delete flow that gates every wake.
type wakeMarker struct {
	ConversationID string `json:"conversation_id"`
	ScheduledAt    int64  `json:"scheduled_at"`

	// ResponsePostID is the bot response post the wait is shown on.
	ResponsePostID string `json:"response_post_id,omitempty"`
	// RequesterUserID is the user whose turn scheduled the wait; only they may
	// check-now or cancel it.
	RequesterUserID string `json:"requester_user_id,omitempty"`
	// ResumeAt is when the bot is scheduled to resume, in unix millis.
	ResumeAt int64 `json:"resume_at,omitempty"`
	// DurationMinutes is the clamped wait length the bot was told.
	DurationMinutes int `json:"duration_minutes,omitempty"`
}

// threadRootID is the root of the thread post belongs to.
func threadRootID(post *model.Post) string {
	if post.RootId != "" {
		return post.RootId
	}
	return post.Id
}

// threadWakeMarkerKey keys the marker by the thread root and the bot: the
// root post is shared by every user's conversation in the thread.
func threadWakeMarkerKey(rootPostID, botID string) string {
	return wakeMarkerKeyPrefix + rootPostID + "_" + botID
}

// responsePostMarkerKey derives the marker key from a bot response post.
func responsePostMarkerKey(post *model.Post) string {
	return threadWakeMarkerKey(threadRootID(post), post.UserId)
}

// WakeJob is the JobOnce props payload for a wait_for_async_work resume. All
// other conversation state is rederived from the response post at wake time.
type WakeJob struct {
	PostID string `json:"post_id"`
	// UserID is the user whose turn scheduled the wait; the wake runs as them.
	UserID string `json:"user_id"`
	// wakeMarker identifies the wake: the job and the thread marker carry the
	// same value.
	wakeMarker
}

// WakeScheduler is the subset of cluster.JobOnceScheduler used to schedule
// wakes.
type WakeScheduler interface {
	ScheduleOnce(key string, runAt time.Time, props any) (*cluster.JobOnce, error)
	Cancel(key string)
}

// SetWakeScheduler installs the scheduler backing ScheduleWake and the API
// used for the per-conversation resume mutex.
func (c *Conversations) SetWakeScheduler(s WakeScheduler, mutexAPI cluster.MutexPluginAPI) {
	c.wakeScheduler = s
	c.mutexAPI = mutexAPI
}

// ScheduleWake schedules the conversation that owns postID to resume as userID
// after wait. It first claims the thread's marker with create-if-absent, so at
// most one wait is pending per thread and bot across the cluster; the job is
// keyed by conversation so a steering consumer can cancel it from the marker.
// The scheduler deletes the job right after the callback returns, and the
// resuming wake consumes the marker first, so a resumed turn can schedule the
// next wait.
func (c *Conversations) ScheduleWake(postID, userID string, wait time.Duration) error {
	post, err := c.mmClient.GetPost(postID)
	if err != nil {
		return fmt.Errorf("failed to get response post: %w", err)
	}
	convID, _ := post.GetProp(streaming.ConversationIDProp).(string)
	if convID == "" {
		return fmt.Errorf("response post %s has no conversation", postID)
	}

	now := time.Now()
	job := WakeJob{PostID: postID, UserID: userID,
		ConversationID:  convID,
		ScheduledAt:     now.UnixMilli(),
		ResponsePostID:  postID,
		RequesterUserID: userID,
		ResumeAt:        now.Add(wait).UnixMilli(),
		DurationMinutes: int(wait / time.Minute)}
	markerKey := responsePostMarkerKey(post)
	claimed, err := c.mmClient.KVCompareAndSetWithExpiry(markerKey, nil, job.wakeMarker, wakeMarkerTTL)
	if err != nil {
		return fmt.Errorf("failed to record pending wake: %w", err)
	}
	if !claimed {
		return mmtools.ErrWaitAlreadyScheduled
	}

	if _, err := c.wakeScheduler.ScheduleOnce(WakeJobKeyPrefix+convID, now.Add(wait), job); err != nil {
		if _, releaseErr := c.consumeWakeMarker(markerKey, job.wakeMarker); releaseErr != nil {
			c.mmClient.LogWarn("wait_for_async_work: failed to release pending wake marker", "error", releaseErr, "key", markerKey)
		}
		return fmt.Errorf("failed to schedule wake: %w", err)
	}
	return nil
}

// consumeWakeMarker deletes the marker if it still holds expected. The
// compare-and-set decides every race over a wake (steering, firing,
// regeneration): exactly one caller consumes it.
func (c *Conversations) consumeWakeMarker(key string, expected wakeMarker) (bool, error) {
	return c.mmClient.KVCompareAndSet(key, expected, nil)
}

// cancelThreadWake cancels the thread's pending wake, whichever user's
// conversation scheduled it, and reports whether there was one. channelID
// scopes the resolve notification; pass "" when the caller has no channel.
func (c *Conversations) cancelThreadWake(markerKey, channelID string) bool {
	if c.wakeScheduler == nil {
		return false
	}
	var marker wakeMarker
	if err := c.mmClient.KVGet(markerKey, &marker); err != nil {
		if !mmapi.IsKVNotFound(err) {
			c.mmClient.LogWarn("wait_for_async_work: failed to read pending wake", "error", err, "key", markerKey)
		}
		return false
	}
	consumed, err := c.consumeWakeMarker(markerKey, marker)
	if err != nil {
		c.mmClient.LogWarn("wait_for_async_work: failed to clear pending wake", "error", err, "key", markerKey)
	}
	if !consumed {
		return false
	}
	c.wakeScheduler.Cancel(WakeJobKeyPrefix + marker.ConversationID)
	c.notifyConversationUpdated(marker.ConversationID, channelID, marker.RequesterUserID)
	return true
}

// WebsocketEventConversationUpdated tells the webapp to refetch a conversation
// (received there as custom_mattermost-ai_conversation_updated). Emitted when a
// pending wait resolves so the "waiting" footer clears from server state rather
// than from the client's countdown alone.
const WebsocketEventConversationUpdated = "conversation_updated"

// notifyConversationUpdated asks clients in the conversation's channel to
// refetch it. The payload carries only an id, but the broadcast is scoped to
// the channel so the resolve timing is not disclosed outside it; it falls back
// to the requester when no channel is known, and never broadcasts unscoped.
func (c *Conversations) notifyConversationUpdated(conversationID, channelID, fallbackUserID string) {
	if conversationID == "" {
		return
	}
	broadcast := &model.WebsocketBroadcast{}
	switch {
	case channelID != "":
		broadcast.ChannelId = channelID
	case fallbackUserID != "":
		broadcast.UserId = fallbackUserID
	default:
		return
	}
	c.mmClient.PublishWebSocketEvent(
		WebsocketEventConversationUpdated,
		map[string]any{"conversation_id": conversationID},
		broadcast,
	)
}

// ErrNoPendingWait is returned by a wait control when the post has no pending
// wait: it was never scheduled, already fired, or already canceled.
var ErrNoPendingWait = errors.New("no pending wait for this post")

// ErrNotWaitOwner is returned when a user other than the wait's requester
// tries to check-now or cancel it.
var ErrNotWaitOwner = errors.New("only the wait requester can control it")

// WaitInfo is the pending-wait metadata surfaced to the webapp so it can show
// a live countdown and gate the requester's controls. It never carries the
// model-supplied reason, which is redacted from onlookers elsewhere.
type WaitInfo struct {
	ResponsePostID  string
	RequesterUserID string
	ScheduledAt     int64
	ResumeAt        int64
	DurationMinutes int
}

// ActiveWaitForConversation returns the pending wait the given conversation
// scheduled in its thread, or nil when none is pending. The marker is keyed by
// thread root and bot, so a wait another participant's conversation scheduled
// in the same thread is deliberately not reported here.
func (c *Conversations) ActiveWaitForConversation(conversationID, rootPostID, botID string) (*WaitInfo, error) {
	if c.wakeScheduler == nil || rootPostID == "" || botID == "" {
		return nil, nil
	}
	var marker wakeMarker
	if err := c.mmClient.KVGet(threadWakeMarkerKey(rootPostID, botID), &marker); err != nil {
		if mmapi.IsKVNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read pending wake: %w", err)
	}
	if marker.ConversationID != conversationID || marker.ResponsePostID == "" {
		return nil, nil
	}
	return &WaitInfo{
		ResponsePostID:  marker.ResponsePostID,
		RequesterUserID: marker.RequesterUserID,
		ScheduledAt:     marker.ScheduledAt,
		ResumeAt:        marker.ResumeAt,
		DurationMinutes: marker.DurationMinutes,
	}, nil
}

// pendingWaitForPost reads the pending wake marker for a bot response post, or
// ErrNoPendingWait when none is pending.
func (c *Conversations) pendingWaitForPost(post *model.Post) (wakeMarker, error) {
	if c.wakeScheduler == nil {
		return wakeMarker{}, ErrNoPendingWait
	}
	var marker wakeMarker
	if err := c.mmClient.KVGet(responsePostMarkerKey(post), &marker); err != nil {
		if mmapi.IsKVNotFound(err) {
			return wakeMarker{}, ErrNoPendingWait
		}
		return wakeMarker{}, fmt.Errorf("failed to read pending wake: %w", err)
	}
	if marker.ConversationID == "" {
		return wakeMarker{}, ErrNoPendingWait
	}
	return marker, nil
}

// HandleWaitCheckNow ends the post's pending wait early and resumes the bot
// immediately on behalf of userID, who must be the wait's requester. The
// scheduled trigger is canceled first so it cannot also fire; the marker's
// compare-and-delete then lets exactly one of the two resumes proceed.
func (c *Conversations) HandleWaitCheckNow(userID string, post *model.Post) error {
	marker, err := c.pendingWaitForPost(post)
	if err != nil {
		return err
	}
	if marker.RequesterUserID != userID {
		return ErrNotWaitOwner
	}
	c.wakeScheduler.Cancel(WakeJobKeyPrefix + marker.ConversationID)
	responsePostID := marker.ResponsePostID
	if responsePostID == "" {
		responsePostID = post.Id
	}
	job := WakeJob{PostID: responsePostID, UserID: marker.RequesterUserID, wakeMarker: marker}
	resume := c.resumeWake
	if resume == nil {
		// runWake locks the conversation's resume mutex and runs a full turn,
		// so it must not block the request, mirroring HandleWakeJob.
		resume = func(j WakeJob) { go c.runWake(j) }
	}
	resume(job)
	return nil
}

// HandleWaitCancel cancels the post's pending wait on behalf of userID, who
// must be the wait's requester; the bot does not resume from it.
func (c *Conversations) HandleWaitCancel(userID string, post *model.Post) error {
	marker, err := c.pendingWaitForPost(post)
	if err != nil {
		return err
	}
	if marker.RequesterUserID != userID {
		return ErrNotWaitOwner
	}
	if !c.cancelThreadWake(responsePostMarkerKey(post), post.ChannelId) {
		return ErrNoPendingWait
	}
	return nil
}

// getOrCreateConversation wraps convService.GetOrCreateConversation for live
// turns. A new message in a thread steers away the thread's pending wake, even
// one owned by another user's conversation, and the invoking conversation
// records why so the model understands the wait is gone. Best-effort: the
// message must still be handled if recording fails.
func (c *Conversations) getOrCreateConversation(params conversation.GetOrCreateParams) (*conversation.GetOrCreateResult, error) {
	result, err := c.convService.GetOrCreateConversation(params)
	if err != nil {
		return nil, err
	}
	if c.cancelThreadWake(threadWakeMarkerKey(params.RootPostID, params.BotID), params.ChannelID) {
		if err := c.convService.AppendSyntheticUserTurn(result.Conversation.ID, conversation.SyntheticKindWakeCanceled, mmtools.WakeCanceledUserMessage); err != nil {
			c.mmClient.LogError("wait_for_async_work: failed to record canceled wait", "error", err, "conversation_id", result.Conversation.ID)
		}
	}
	return result, nil
}

// HandleWakeJob is the cluster.JobOnceScheduler callback. Props scheduled
// before a restart arrive as generic JSON, so normalize via re-marshal.
func (c *Conversations) HandleWakeJob(key string, props any) {
	if !strings.HasPrefix(key, WakeJobKeyPrefix) {
		return
	}

	job, err := wakeJobFromProps(props)
	if err != nil {
		c.mmClient.LogError("wait_for_async_work: invalid wake job props", "error", err, "key", key)
		return
	}

	// The scheduler serializes callbacks under a process-wide mutex; resume
	// runs a full LLM turn (potentially minutes), so it must not block other
	// wakes from firing.
	go c.runWake(job)
}

// runWake resumes the conversation once it holds the conversation's resume
// mutex.
func (c *Conversations) runWake(job WakeJob) {
	unlock, err := c.lockResume(job.ConversationID)
	if err != nil {
		c.mmClient.LogError("wait_for_async_work: dropping wake", "error", err, "conversation_id", job.ConversationID)
		return
	}
	defer unlock()
	if err := c.ResumeConversation(context.Background(), job); err != nil {
		c.mmClient.LogError("wait_for_async_work: wake failed", "error", err, "post_id", job.PostID)
	}
}

func wakeJobFromProps(props any) (WakeJob, error) {
	raw, err := json.Marshal(props)
	if err != nil {
		return WakeJob{}, fmt.Errorf("failed to marshal wake job props: %w", err)
	}
	var job WakeJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return WakeJob{}, fmt.Errorf("failed to unmarshal wake job props: %w", err)
	}
	if job.PostID == "" || job.ConversationID == "" || job.UserID == "" || job.ScheduledAt == 0 {
		return WakeJob{}, fmt.Errorf("wake job props missing post_id, conversation_id, user_id, or scheduled_at")
	}
	return job, nil
}

// ResumeConversation injects a synthetic user turn for a wait_for_async_work
// wake and re-enters the tool follow-up loop on a new bot post in the same
// thread. Streaming onto the original post would blank and replace its
// finished response (continuation semantics), so the wake gets its own reply.
// Conversation state is rederived from the original response post, mirroring
// HandleToolCall; if any entity is gone the thread is dead and the wake is
// dropped.
func (c *Conversations) ResumeConversation(ctx context.Context, job WakeJob) (err error) {
	post, err := c.mmClient.GetPost(job.PostID)
	if err != nil {
		return c.dropIfMissing("post", err, "post_id", job.PostID)
	}

	// Chain into the originating run's trace, same as HandleToolCall / regen.
	// The scheduler callback has no request ctx; Background is independent of
	// the expired tool-call request. rehydrateRunTrace is best-effort.
	ctx = c.rehydrateRunTrace(ctx, post)
	ctx, span := telemetry.Tracer().Start(ctx, "resume conversation after wait",
		trace.WithNewRoot(),
		trace.WithAttributes(telemetry.PostID.String(job.PostID)),
	)
	defer span.End()
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to resume conversation")
		}
	}()

	// A missing marker means a new message steered the thread (or a newer wait
	// replaced this one) after the wake fired.
	consumed, err := c.consumeWakeMarker(responsePostMarkerKey(post), job.wakeMarker)
	if err != nil {
		return fmt.Errorf("failed to consume pending wake: %w", err)
	}
	if !consumed {
		return c.dropWake("steered or superseded", nil, "conversation_id", job.ConversationID)
	}
	// The wait is resolving; tell clients to drop the "waiting" footer now
	// rather than waiting for the countdown to run out.
	c.notifyConversationUpdated(job.ConversationID, post.ChannelId, job.RequesterUserID)

	conv, err := c.convService.GetConversation(job.ConversationID)
	if err != nil {
		return c.dropIfMissing("conversation", err, "conversation_id", job.ConversationID)
	}

	turns, err := c.convService.GetTurns(conv.ID)
	if err != nil {
		return fmt.Errorf("failed to get turns: %w", err)
	}

	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		return c.dropWake("bot missing", nil, "bot_id", post.UserId)
	}

	user, err := c.mmClient.GetUser(job.UserID)
	if err != nil {
		return c.dropIfMissing("user", err, "user_id", job.UserID)
	}

	channel, err := c.mmClient.GetChannel(post.ChannelId)
	if err != nil {
		return c.dropIfMissing("channel", err, "channel_id", post.ChannelId)
	}

	// The wake runs unattended with plugin authority, so re-check that the
	// requester could still start this turn themselves.
	if user.DeleteAt != 0 || !c.mmClient.HasPermissionToChannel(user.Id, channel.Id, model.PermissionReadChannel) {
		return c.dropWake("user no longer authorized", nil, "user_id", user.Id, "channel_id", channel.Id)
	}

	policy, err := c.resolveTurnPolicy(ctx, turnRequest{bot: bot, user: user, channel: channel, conv: conv, post: post})
	_, isLicenseErr := errors.AsType[*enterprise.LicenseError](err)
	switch {
	case errors.Is(err, bots.ErrUsageRestriction), errors.Is(err, ErrAnalysisSourceUnavailable), isLicenseErr:
		return c.dropWake("turn not allowed", err, "user_id", user.Id, "bot_id", bot.GetMMBot().UserId, "channel_id", channel.Id)
	case err != nil:
		return fmt.Errorf("failed to resolve turn policy: %w", err)
	}

	if wakes := countConsecutiveWakes(turns); wakes >= maxConsecutiveWakes {
		return c.dropWake("chain limit reached", nil, "conversation_id", conv.ID, "wakes", wakes)
	}

	wakePost := &model.Post{
		ChannelId: post.ChannelId,
		RootId:    threadRootID(post),
	}
	wakePost.AddProp(streaming.ConversationIDProp, conv.ID)
	if err := c.createResponsePlaceholder(bot.GetMMBot().UserId, user.Id, wakePost, post.Id); err != nil {
		return fmt.Errorf("failed to create wake response post: %w", err)
	}

	if err := c.convService.AppendSyntheticUserTurn(conv.ID, conversation.SyntheticKindWake, mmtools.WakeUserMessage); err != nil {
		c.failResponsePlaceholder(wakePost, user.Locale)
		return err
	}

	if err := c.streamToolFollowUp(ctx, policy, wakePost, conv, followUpOptions{Unattended: true}); err != nil {
		c.failResponsePlaceholder(wakePost, user.Locale)
		return err
	}
	return nil
}

// dropIfMissing drops the wake when err says the entity is gone. Any other
// failure may be transient, so it is returned for runWake to log as an error.
func (c *Conversations) dropIfMissing(entity string, err error, keyValues ...any) error {
	appErr, isAppErr := errors.AsType[*model.AppError](err)
	if errors.Is(err, store.ErrConversationNotFound) || isAppErr && appErr.StatusCode == http.StatusNotFound {
		return c.dropWake(entity+" missing", err, keyValues...)
	}
	return fmt.Errorf("failed to get %s: %w", entity, err)
}

// dropWake logs why a wake is abandoned and reports it as handled: the thread
// is dead or the requester lost access, so retrying cannot help.
func (c *Conversations) dropWake(reason string, cause error, keyValues ...any) error {
	if cause != nil {
		keyValues = append(keyValues, "error", cause)
	}
	c.mmClient.LogDebug("wait_for_async_work: dropping wake, "+reason, keyValues...)
	return nil
}

// countConsecutiveWakes counts wake turns since the requester's latest real
// (post-linked) user turn.
func countConsecutiveWakes(turns []store.Turn) int {
	count := 0
	for _, turn := range slices.Backward(turns) {
		if turn.Role != "user" {
			continue
		}
		if turn.PostID != nil {
			break
		}
		blocks, err := conversation.UnmarshalBlocks(turn.Content)
		if err == nil && len(blocks) == 1 && blocks[0].SyntheticKind == conversation.SyntheticKindWake {
			count++
		}
	}
	return count
}
