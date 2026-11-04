// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// ConversationResponse is the JSON shape returned by GET /conversations/{id}.
type ConversationResponse struct {
	ID         string         `json:"id"`
	UserID     string         `json:"user_id"`
	BotID      string         `json:"bot_id"`
	ChannelID  *string        `json:"channel_id"`
	RootPostID *string        `json:"root_post_id"`
	Title      string         `json:"title"`
	Operation  string         `json:"operation"`
	Turns      []TurnResponse `json:"turns"`

	// ActiveWait is set only while a wait_for_async_work pause is pending in
	// this conversation's thread, so the webapp can render a live countdown.
	ActiveWait *ActiveWaitResponse `json:"active_wait,omitempty"`
}

// ActiveWaitResponse describes a pending wait_for_async_work pause. Times are
// unix millis. RequesterUserID is the only user allowed to check-now or cancel
// the wait; the webapp also uses it to pick the owner vs onlooker rendering.
type ActiveWaitResponse struct {
	PostID          string `json:"post_id"`
	RequesterUserID string `json:"requester_user_id"`
	ScheduledAt     int64  `json:"scheduled_at"`
	ResumeAt        int64  `json:"resume_at"`
	DurationMinutes int    `json:"duration_minutes"`
}

// TurnResponse is the JSON shape for a single turn within a conversation response.
type TurnResponse struct {
	ID        string          `json:"id"`
	PostID    *string         `json:"post_id"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	TokensIn  int64           `json:"tokens_in"`
	TokensOut int64           `json:"tokens_out"`
	Sequence  int             `json:"sequence"`
	// ApprovalState is set only on post-anchor assistant turns (those with
	// a non-nil PostID). One of "call" | "result" | "done". Computed by the
	// server so the webapp renders approval UI from a single source of truth.
	ApprovalState string `json:"approval_state,omitempty"`
}

// handleGetConversation returns a conversation and its turns with privacy filtering applied.
func (a *API) handleGetConversation(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	conversationID := c.Param("conversationid")

	// 1. Load conversation
	conv, err := a.conversationStore.GetConversation(conversationID)
	if err != nil {
		if errors.Is(err, store.ErrConversationNotFound) {
			c.AbortWithError(http.StatusNotFound, fmt.Errorf("conversation not found"))
			return
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get conversation: %w", err))
		return
	}

	// 2. Authorization: channel membership check
	if conv.ChannelID != nil {
		if !a.pluginAPI.User.HasPermissionToChannel(userID, *conv.ChannelID, model.PermissionReadChannel) {
			c.AbortWithError(http.StatusForbidden, fmt.Errorf("user doesn't have permission to this conversation"))
			return
		}
	} else {
		// Threadless conversation: only the owner can access
		if userID != conv.UserID {
			c.AbortWithError(http.StatusForbidden, fmt.Errorf("user doesn't have permission to this conversation"))
			return
		}
	}

	// 3. Load turns
	turns, err := a.conversationStore.GetTurnsForConversation(conv.ID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get turns: %w", err))
		return
	}

	// 4. Privacy filtering and display sanitization
	turnResponses, err := turnsToResponse(turns, userID != conv.UserID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to sanitize turns: %w", err))
		return
	}

	// 5. Build response
	resp := ConversationResponse{
		ID:         conv.ID,
		UserID:     conv.UserID,
		BotID:      conv.BotID,
		ChannelID:  conv.ChannelID,
		RootPostID: conv.RootPostID,
		Title:      conv.Title,
		Operation:  conv.Operation,
		Turns:      turnResponses,
	}

	// A pending wait_for_async_work pause is surfaced so the webapp can show a
	// live countdown. It is best-effort: the conversation still renders if the
	// lookup fails.
	if a.conversationsService != nil && conv.RootPostID != nil {
		wait, waitErr := a.conversationsService.ActiveWaitForConversation(conv.ID, *conv.RootPostID, conv.BotID)
		if waitErr != nil {
			a.pluginAPI.Log.Warn("failed to load active wait for conversation", "error", waitErr.Error())
		} else if wait != nil {
			resp.ActiveWait = &ActiveWaitResponse{
				PostID:          wait.ResponsePostID,
				RequesterUserID: wait.RequesterUserID,
				ScheduledAt:     wait.ScheduledAt,
				ResumeAt:        wait.ResumeAt,
				DurationMinutes: wait.DurationMinutes,
			}
		}
	}

	c.JSON(http.StatusOK, resp)
}

// approvalStateForTurn computes the approval-stage string for a post-anchor
// assistant turn, or "" for any turn that is not a post anchor.
func approvalStateForTurn(turn store.Turn, allTurns []store.Turn) string {
	if turn.Role != "assistant" || turn.PostID == nil {
		return ""
	}
	return conversation.ComputePostApprovalState(allTurns, *turn.PostID)
}

// turnsToResponse converts store turns to response objects with display
// sanitization, first applying privacy filtering when the requesting user is
// not the conversation owner.
func turnsToResponse(turns []store.Turn, filterForNonRequester bool) ([]TurnResponse, error) {
	result := make([]TurnResponse, len(turns))
	for i, turn := range turns {
		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turn.Content, &blocks); err != nil {
			return nil, fmt.Errorf("failed to unmarshal turn content: %w", err)
		}
		if filterForNonRequester {
			blocks = conversation.FilterForNonRequester(blocks)
		}
		sanitized := conversation.SanitizeForDisplay(blocks)
		sanitizedJSON, err := json.Marshal(sanitized)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal sanitized content: %w", err)
		}
		result[i] = TurnResponse{
			ID:            turn.ID,
			PostID:        turn.PostID,
			Role:          turn.Role,
			Content:       sanitizedJSON,
			TokensIn:      turn.TokensIn,
			TokensOut:     turn.TokensOut,
			Sequence:      turn.Sequence,
			ApprovalState: approvalStateForTurn(turn, turns),
		}
	}
	return result, nil
}
