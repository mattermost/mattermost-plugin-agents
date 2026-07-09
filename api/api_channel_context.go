// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelcontext"
	"github.com/mattermost/mattermost/server/public/model"
)

type ChannelContextService interface {
	Get(channelID string) (channelcontext.State, error)
	Save(channelID, userID string, update channelcontext.Update) (channelcontext.State, error)
}

func (a *API) channelContextAuthorizationRequired(c *gin.Context) {
	channelID := c.Param("channelid")
	if !model.IsValidId(channelID) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}

	channel, err := a.pluginAPI.Channel.Get(channelID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "channel not found"})
		return
	}

	var permission *model.Permission
	switch channel.Type {
	case model.ChannelTypeOpen:
		permission = model.PermissionManagePublicChannelProperties
	case model.ChannelTypePrivate:
		permission = model.PermissionManagePrivateChannelProperties
	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "channel context is only available for public and private channels"})
		return
	}

	userID := c.GetHeader("Mattermost-User-Id")
	if !a.pluginAPI.User.HasPermissionToChannel(userID, channelID, permission) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "you do not have permission to manage this channel"})
		return
	}

	c.Set(ContextChannelKey, channel)
}

func (a *API) handleGetChannelContext(c *gin.Context) {
	if a.channelContextService == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "channel context service is unavailable"})
		return
	}

	state, err := a.channelContextService.Get(c.Param("channelid"))
	if err != nil {
		a.writeChannelContextError(c, err)
		return
	}

	c.JSON(http.StatusOK, state)
}

func (a *API) handleSaveChannelContext(c *gin.Context) {
	if a.channelContextService == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "channel context service is unavailable"})
		return
	}

	var update channelcontext.Update
	if err := c.ShouldBindJSON(&update); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	state, err := a.channelContextService.Save(
		c.Param("channelid"),
		c.GetHeader("Mattermost-User-Id"),
		update,
	)
	if err != nil {
		a.writeChannelContextError(c, err)
		return
	}

	c.JSON(http.StatusOK, state)
}

func (a *API) writeChannelContextError(c *gin.Context, err error) {
	if channelcontext.IsValidationError(err) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	a.pluginAPI.Log.Error("Channel context request failed", "error", err)
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to manage channel context"})
}
