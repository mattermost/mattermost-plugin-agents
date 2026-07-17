// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/delegation"
)

// SetDelegationService sets the delegation service used by the delegation
// status endpoint.
func (a *API) SetDelegationService(svc *delegation.Service) {
	a.delegationService = svc
}

// handleGetDelegationStatus returns the live status of a delegation keyed by
// the parent ask_agent tool call ID. Initiator-only: any other user (or an
// unknown ID) gets a 404 so existence is not leaked.
func (a *API) handleGetDelegationStatus(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	parentToolCallID := c.Param("parenttoolcallid")

	if a.delegationService == nil {
		c.AbortWithError(http.StatusNotFound, errors.New("delegation service not available"))
		return
	}

	status, err := a.delegationService.StatusByParentToolCall(userID, parentToolCallID)
	if err != nil {
		if errors.Is(err, delegation.ErrNotConfigured) {
			c.AbortWithError(http.StatusNotFound, err)
			return
		}
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	if status == nil {
		c.AbortWithError(http.StatusNotFound, errors.New("delegation not found"))
		return
	}

	c.JSON(http.StatusOK, status)
}
