// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/dictation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
)

var errDictationUnavailable = errors.New("dictation is not available")

type dictationErrorResponse struct {
	Error string `json:"error"`
}

// SetDictationService sets the service behind the /dictation routes. Without
// one, both routes report dictation as unavailable.
func (a *API) SetDictationService(svc *dictation.Service) {
	a.dictationService = svc
}

// GET /dictation/availability
func (a *API) handleGetDictationAvailability(c *gin.Context) {
	if a.dictationService == nil {
		abortDictation(c, http.StatusServiceUnavailable, errDictationUnavailable)
		return
	}
	userID := c.GetHeader("Mattermost-User-Id")
	availability, err := a.dictationService.Availability(c.Request.Context(), userID)
	if err != nil {
		a.pluginAPI.Log.Error("Failed to check dictation availability", "error", err.Error())
		abortDictation(c, http.StatusInternalServerError, errDictationUnavailable)
		return
	}
	c.JSON(http.StatusOK, availability)
}

// POST /dictation/session
func (a *API) handleCreateDictationSession(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if a.dictationService == nil {
		abortDictation(c, http.StatusServiceUnavailable, errDictationUnavailable)
		return
	}

	if d := a.config.Dictation(); d.Enabled {
		audit.AddParam(auditRec(c), audit.KeyServiceID, d.ServiceID)
		audit.AddParam(auditRec(c), audit.KeyModel, d.Model)
	}

	userID := c.GetHeader("Mattermost-User-Id")
	session, err := a.dictationService.CreateSession(c.Request.Context(), userID)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, session)
	case errors.Is(err, enterprise.ErrNotLicensed):
		abortNotLicensed(c, err)
	case errors.Is(err, dictation.ErrDisabled):
		abortDictation(c, http.StatusNotFound, dictation.ErrDisabled)
	case errors.Is(err, dictation.ErrAccessDenied):
		abortDictation(c, http.StatusForbidden, dictation.ErrAccessDenied)
	case errors.Is(err, dictation.ErrMisconfigured):
		abortDictation(c, http.StatusServiceUnavailable, dictation.ErrMisconfigured)
	case errors.Is(err, dictation.ErrRateLimited):
		abortDictation(c, http.StatusTooManyRequests, dictation.ErrRateLimited)
	case errors.Is(err, dictation.ErrProvider):
		abortDictation(c, http.StatusBadGateway, dictation.ErrProvider)
	default:
		a.pluginAPI.Log.Error("Failed to create a dictation session", "error", err.Error())
		abortDictation(c, http.StatusInternalServerError, errDictationUnavailable)
	}
}

// abortDictation records only the public error on the request: gin errors
// reach the HTTP span status, and provider errors can carry upstream text.
// The dictation service logs the detail server-side.
func abortDictation(c *gin.Context, status int, public error) {
	_ = c.Error(public)
	c.AbortWithStatusJSON(status, dictationErrorResponse{Error: public.Error()})
}
