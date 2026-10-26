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
	"github.com/mattermost/mattermost/server/public/model"
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
func (a *API) loadOwnedConversation(c *gin.Context, includeArchived bool) (*store.Conversation, bool) {
	userID := c.GetHeader("Mattermost-User-Id")
	conversationID := c.Param("conversationid")
	audit.AddParam(auditRec(c), audit.KeyConversationID, audit.TruncateID(conversationID))

	var conv *store.Conversation
	var err error
	if includeArchived {
		conv, err = a.conversationStore.GetConversationIncludingArchived(conversationID)
	} else {
		conv, err = a.conversationStore.GetConversation(conversationID)
	}
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

// handleRenameConversation sets the title of one of the caller's active
// conversations. Archived conversations cannot be renamed.
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

	conv, ok := a.loadOwnedConversation(c, false)
	if !ok {
		return
	}

	renamed, err := a.conversationStore.RenameConversation(conv.ID, title)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to rename conversation: %w", err))
		return
	}
	if !renamed {
		// Archived between the lookup and the write.
		c.AbortWithError(http.StatusNotFound, errConversationNotFound)
		return
	}

	c.JSON(http.StatusOK, struct{}{})
}

// handleArchiveConversation hides one of the caller's conversations from their
// thread lists. Its turns and Mattermost posts are kept, and a later reply in
// the thread restores it. Archiving an archived conversation succeeds.
func (a *API) handleArchiveConversation(c *gin.Context) {
	conv, ok := a.loadOwnedConversation(c, true)
	if !ok {
		return
	}

	if conv.DeleteAt == 0 {
		// A false result means a concurrent archive won; either way it is archived.
		if _, err := a.conversationStore.ArchiveConversation(conv.ID, model.GetMillis()); err != nil {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to archive conversation: %w", err))
			return
		}
	}

	c.JSON(http.StatusOK, struct{}{})
}
