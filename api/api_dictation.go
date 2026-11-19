// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/dictation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
)

// WebsocketEventDictationAvailabilityChanged tells clients to refetch
// GET /dictation/availability (webapp: custom_mattermost-ai_<name>).
const WebsocketEventDictationAvailabilityChanged = "dictation_availability_changed"

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
	c.JSON(http.StatusOK, a.dictationService.Availability(c.Request.Context(), userID))
}

// WebsocketEventDictationSessionEnded tells the user the server ended one
// of their dictation sessions (webapp: custom_mattermost-ai_<name>).
const WebsocketEventDictationSessionEnded = "dictation_session_ended"

var errDictationOfferTooLarge = errors.New("the SDP offer is too large")

type createDictationSessionRequest struct {
	SDP string `json:"sdp"`
}

// POST /dictation/session exchanges the browser's SDP offer for an answer on
// a call the server creates and watches.
func (a *API) handleCreateDictationSession(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if a.dictationService == nil {
		abortDictation(c, http.StatusServiceUnavailable, errDictationUnavailable)
		return
	}

	// The JSON envelope adds a little to the SDP limit.
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, bifrost.MaxSDPBytes+4096+1))
	if err != nil {
		abortDictation(c, http.StatusBadRequest, dictation.ErrInvalidOffer)
		return
	}
	if len(body) > bifrost.MaxSDPBytes+4096 {
		abortDictation(c, http.StatusRequestEntityTooLarge, errDictationOfferTooLarge)
		return
	}
	var req createDictationSessionRequest
	if err = json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.SDP) == "" {
		abortDictation(c, http.StatusBadRequest, dictation.ErrInvalidOffer)
		return
	}
	if len(req.SDP) > bifrost.MaxSDPBytes {
		abortDictation(c, http.StatusRequestEntityTooLarge, errDictationOfferTooLarge)
		return
	}

	userID := c.GetHeader("Mattermost-User-Id")
	session, err := a.dictationService.CreateSession(c.Request.Context(), userID, req.SDP)
	switch {
	case err == nil:
		c.JSON(http.StatusCreated, session)
	case errors.Is(err, enterprise.ErrNotLicensed):
		abortNotLicensed(c, err)
	case errors.Is(err, dictation.ErrInvalidOffer):
		abortDictation(c, http.StatusBadRequest, dictation.ErrInvalidOffer)
	case errors.Is(err, dictation.ErrDisabled):
		abortDictation(c, http.StatusNotFound, dictation.ErrDisabled)
	case errors.Is(err, dictation.ErrAccessDenied):
		abortDictation(c, http.StatusForbidden, dictation.ErrAccessDenied)
	case errors.Is(err, dictation.ErrMisconfigured), errors.Is(err, dictation.ErrSessionsShutdown):
		abortDictation(c, http.StatusServiceUnavailable, errDictationUnavailable)
	case errors.Is(err, dictation.ErrRateLimited):
		abortDictation(c, http.StatusTooManyRequests, dictation.ErrRateLimited)
	case errors.Is(err, dictation.ErrConcurrentLimit):
		abortDictation(c, http.StatusTooManyRequests, dictation.ErrConcurrentLimit)
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
