// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/store"
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

	// 4. Privacy filtering
	var turnResponses []TurnResponse
	if userID != conv.UserID {
		turnResponses, err = filterTurnsForNonRequester(turns)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to filter turns: %w", err))
			return
		}
	} else {
		turnResponses = turnsToResponse(turns)
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

// filterTurnsForNonRequester applies privacy filtering to turn content for
// a user who is not the conversation owner.
func filterTurnsForNonRequester(turns []store.Turn) ([]TurnResponse, error) {
	result := make([]TurnResponse, len(turns))
	for i, turn := range turns {
		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turn.Content, &blocks); err != nil {
			return nil, fmt.Errorf("failed to unmarshal turn content: %w", err)
		}
		filtered := conversation.FilterForNonRequester(blocks)
		filteredJSON, err := json.Marshal(filtered)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal filtered content: %w", err)
		}
		result[i] = TurnResponse{
			ID:        turn.ID,
			PostID:    turn.PostID,
			Role:      turn.Role,
			Content:   filteredJSON,
			TokensIn:  turn.TokensIn,
			TokensOut: turn.TokensOut,
			Sequence:  turn.Sequence,
		}
	}
	return result, nil
}

// turnsToResponse converts store turns directly to response objects (no filtering).
func turnsToResponse(turns []store.Turn) []TurnResponse {
	result := make([]TurnResponse, len(turns))
	for i, turn := range turns {
		result[i] = TurnResponse{
			ID:        turn.ID,
			PostID:    turn.PostID,
			Role:      turn.Role,
			Content:   turn.Content,
			TokensIn:  turn.TokensIn,
			TokensOut: turn.TokensOut,
			Sequence:  turn.Sequence,
		}
	}
	return result
}
