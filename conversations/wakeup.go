// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// WakeJobKeyPrefix namespaces wait_for_async_work jobs in the shared
// cluster.JobOnceScheduler.
const WakeJobKeyPrefix = "wait_wake_"

// WakeJob is the JobOnce props payload for a wait_for_async_work resume. All
// other conversation state is rederived from the response post at wake time.
type WakeJob struct {
	PostID string `json:"post_id"`
	Reason string `json:"reason"`
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
	go func() {
		if err := c.ResumeConversation(context.Background(), job); err != nil {
			c.mmClient.LogError("wait_for_async_work: wake failed", "error", err, "post_id", job.PostID)
		}
	}()
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
	if job.PostID == "" {
		return WakeJob{}, fmt.Errorf("wake job props missing post_id")
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
func (c *Conversations) ResumeConversation(ctx context.Context, job WakeJob) error {
	post, err := c.mmClient.GetPost(job.PostID)
	if err != nil {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, post missing",
			"error", err, "post_id", job.PostID)
		return nil
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

	convID, ok := post.GetProp(streaming.ConversationIDProp).(string)
	if !ok || convID == "" {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, post has no conversation",
			"post_id", job.PostID)
		return nil
	}

	conv, err := c.convService.GetConversation(convID)
	if err != nil {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, conversation missing",
			"error", err, "conversation_id", convID)
		return nil
	}

	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, bot missing", "bot_id", post.UserId)
		return nil
	}

	user, err := c.mmClient.GetUser(conv.UserID)
	if err != nil {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, user missing",
			"error", err, "user_id", conv.UserID)
		return nil
	}

	channel, err := c.mmClient.GetChannel(post.ChannelId)
	if err != nil {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, channel missing",
			"error", err, "channel_id", post.ChannelId)
		return nil
	}

	responseRootID := post.Id
	if post.RootId != "" {
		responseRootID = post.RootId
	}
	wakePost := &model.Post{
		ChannelId: post.ChannelId,
		RootId:    responseRootID,
	}
	if err := c.createResponsePlaceholder(bot.GetMMBot().UserId, conv.UserID, wakePost, post.Id); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create wake response post")
		return err
	}
	wakePost.AddProp(streaming.ConversationIDProp, conv.ID)
	if err := c.mmClient.UpdatePost(wakePost); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to attach conversation to wake response post")
		c.failResponsePlaceholder(wakePost, user.Locale)
		return fmt.Errorf("failed to attach conversation to wake response post: %w", err)
	}

	if err := c.convService.AppendSyntheticUserTurn(conv.ID, mmtools.WakeUserMessage(job.Reason)); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to write wake turn")
		c.failResponsePlaceholder(wakePost, user.Locale)
		return err
	}

	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)
	if err := c.streamToolFollowUp(ctx, bot, user, channel, wakePost, conv, isDM, nil); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resume conversation")
		c.failResponsePlaceholder(wakePost, user.Locale)
		return err
	}
	return nil
}
