// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost/server/public/model"
)

// maxViewingBodyBytes bounds the {"viewing": bool} body.
const maxViewingBodyBytes = 1024

// AgentPostNotifications is what the API needs from agent post notifications.
type AgentPostNotifications interface {
	// Supported reports whether the server defers agent post notifications.
	Supported() bool
	// SetViewing records whether userID has postID on screen in a focused window.
	SetViewing(postID, userID string, viewing bool) error
}

// SetNotifications sets the agent post notifications service.
func (a *API) SetNotifications(n AgentPostNotifications) {
	a.notifications = n
}

// handleViewing records whether the caller has an agent post on screen, so
// pushes and emails about it are withheld from them while they watch it.
// Not audited: it only changes the caller's own short-lived presence state.
func (a *API) handleViewing(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)

	var data struct {
		Viewing *bool `json:"viewing" binding:"required"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxViewingBodyBytes)
	if err := c.ShouldBindJSON(&data); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if a.notifications == nil {
		c.AbortWithError(http.StatusInternalServerError, errors.New("agent post notifications are not available"))
		return
	}
	if err := a.notifications.SetViewing(post.Id, userID, *data.Viewing); err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.Status(http.StatusOK)
}
