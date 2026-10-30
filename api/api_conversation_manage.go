// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

// MaxConversationTitleLength bounds a user-set conversation title, in runes.
// Generated titles are capped at 25 tokens, far below this.
const MaxConversationTitleLength = 256

// renameConversationMaxRequestBodyBytes bounds the rename body: a title of
// MaxConversationTitleLength 4-byte runes plus JSON framing and escapes.
const renameConversationMaxRequestBodyBytes = 8 * 1024

var errConversationNotFound = errors.New("conversation not found")

type renameConversationRequest struct {
	Title string `json:"title"`
}

// loadOwnedConversation loads the conversation named in the route and
// requires that the requesting user owns it. A conversation owned by someone
// else answers 404, exactly like a missing one, so the route never confirms
// that another user's conversation exists. Store errors answer 500.
func (a *API) loadOwnedConversation(c *gin.Context) (*store.Conversation, bool) {
	userID := c.GetHeader("Mattermost-User-Id")
	conversationID := c.Param("conversationid")
	audit.AddParam(auditRec(c), audit.KeyConversationID, audit.TruncateID(conversationID))

	conv, err := a.conversationStore.GetConversation(conversationID)
	if err != nil {
		if errors.Is(err, store.ErrConversationNotFound) {
			c.AbortWithError(http.StatusNotFound, errConversationNotFound)
			return nil, false
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get conversation: %w", err))
		return nil, false
	}
	if conv.UserID != userID {
		c.AbortWithError(http.StatusNotFound, errConversationNotFound)
		return nil, false
	}
	audit.AddParam(auditRec(c), audit.KeyAgentID, conv.BotID)
	return conv, true
}

// handleRenameConversation sets the title of one of the caller's
// conversations. Deleted conversations cannot be renamed.
func (a *API) handleRenameConversation(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, renameConversationMaxRequestBodyBytes)
	var req renameConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		c.AbortWithError(http.StatusBadRequest, errors.New("title must not be empty"))
		return
	}
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) > MaxConversationTitleLength {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("title must be valid text of at most %d characters", MaxConversationTitleLength))
		return
	}

	conv, ok := a.loadOwnedConversation(c)
	if !ok {
		return
	}

	renamed, err := a.conversationStore.RenameConversation(conv.ID, title)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to rename conversation: %w", err))
		return
	}
	if !renamed {
		// Deleted between the lookup and the write.
		c.AbortWithError(http.StatusNotFound, errConversationNotFound)
		return
	}

	c.JSON(http.StatusOK, struct{}{})
}
