// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
	// Status is the turn lifecycle state: "streaming" while a response is
	// being generated, then "complete" or "interrupted".
	Status string `json:"status"`
	// RunPostID is the response post whose run wrote a tool-round turn. While
	// that response streams, its anchor sits before these turns, so the
	// webapp matches them by this ID.
	RunPostID *string `json:"run_post_id,omitempty"`
}

// handleGetConversation returns a conversation and its turns with privacy filtering applied.
func (a *API) handleGetConversation(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	conversationID := c.Param("conversationid")

	// 1. Load conversation. Archiving keeps the Mattermost posts, which
	// render from their conversation, so archived conversations are served
	// under the same authorization as active ones.
	conv, err := a.getReadableConversation(conversationID)
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
	turnResponses, openingTextChanged, err := turnsToResponse(turns, userID != conv.UserID, a.agentUsername(conv.BotID), anchoredPostText)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to sanitize turns: %w", err))
		return
	}

	// The title is generated from the opening turn's text, so it goes with
	// that text when the post no longer reads as the turn stored it.
	title := conv.Title
	if openingTextChanged {
		title = ""
	}

	// 5. Build response
	resp := ConversationResponse{
		ID:         conv.ID,
		UserID:     conv.UserID,
		BotID:      conv.BotID,
		ChannelID:  conv.ChannelID,
		RootPostID: conv.RootPostID,
		Title:      title,
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

// getReadableConversation loads a conversation by ID, archived or not, for a
// route that serves or acts on it. A conversation whose root post was deleted
// answers store.ErrConversationNotFound (see conversation.CheckRootPost); a
// failed post lookup is returned as an error, never treated as readable.
// Callers still authorize the requesting user.
func (a *API) getReadableConversation(id string) (*store.Conversation, error) {
	conv, err := a.conversationStore.GetConversationIncludingArchived(id)
	if err != nil {
		return nil, err
	}
	if err := a.checkRootPost(conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// checkRootPost maps a deleted root post to store.ErrConversationNotFound so
// routes answer it exactly like a missing conversation.
func (a *API) checkRootPost(conv *store.Conversation) error {
	err := conversation.CheckRootPost(&a.pluginAPI.Post, conv)
	if errors.Is(err, conversation.ErrRootPostDeleted) {
		return fmt.Errorf("%w: %w", store.ErrConversationNotFound, err)
	}
	return err
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
// turn belongs to is bound to. A lookup failing for any reason other than the
// post not existing is returned as an error.
func (a *API) anchoredPostTextLookup(channelID *string) func(postID string) (message string, ok bool, err error) {
	type anchoredPost struct {
		message string
		ok      bool
	}
	resolved := make(map[string]anchoredPost)

	return func(postID string) (string, bool, error) {
		if cached, seen := resolved[postID]; seen {
			return cached.message, cached.ok, nil
		}

		var anchor anchoredPost
		post, err := a.pluginAPI.Post.GetPost(postID)
		switch {
		case err != nil:
			if !errors.Is(err, pluginapi.ErrNotFound) {
				return "", false, fmt.Errorf("failed to read the post a conversation turn is anchored to: %w", err)
			}
		case post != nil && post.DeleteAt == 0 && channelID != nil && post.ChannelId == *channelID:
			anchor = anchoredPost{message: post.Message, ok: true}
		}

		resolved[postID] = anchor
		return anchor.message, anchor.ok, nil
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
	agentUsername string,
	anchoredPostText func(postID string) (string, bool, error),
) ([]TurnResponse, bool, error) {
	result := make([]TurnResponse, len(turns))
	openingTextChanged := false
	for i, turn := range turns {
		var blocks []conversation.ContentBlock
		if err := json.Unmarshal(turn.Content, &blocks); err != nil {
			return nil, false, fmt.Errorf("failed to unmarshal turn content: %w", err)
		}
		if filterForNonRequester {
			blocks = conversation.FilterForNonRequester(blocks)
			if stored := conversation.TextContent(blocks); turn.PostID != nil && stored != "" {
				message, ok, err := anchoredPostText(*turn.PostID)
				if err != nil {
					return nil, false, err
				}
				if !ok {
					message = ""
				}
				if message != stored {
					blocks = conversation.WithTextContent(blocks, message)
					if i == 0 && (!ok || strings.TrimSpace(message) != strings.TrimSpace(withoutLeadingMention(stored, agentUsername))) {
						openingTextChanged = true
					}
				}
			}
		}
		sanitized := conversation.SanitizeForDisplay(blocks)
		sanitizedJSON, err := json.Marshal(sanitized)
		if err != nil {
			return nil, false, fmt.Errorf("failed to marshal sanitized content: %w", err)
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
			Status:        turn.Status,
			RunPostID:     turn.RunPostID,
		}
	}
	return result, openingTextChanged, nil
}

// withoutLeadingMention drops the "@agent " the plugin puts in front of the
// text of a turn it answers on its own (auto-reply, loop-in), where agent is
// agentUsername. A bare mention, stored for a post with no text of its own,
// leaves nothing. Any other text is returned as it is.
func withoutLeadingMention(text, agentUsername string) string {
	if agentUsername == "" {
		return text
	}
	mention := "@" + agentUsername
	if text == mention {
		return ""
	}
	if rest, found := strings.CutPrefix(text, mention+" "); found {
		return rest
	}
	return text
}

// agentUsername returns the username of the agent botID names, or "" when
// there is none, in which case no mention counts as the plugin's own.
func (a *API) agentUsername(botID string) string {
	bot := a.bots.GetBotByID(botID)
	if bot == nil || bot.GetMMBot() == nil {
		return ""
	}
	return bot.GetMMBot().Username
}
