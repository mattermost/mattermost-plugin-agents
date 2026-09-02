// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost/server/public/model"
)

// WebsocketEventChannelAutoReplyUpdated is the event name for PublishWebSocketEvent
// (webapp: custom_mattermost-ai_<name>).
const WebsocketEventChannelAutoReplyUpdated = "channel_autoreply_updated"

// channelAutoReplyMaxRequestBodyBytes caps the PUT body; the payload is two short strings.
const channelAutoReplyMaxRequestBodyBytes = 1 << 10 // 1 KiB

// Wire values for the auto-reply mode enum (fixed cross-phase contract).
const (
	channelAutoReplyModeOff       = "off"
	channelAutoReplyModeRootPosts = "root_posts"
	channelAutoReplyModeThreads   = "threads"
)

// ChannelAutoReply is the request and response payload for
// GET/PUT /channel/{channelid}/autoreply.
type ChannelAutoReply struct {
	BotID string `json:"bot_id"`
	Mode  string `json:"mode"`
}

// handleGetChannelAutoReply returns the channel's auto-reply setting. Readable
// by any channel member (PermissionReadChannel is enforced by the router
// middleware). GET is intentionally not license-gated so the webapp can always
// render the current state; only writing and firing are licensed.
func (a *API) handleGetChannelAutoReply(c *gin.Context) {
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	setting, err := a.autoReplyStore.Get(channel.Id)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get channel auto-reply setting: %w", err))
		return
	}
	if setting == nil {
		c.JSON(http.StatusOK, ChannelAutoReply{BotID: "", Mode: channelAutoReplyModeOff})
		return
	}
	c.JSON(http.StatusOK, ChannelAutoReply{BotID: setting.BotID, Mode: string(setting.Mode)})
}

// handlePutChannelAutoReply updates the channel's auto-reply setting. Requires
// the channel-management permission matching the channel type; DM/GM channels
// are rejected. Enabling (root_posts/threads) additionally requires a license;
// mode "off" deletes the row and is never license-gated so an existing setting
// stays clearable after a license downgrade. The requested setting itself is
// validated by the auto-reply service, whose ErrValidation failures become
// 400s. On success the new state is published as a channel-scoped websocket
// event and echoed back.
func (a *API) handlePutChannelAutoReply(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	audit.AddParam(auditRec(c), audit.KeyChannelID, channel.Id)

	var perm *model.Permission
	switch channel.Type {
	case model.ChannelTypeOpen:
		perm = model.PermissionManagePublicChannelProperties
	case model.ChannelTypePrivate:
		perm = model.PermissionManagePrivateChannelProperties
	default: // ChannelTypeDirect, ChannelTypeGroup
		c.AbortWithError(http.StatusBadRequest,
			errors.New("auto-reply cannot be configured for direct or group message channels"))
		return
	}
	if !a.pluginAPI.User.HasPermissionToChannel(userID, channel.Id, perm) {
		c.AbortWithError(http.StatusForbidden, errors.New("user doesn't have permission to manage channel properties"))
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, channelAutoReplyMaxRequestBodyBytes)
	var req ChannelAutoReply
	if err := c.ShouldBindJSON(&req); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			c.AbortWithError(http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large: %w", err))
			return
		}
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Body values are recorded before validation so fail records carry them
	// too; TruncateID clamps them since they are unvalidated request input.
	audit.AddParam(auditRec(c), "mode", audit.TruncateID(req.Mode))
	if req.BotID != "" {
		audit.AddParam(auditRec(c), "bot_user_id", audit.TruncateID(req.BotID))
	}

	var saved ChannelAutoReply
	switch req.Mode {
	case channelAutoReplyModeOff:
		// "off" is represented by row absence; BotID is irrelevant.
		if err := a.autoReplyStore.Delete(channel.Id); err != nil {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to delete channel auto-reply setting: %w", err))
			return
		}
		saved = ChannelAutoReply{BotID: "", Mode: channelAutoReplyModeOff}
	case channelAutoReplyModeRootPosts, channelAutoReplyModeThreads:
		if !a.licenseChecker.IsBasicsLicensed() {
			c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
			return
		}
		// bot_id is validated by the auto-reply service (present, known, and
		// allowed in this channel); its ErrValidation failures map to 400.
		if _, err := a.autoReplyStore.Set(channel.Id, req.BotID, autoreply.Mode(req.Mode), userID); err != nil {
			if errors.Is(err, autoreply.ErrValidation) {
				c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid auto-reply setting: %w", err))
				return
			}
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save channel auto-reply setting: %w", err))
			return
		}
		saved = ChannelAutoReply{BotID: req.BotID, Mode: req.Mode}
	default:
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid mode: %q", req.Mode))
		return
	}

	a.publishChannelAutoReplyUpdated(channel.Id, saved)

	c.JSON(http.StatusOK, saved)
}

// publishChannelAutoReplyUpdated notifies channel members that the auto-reply
// setting changed so open channel-settings modals can re-sync. The broadcast
// must be non-nil (the server dereferences it).
func (a *API) publishChannelAutoReplyUpdated(channelID string, saved ChannelAutoReply) {
	if a.mmClient == nil {
		return
	}
	a.mmClient.PublishWebSocketEvent(
		WebsocketEventChannelAutoReplyUpdated,
		map[string]any{
			"channel_id": channelID,
			"bot_id":     saved.BotID,
			"mode":       saved.Mode,
		},
		&model.WebsocketBroadcast{ChannelId: channelID},
	)
}
