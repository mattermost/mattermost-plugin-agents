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
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost/server/public/model"
)

// WebsocketEventChannelAutoReplyUpdated is the event name for PublishWebSocketEvent
// (webapp: custom_mattermost-ai_<name>).
const WebsocketEventChannelAutoReplyUpdated = "channel_autoreply_updated"

// channelAutoReplyMaxRequestBodyBytes caps the PUT body; ambient mode may
// include instructions up to autoreply.MaxInstructionsRunes.
const channelAutoReplyMaxRequestBodyBytes = 64 << 10 // 64 KiB

// ChannelAutoReply is the request and response payload for
// GET/PUT /channel/{channelid}/autoreply.
type ChannelAutoReply struct {
	BotID        string `json:"bot_id"`
	Mode         string `json:"mode"`
	Instructions string `json:"instructions,omitempty"`
}

// handleGetChannelAutoReply returns the channel's auto-reply setting. Readable
// by any channel member (PermissionReadChannel is enforced by the router
// middleware), but the ambient instructions are returned only to users who
// can manage the setting. GET is not license-gated so the
// current state is always readable; writing an enabled mode is available at
// Enterprise Advanced and above.
func (a *API) handleGetChannelAutoReply(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	setting, err := a.autoReplyStore.Get(channel.Id)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get channel auto-reply setting: %w", err))
		return
	}
	if setting == nil {
		c.JSON(http.StatusOK, ChannelAutoReply{BotID: "", Mode: string(autoreply.ModeOff)})
		return
	}
	resp := channelAutoReplyFromSetting(setting)
	if resp.Instructions != "" {
		perm := channelManagePermission(channel)
		if perm == nil || !a.pluginAPI.User.HasPermissionToChannel(userID, channel.Id, perm) {
			resp.Instructions = ""
		}
	}
	c.JSON(http.StatusOK, resp)
}

// channelManagePermission returns the permission that governs a channel's
// auto-reply setting, or nil for DM/GM channels, which cannot have one.
func channelManagePermission(channel *model.Channel) *model.Permission {
	switch channel.Type {
	case model.ChannelTypeOpen:
		return model.PermissionManagePublicChannelProperties
	case model.ChannelTypePrivate:
		return model.PermissionManagePrivateChannelProperties
	default:
		return nil
	}
}

// handlePutChannelAutoReply updates the channel's auto-reply setting. Requires
// the channel-management permission matching the channel type; DM/GM channels
// are rejected. Enabling a mode is available at Enterprise Advanced and above,
// and the experimental ambient modes additionally require the admin's
// EnableAmbientAutoReply setting; mode "off" deletes the row and is never
// gated. The requested setting itself is validated by the auto-reply service, whose
// ErrValidation failures become 400s. On success the new state is published as
// a channel-scoped websocket event and echoed back.
func (a *API) handlePutChannelAutoReply(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	audit.AddParam(auditRec(c), audit.KeyChannelID, channel.Id)

	perm := channelManagePermission(channel)
	if perm == nil {
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
	// Instructions are configuration content, never audited.
	audit.AddParam(auditRec(c), "mode", audit.TruncateID(req.Mode))
	if req.BotID != "" {
		audit.AddParam(auditRec(c), "bot_user_id", audit.TruncateID(req.BotID))
	}

	var saved ChannelAutoReply
	switch mode := autoreply.Mode(req.Mode); {
	case mode == autoreply.ModeOff:
		// "off" is represented by row absence; BotID is irrelevant.
		if err := a.autoReplyStore.Delete(channel.Id); err != nil {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to delete channel auto-reply setting: %w", err))
			return
		}
		saved = ChannelAutoReply{BotID: "", Mode: string(autoreply.ModeOff)}
	case mode.IsStorable():
		if !a.requireCapability(c, enterprise.CapChannelAutoReply) {
			return
		}
		// 409 rather than 403 so the webapp can tell the admin setting apart
		// from a missing channel permission.
		if mode.IsAmbient() && !a.config.EnableAmbientAutoReply() {
			c.AbortWithError(http.StatusConflict, errors.New("ambient auto-reply is disabled by the system admin"))
			return
		}
		// bot_id is validated by the auto-reply service (present, known, and
		// allowed in this channel); its ErrValidation failures map to 400.
		if _, err := a.autoReplyStore.Set(autoreply.Setting{
			ChannelID:    channel.Id,
			BotID:        req.BotID,
			Mode:         mode,
			UpdatedBy:    userID,
			Instructions: req.Instructions,
		}); err != nil {
			if errors.Is(err, autoreply.ErrValidation) {
				c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid auto-reply setting: %w", err))
				return
			}
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save channel auto-reply setting: %w", err))
			return
		}
		saved = ChannelAutoReply{
			BotID:        req.BotID,
			Mode:         req.Mode,
			Instructions: req.Instructions,
		}
	default:
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid mode: %q", req.Mode))
		return
	}

	a.publishChannelAutoReplyUpdated(channel.Id, saved)

	c.JSON(http.StatusOK, saved)
}

func channelAutoReplyFromSetting(setting *autoreply.Setting) ChannelAutoReply {
	return ChannelAutoReply{
		BotID:        setting.BotID,
		Mode:         string(setting.Mode),
		Instructions: setting.Instructions,
	}
}

// publishChannelAutoReplyUpdated notifies channel members that the auto-reply
// setting changed so open channel-settings modals can re-sync. The broadcast
// must be non-nil (the server dereferences it). Instructions are deliberately
// omitted from the payload.
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
