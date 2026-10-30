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
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// MaxConversationTitleLength bounds a user-set conversation title, in runes.
// Generated titles are capped at 25 tokens, far below this.
const MaxConversationTitleLength = 256

// renameConversationMaxRequestBodyBytes bounds the rename body: a title of
// MaxConversationTitleLength 4-byte runes plus JSON framing and escapes.
const renameConversationMaxRequestBodyBytes = 8 * 1024

var (
	errConversationNotFound = errors.New("conversation not found")
	errConversationInternal = errors.New("internal error")
)

type renameConversationRequest struct {
	Title string `json:"title"`
}

// loadOwnedConversation loads the conversation named in the route, archived
// or not, and requires that the requesting user owns it. A conversation owned
// by someone else, or whose root post was deleted, answers 404 exactly like a
// missing one, so the route never confirms that it exists. Store errors answer
// 500 with a generic message; the detail is logged.
func (a *API) loadOwnedConversation(c *gin.Context) (*store.Conversation, bool) {
	userID := c.GetHeader("Mattermost-User-Id")
	conversationID := c.Param("conversationid")
	audit.AddParam(auditRec(c), audit.KeyConversationID, audit.TruncateID(conversationID))

	conv, err := a.getReadableConversation(conversationID)
	if err != nil {
		if errors.Is(err, store.ErrConversationNotFound) {
			c.AbortWithError(http.StatusNotFound, errConversationNotFound)
			return nil, false
		}
		a.pluginAPI.Log.Error("Failed to get conversation", "conversation_id", conversationID, "error", err.Error())
		c.AbortWithError(http.StatusInternalServerError, errConversationInternal)
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
// conversations. Archived conversations cannot be renamed: the store's
// conditional write reports them, like missing ones, as not renamed.
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
		a.pluginAPI.Log.Error("Failed to rename conversation", "conversation_id", conv.ID, "error", err.Error())
		c.AbortWithError(http.StatusInternalServerError, errConversationInternal)
		return
	}
	if !renamed {
		// Archived (before or after the lookup).
		c.AbortWithError(http.StatusNotFound, errConversationNotFound)
		return
	}

	conversation.PublishConversationTitleUpdated(a.mmClient, conv, title)

	c.JSON(http.StatusOK, struct{}{})
}

// handleArchiveConversation hides one of the caller's conversations from their
// thread lists. Its turns and Mattermost posts are kept, and a later reply in
// the thread restores it. Archiving is idempotent: the store's write is
// conditional on the row being active, so archiving an archived conversation
// (or racing another archive) succeeds without writing.
func (a *API) handleArchiveConversation(c *gin.Context) {
	conv, ok := a.loadOwnedConversation(c)
	if !ok {
		return
	}

	if err := a.conversationStore.ArchiveConversation(conv.ID, model.GetMillis()); err != nil {
		a.pluginAPI.Log.Error("Failed to archive conversation", "conversation_id", conv.ID, "error", err.Error())
		c.AbortWithError(http.StatusInternalServerError, errConversationInternal)
		return
	}

	conversation.PublishConversationArchived(a.mmClient, conv)

	c.JSON(http.StatusOK, struct{}{})
}
