// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func bindConversationResume(llmContext *llm.Context, conversationID, postID string) {
	if llmContext == nil {
		return
	}
	llmContext.ConversationID = conversationID
	llmContext.PostID = postID
}

// ResumeConversation injects a synthetic user turn for a wait_for_async_work
// wake and re-enters the tool follow-up loop on the original bot post.
func (c *Conversations) ResumeConversation(ctx context.Context, rec mmtools.WakeRecord) error {
	ctx, span := telemetry.Tracer().Start(ctx, "resume conversation after wait",
		trace.WithAttributes(
			telemetry.UserID.String(rec.UserID),
			telemetry.ChannelID.String(rec.ChannelID),
			telemetry.PostID.String(rec.PostID),
			telemetry.AgentID.String(rec.BotID),
		),
	)
	defer span.End()

	if c == nil || c.convService == nil || c.bots == nil || c.mmClient == nil {
		return errors.New("conversations service is not configured")
	}

	conv, err := c.convService.GetConversation(rec.ConversationID)
	if err != nil {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, conversation missing",
			"error", err, "conversation_id", rec.ConversationID)
		return nil
	}

	bot := c.bots.GetBotByID(rec.BotID)
	if bot == nil {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, bot missing", "bot_id", rec.BotID)
		return nil
	}

	user, err := c.mmClient.GetUser(rec.UserID)
	if err != nil {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, user missing",
			"error", err, "user_id", rec.UserID)
		return nil
	}

	channel, err := c.mmClient.GetChannel(rec.ChannelID)
	if err != nil {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, channel missing",
			"error", err, "channel_id", rec.ChannelID)
		return nil
	}

	post, err := c.mmClient.GetPost(rec.PostID)
	if err != nil {
		c.mmClient.LogDebug("wait_for_async_work: dropping wake, post missing",
			"error", err, "post_id", rec.PostID)
		return nil
	}

	if err := c.writeWaitWakeTurn(conv.ID, rec.Reason); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to write wake turn")
		return err
	}

	if err := c.streamToolFollowUp(ctx, bot, user, channel, post, conv, rec.IsDM, nil); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resume conversation")
		return err
	}
	return nil
}

func (c *Conversations) writeWaitWakeTurn(conversationID, reason string) error {
	content, err := json.Marshal([]conversation.ContentBlock{{
		Type: conversation.BlockTypeText,
		Text: mmtools.WakeUserMessage(reason),
	}})
	if err != nil {
		return err
	}

	return c.convService.CreateTurnAutoSequence(&store.Turn{
		ID:             model.NewId(),
		ConversationID: conversationID,
		PostID:         nil,
		Role:           "user",
		Content:        content,
		CreatedAt:      model.GetMillis(),
	})
}
