// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost/server/public/model"
)

// WebsocketEventChannelContextUpdated is the event name for PublishWebSocketEvent
// (webapp: custom_mattermost-ai_<name>). It fires when a channel's
// instructions or agent-context pins change.
const WebsocketEventChannelContextUpdated = "channel_context_updated"

// channelInstructionsMaxRequestBodyBytes caps the instructions PUT body: the
// stored text is at most channelcontext.MaxInstructionsRunes characters (up to
// 4 bytes each), plus room for JSON escaping.
const channelInstructionsMaxRequestBodyBytes = 64 << 10 // 64 KiB

// channelContextPinMaxRequestBodyBytes caps the pin POST body; the payload is one ID.
const channelContextPinMaxRequestBodyBytes = 1 << 10 // 1 KiB

// ChannelInstructions is the request and response payload for
// GET/PUT /channel/{channelid}/instructions.
type ChannelInstructions struct {
	Instructions string `json:"instructions"`
}

// ChannelContextPost is one post pinned to a channel's agent context.
type ChannelContextPost struct {
	PostID   string `json:"post_id"`
	UserID   string `json:"user_id"`
	RootID   string `json:"root_id"`
	Message  string `json:"message"`
	CreateAt int64  `json:"create_at"`
	PinnedBy string `json:"pinned_by"`
	PinnedAt int64  `json:"pinned_at"`
}

// ChannelContextPosts is the response payload for the
// /channel/{channelid}/context_posts routes.
type ChannelContextPosts struct {
	Posts    []ChannelContextPost `json:"posts"`
	MaxPosts int                  `json:"max_posts"`
}

type pinChannelContextPostRequest struct {
	PostID string `json:"post_id"`
}

// requireManageChannelProperties aborts the request unless userID holds the
// channel-management permission for the channel's type. DM and GM channels
// have no manageable agent settings and are rejected with 400.
func (a *API) requireManageChannelProperties(c *gin.Context, userID string, channel *model.Channel) bool {
	var perm *model.Permission
	switch channel.Type {
	case model.ChannelTypeOpen:
		perm = model.PermissionManagePublicChannelProperties
	case model.ChannelTypePrivate:
		perm = model.PermissionManagePrivateChannelProperties
	default: // ChannelTypeDirect, ChannelTypeGroup
		c.AbortWithError(http.StatusBadRequest,
			errors.New("agent channel settings cannot be configured for direct or group message channels"))
		return false
	}
	if !a.pluginAPI.User.HasPermissionToChannel(userID, channel.Id, perm) {
		c.AbortWithError(http.StatusForbidden, errors.New("user doesn't have permission to manage channel properties"))
		return false
	}
	return true
}

// handleGetChannelInstructions returns the channel's agent instructions.
// Readable by any channel member and never license-gated, so stored
// instructions stay visible and clearable on every plan.
func (a *API) handleGetChannelInstructions(c *gin.Context) {
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	instructions, err := a.channelContextStore.GetInstructions(channel.Id)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get channel instructions: %w", err))
		return
	}
	c.JSON(http.StatusOK, ChannelInstructions{Instructions: instructions})
}

// handlePutChannelInstructions stores the channel's agent instructions.
// Requires the channel-management permission. Setting instructions requires
// the channel context capability; clearing them never does.
func (a *API) handlePutChannelInstructions(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	audit.AddParam(auditRec(c), audit.KeyChannelID, channel.Id)

	if !a.requireManageChannelProperties(c, userID, channel) {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, channelInstructionsMaxRequestBodyBytes)
	var req ChannelInstructions
	if err := c.ShouldBindJSON(&req); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			c.AbortWithError(http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large: %w", err))
			return
		}
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if strings.TrimSpace(req.Instructions) != "" && !a.requireCapability(c, enterprise.CapChannelContext) {
		return
	}

	saved, err := a.channelContextStore.SetInstructions(channel, req.Instructions, userID)
	if err != nil {
		if errors.Is(err, channelcontext.ErrValidation) {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid channel instructions: %w", err))
			return
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save channel instructions: %w", err))
		return
	}

	a.publishChannelContextUpdated(channel.Id)

	c.JSON(http.StatusOK, ChannelInstructions{Instructions: saved})
}

// handleGetChannelContextPosts lists the posts pinned to the channel's agent
// context. Readable by any channel member and never license-gated.
func (a *API) handleGetChannelContextPosts(c *gin.Context) {
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	posts, err := a.channelContextPostsResponse(channel.Id)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, posts)
}

