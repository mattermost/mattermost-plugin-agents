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
	"github.com/mattermost/mattermost/server/public/pluginapi"
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
	anchoredPostText := a.anchoredPostTextLookup(conv.ChannelID)
	turnResponses, err := turnsToResponse(turns, userID != conv.UserID, anchoredPostText)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to sanitize turns: %w", err))
		return
	}

	// 5. Build response
	c.JSON(http.StatusOK, ConversationResponse{
		ID:         conv.ID,
		UserID:     conv.UserID,
		BotID:      conv.BotID,
		ChannelID:  conv.ChannelID,
		RootPostID: conv.RootPostID,
		Title:      conv.Title,
		Operation:  conv.Operation,
		Turns:      turnResponses,
	})
}

// approvalStateForTurn computes the approval-stage string for a post-anchor
// assistant turn, or "" for any turn that is not a post anchor.
func approvalStateForTurn(turn store.Turn, allTurns []store.Turn) string {
	if turn.Role != "assistant" || turn.PostID == nil {
		return ""
	}
	return conversation.ComputePostApprovalState(allTurns, *turn.PostID)
}

// anchoredPostTextLookup returns a lookup for the current message of a post a
// turn is anchored to, resolving each post ID at most once per request. ok is
// true only for a live post in channelID, the channel the conversation the
// turn belongs to is bound to.
func (a *API) anchoredPostTextLookup(channelID *string) func(postID string) (message string, ok bool) {
	type anchoredPost struct {
		message string
		ok      bool
	}
	resolved := make(map[string]anchoredPost)

	return func(postID string) (string, bool) {
		if cached, seen := resolved[postID]; seen {
			return cached.message, cached.ok
		}

		var anchor anchoredPost
		post, err := a.pluginAPI.Post.GetPost(postID)
		switch {
		case err != nil:
			if !errors.Is(err, pluginapi.ErrNotFound) {
				a.pluginAPI.Log.Warn("Failed to read the post a conversation turn is anchored to",
					"error", err, "post_id", postID)
			}
		case post != nil && post.DeleteAt == 0 && channelID != nil && post.ChannelId == *channelID:
			anchor = anchoredPost{message: post.Message, ok: true}
		}

		resolved[postID] = anchor
		return anchor.message, anchor.ok
	}
}

// turnsToResponse converts store turns to response objects with display
// sanitization, first applying privacy filtering when the requesting user is
// not the conversation owner. For such a request, the text of a turn anchored
// to a post is the message that post currently carries, which anchoredPostText
// resolves; a turn holding no text of its own keeps none, and a turn already
// holding that message is carried over as stored.
func turnsToResponse(
	turns []store.Turn,
	filterForNonRequester bool,
	anchoredPostText func(postID string) (string, bool),
) ([]TurnResponse, error) {
	result := make([]TurnResponse, len(turns))
	for i, turn := range turns {
		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turn.Content, &blocks); err != nil {
			return nil, fmt.Errorf("failed to unmarshal turn content: %w", err)
		}
		if filterForNonRequester {
			blocks = conversation.FilterForNonRequester(blocks)
			if stored := conversation.TextContent(blocks); turn.PostID != nil && stored != "" {
				message, ok := anchoredPostText(*turn.PostID)
				if !ok {
					message = ""
				}
				if message != stored {
					blocks = conversation.WithTextContent(blocks, message)
				}
			}
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