// handlePinChannelContextPost pins a post to the channel's agent context and
// returns the updated list. Requires the channel-management permission and
// the channel context capability. A full channel answers 409.
func (a *API) handlePinChannelContextPost(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	audit.AddParam(auditRec(c), audit.KeyChannelID, channel.Id)

	if !a.requireManageChannelProperties(c, userID, channel) {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, channelContextPinMaxRequestBodyBytes)
	var req pinChannelContextPostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			c.AbortWithError(http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large: %w", err))
			return
		}
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	audit.AddParam(auditRec(c), audit.KeyPostID, audit.TruncateID(req.PostID))

	if !a.requireCapability(c, enterprise.CapChannelContext) {
		return
	}

	if err := a.channelContextStore.PinPost(channel, req.PostID, userID); err != nil {
		switch {
		case errors.Is(err, channelcontext.ErrValidation):
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid post for agent context: %w", err))
		case errors.Is(err, channelcontext.ErrPinLimitReached):
			c.AbortWithError(http.StatusConflict, fmt.Errorf("a channel can pin at most %d posts to agent context: %w", channelcontext.MaxPinnedPosts, err))
		default:
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to pin post to agent context: %w", err))
		}
		return
	}

	a.publishChannelContextUpdated(channel.Id)
	a.respondWithChannelContextPosts(c, channel.Id)
}

// handleUnpinChannelContextPost removes a post from the channel's agent
// context and returns the updated list. Requires the channel-management
// permission; never license-gated, so stale pins are always removable.
func (a *API) handleUnpinChannelContextPost(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	postID := c.Param("postid")
	audit.AddParam(auditRec(c), audit.KeyChannelID, channel.Id)
	audit.AddParam(auditRec(c), audit.KeyPostID, audit.TruncateID(postID))

	if !a.requireManageChannelProperties(c, userID, channel) {
		return
	}

	if err := a.channelContextStore.UnpinPost(channel.Id, postID); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to unpin post from agent context: %w", err))
		return
	}

	a.publishChannelContextUpdated(channel.Id)
	a.respondWithChannelContextPosts(c, channel.Id)
}

// respondWithChannelContextPosts answers a successful write with the updated
// list. The write already happened, so a failed re-read is a server error
// rather than a write failure.
func (a *API) respondWithChannelContextPosts(c *gin.Context, channelID string) {
	posts, err := a.channelContextPostsResponse(channelID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, posts)
}

func (a *API) channelContextPostsResponse(channelID string) (ChannelContextPosts, error) {
	pinned, err := a.channelContextStore.ListPinnedPosts(channelID)
	if err != nil {
		return ChannelContextPosts{}, fmt.Errorf("failed to list agent context posts: %w", err)
	}

	posts := make([]ChannelContextPost, 0, len(pinned))
	for _, p := range pinned {
		posts = append(posts, ChannelContextPost{
			PostID:   p.Post.Id,
			UserID:   p.Post.UserId,
			RootID:   p.Post.RootId,
			Message:  p.Post.Message,
			CreateAt: p.Post.CreateAt,
			PinnedBy: p.PinnedBy,
			PinnedAt: p.PinnedAt,
		})
	}
	return ChannelContextPosts{Posts: posts, MaxPosts: channelcontext.MaxPinnedPosts}, nil
}

// publishChannelContextUpdated tells channel members that the channel's agent
// context changed so open settings and post menus can re-fetch. The payload
// carries only the channel ID; clients re-read through the API.
func (a *API) publishChannelContextUpdated(channelID string) {
	if a.mmClient == nil {
		return
	}
	a.mmClient.PublishWebSocketEvent(
		WebsocketEventChannelContextUpdated,
		map[string]any{"channel_id": channelID},
		&model.WebsocketBroadcast{ChannelId: channelID},
	)
}
