// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	WebsocketEventChannelArtifactCreated = channelartifacts.EventCreated
	WebsocketEventChannelArtifactUpdated = channelartifacts.EventUpdated
	WebsocketEventChannelArtifactDeleted = channelartifacts.EventDeleted
)

// SetChannelArtifacts installs the channel artifact library service.
func (a *API) SetChannelArtifacts(svc *channelartifacts.Service) {
	a.channelArtifacts = svc
}

func (a *API) channelArtifactsEnabled() bool {
	return a.config.EnableChannelArtifacts()
}

func (a *API) channelArtifactLimits(maxSize int64) channelartifacts.Limits {
	return channelartifacts.Limits{
		MaxSize:        maxSize,
		MaxPerChannel:  a.config.ChannelArtifactsMaxPerChannel(),
		VersionsToKeep: a.config.ChannelArtifactVersionsToKeep(),
	}
}

func (a *API) writeChannelArtifactError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, channelartifacts.ErrNotFound):
		c.String(http.StatusNotFound, "not found")
	case errors.Is(err, channelartifacts.ErrForbidden):
		c.String(http.StatusForbidden, "forbidden")
	case errors.Is(err, channelartifacts.ErrInvalidInput):
		c.String(http.StatusBadRequest, "invalid request")
	case errors.Is(err, channelartifacts.ErrIneligible):
		c.String(http.StatusUnprocessableEntity, "this file cannot be saved as a channel artifact")
	case errors.Is(err, channelartifacts.ErrLimitReached):
		c.String(http.StatusConflict, "channel artifact limit reached")
	case errors.Is(err, channelartifacts.ErrDuplicate):
		body := gin.H{"error": "duplicate"}
		if dup, ok := errors.AsType[*channelartifacts.DuplicateError](err); ok {
			body["artifact_id"] = dup.ArtifactID
		}
		c.JSON(http.StatusConflict, body)
	case errors.Is(err, channelartifacts.ErrTooLarge):
		c.String(http.StatusRequestEntityTooLarge, "artifact too large")
	case errors.Is(err, channelartifacts.ErrUnavailable):
		c.String(http.StatusGone, "the source message or file is no longer available")
	default:
		if a.pluginAPI != nil {
			a.pluginAPI.Log.Error("channel artifact operation failed", "error", err)
		}
		c.String(http.StatusInternalServerError, "internal error")
	}
}

func bindChannelArtifactJSON(c *gin.Context, out any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxAgentRequestBodyBytes)
	if err := c.ShouldBindJSON(out); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			c.String(http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		c.String(http.StatusBadRequest, "invalid request")
		return false
	}
	return true
}

// beginChannelArtifactRoute sets the response headers every library route
// shares and reports whether the library is on. Disabled libraries answer 404.
func (a *API) beginChannelArtifactRoute(c *gin.Context) bool {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
		return false
	}
	return true
}

// channelArtifactIDParam returns the validated :id route parameter and adds it
// to the audit record.
func (a *API) channelArtifactIDParam(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !model.IsValidId(id) {
		c.String(http.StatusBadRequest, "invalid id")
		return "", false
	}
	audit.AddParam(auditRec(c), audit.KeyChannelArtifactID, id)
	return id, true
}

func (a *API) handleListChannelArtifacts(c *gin.Context) {
	if !a.beginChannelArtifactRoute(c) {
		return
	}
	channelID := c.Query("channel_id")
	if !model.IsValidId(channelID) {
		c.String(http.StatusBadRequest, "invalid channel id")
		return
	}
	resp, err := a.channelArtifacts.List(c.GetHeader("Mattermost-User-Id"), channelID)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	resp.MaxPerChannel = a.config.ChannelArtifactsMaxPerChannel()
	c.JSON(http.StatusOK, resp)
}

// handleCreateChannelArtifact saves an agent's HTML attachment as a new library
// entry, or as a new version of the entry named by replace_id.
func (a *API) handleCreateChannelArtifact(c *gin.Context) {
	if !a.beginChannelArtifactRoute(c) {
		return
	}
	var req channelartifacts.SaveRequest
	if !bindChannelArtifactJSON(c, &req) {
		return
	}
	if !model.IsValidId(req.ChannelID) || !model.IsValidId(req.PostID) || !model.IsValidId(req.FileID) ||
		(req.ArtifactID != "" && !model.IsValidId(req.ArtifactID)) {
		c.String(http.StatusBadRequest, "invalid request")
		return
	}
	audit.AddParam(auditRec(c), audit.KeyChannelID, req.ChannelID)
	if req.ArtifactID != "" {
		if rec := auditRec(c); rec != nil {
			rec.EventName = AuditEventReplaceChannelArtifact
		}
		audit.AddParam(auditRec(c), audit.KeyChannelArtifactID, req.ArtifactID)
	}
	if a.mmClient == nil || !a.config.EnableHTMLArtifacts() {
		c.String(http.StatusNotFound, "not found")
		return
	}

	reader := mmapi.WithFilePolicy(a.mmClient, auth.SessionIDFromContext(c.Request.Context()))
	entry, err := a.channelArtifacts.Save(
		c.GetHeader("Mattermost-User-Id"),
		reader,
		req,
		a.channelArtifactLimits(artifactRenderLimit(a.mmClient.GetConfig())),
	)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	audit.AddParam(auditRec(c), audit.KeyChannelArtifactID, entry.ID)
	event, status := WebsocketEventChannelArtifactCreated, http.StatusCreated
	if req.ArtifactID != "" {
		event, status = WebsocketEventChannelArtifactUpdated, http.StatusOK
	}
	a.channelArtifacts.Publish(event, entry.ChannelID, map[string]any{"artifact": entry})
	c.JSON(status, entry)
}

func (a *API) handleGetChannelArtifactInfo(c *gin.Context) {
	if !a.beginChannelArtifactRoute(c) {
		return
	}
	id := c.Param("id")
	if !model.IsValidId(id) {
		c.String(http.StatusBadRequest, "invalid id")
		return
	}
	entry, err := a.channelArtifacts.GetReadable(c.GetHeader("Mattermost-User-Id"), id)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	c.JSON(http.StatusOK, entry)
}

func (a *API) handlePatchChannelArtifact(c *gin.Context) {
	if !a.beginChannelArtifactRoute(c) {
		return
	}
	id, ok := a.channelArtifactIDParam(c)
	if !ok {
		return
	}
	var req channelartifacts.PatchRequest
	if !bindChannelArtifactJSON(c, &req) {
		return
	}
	entry, err := a.channelArtifacts.Patch(c.GetHeader("Mattermost-User-Id"), id, req)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	a.channelArtifacts.Publish(WebsocketEventChannelArtifactUpdated, entry.ChannelID, map[string]any{"artifact": entry})
	c.JSON(http.StatusOK, entry)
}

func (a *API) handleRollbackChannelArtifact(c *gin.Context) {
	if !a.beginChannelArtifactRoute(c) {
		return
	}
	id, ok := a.channelArtifactIDParam(c)
	if !ok {
		return
	}
	var req channelartifacts.RollbackRequest
	if !bindChannelArtifactJSON(c, &req) {
		return
	}
	if req.Version < 1 {
		c.String(http.StatusBadRequest, "invalid request")
		return
	}
	audit.AddParam(auditRec(c), audit.KeyChannelArtifactVersion, strconv.Itoa(req.Version))
	entry, err := a.channelArtifacts.Rollback(c.GetHeader("Mattermost-User-Id"), id, req.Version)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	a.channelArtifacts.Publish(WebsocketEventChannelArtifactUpdated, entry.ChannelID, map[string]any{"artifact": entry})
	c.JSON(http.StatusOK, entry)
}

// handleDeleteChannelArtifact removes the library entry and its version rows.
// The source posts and files are not touched.
func (a *API) handleDeleteChannelArtifact(c *gin.Context) {
	if !a.beginChannelArtifactRoute(c) {
		return
	}
	id, ok := a.channelArtifactIDParam(c)
	if !ok {
		return
	}
	entry, err := a.channelArtifacts.Delete(c.GetHeader("Mattermost-User-Id"), id)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	audit.AddParam(auditRec(c), audit.KeyChannelID, entry.ChannelID)
	a.channelArtifacts.Publish(WebsocketEventChannelArtifactDeleted, entry.ChannelID, map[string]any{"id": id})
	c.Status(http.StatusNoContent)
}

// handleDeleteChannelArtifactVersion removes one version row. Removing the
// current version makes the newest remaining one current.
func (a *API) handleDeleteChannelArtifactVersion(c *gin.Context) {
	if !a.beginChannelArtifactRoute(c) {
		return
	}
	id, ok := a.channelArtifactIDParam(c)
	if !ok {
		return
	}
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version < 1 {
		c.String(http.StatusBadRequest, "invalid version")
		return
	}
	audit.AddParam(auditRec(c), audit.KeyChannelArtifactVersion, strconv.Itoa(version))
	entry, err := a.channelArtifacts.DeleteVersion(c.GetHeader("Mattermost-User-Id"), id, version)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	a.channelArtifacts.Publish(WebsocketEventChannelArtifactUpdated, entry.ChannelID, map[string]any{"artifact": entry})
	c.JSON(http.StatusOK, entry)
}

func (a *API) handleListChannelArtifactVersions(c *gin.Context) {
	if !a.beginChannelArtifactRoute(c) {
		return
	}
	id := c.Param("id")
	if !model.IsValidId(id) {
		c.String(http.StatusBadRequest, "invalid id")
		return
	}
	versions, err := a.channelArtifacts.Versions(c.GetHeader("Mattermost-User-Id"), id)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

// libraryArtifactBridgeToken authenticates bridge messages for a library entry.
func libraryArtifactBridgeToken(secret []byte, userID, artifactID, nonce string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("html-artifact-bridge:v3|library|" + userID + "|" + artifactID + "|" + nonce))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *API) authorizeLibraryArtifact(c *gin.Context) (*channelartifacts.Entry, bool) {
	if !a.beginChannelArtifactRoute(c) {
		return nil, false
	}
	id := c.Param("id")
	if !model.IsValidId(id) {
		c.String(http.StatusBadRequest, "invalid id")
		return nil, false
	}
	entry, err := a.channelArtifacts.GetReadable(c.GetHeader("Mattermost-User-Id"), id)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return nil, false
	}
	return entry, true
}

// strictQueryValue reads one query parameter and rejects a malformed query
// string or a repeated key. c.Query silently drops bad pairs and takes the
// first of a repeated key, which would turn a bad request into a default one.
func strictQueryValue(c *gin.Context, key string) (string, bool) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil || len(query[key]) > 1 {
		c.String(http.StatusBadRequest, "invalid request")
		return "", false
	}
	return query.Get(key), true
}

func parseArtifactVersionQuery(c *gin.Context) (int, bool) {
	raw, ok := strictQueryValue(c, "version")
	if !ok {
		return 0, false
	}
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		c.String(http.StatusBadRequest, "invalid version")
		return 0, false
	}
	return n, true
}

func (a *API) handleGetChannelArtifactToken(c *gin.Context) {
	entry, ok := a.authorizeLibraryArtifact(c)
	if !ok {
		return
	}
	nonce, ok := artifactNonceAllowing(c, "version")
	if !ok {
		return
	}
	secret, err := a.artifactSecret()
	if err != nil {
		a.logArtifactError("Failed to load artifact bridge secret", err, entry.ID)
		c.String(http.StatusInternalServerError, "failed to prepare artifact")
		return
	}
	version, ok := parseArtifactVersionQuery(c)
	if !ok {
		return
	}
	if err = a.channelArtifacts.CheckReadable(auth.SessionIDFromContext(c.Request.Context()), entry, version); err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	rec, err := a.channelArtifacts.VersionOf(entry, version)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	token := libraryArtifactBridgeToken(secret, c.GetHeader("Mattermost-User-Id"), entry.ID, nonce)
	c.JSON(http.StatusOK, gin.H{"token": token, "tooLarge": rec.Size > artifactRenderLimit(a.mmClient.GetConfig())})
}

func (a *API) handleGetChannelArtifact(c *gin.Context) {
	entry, ok := a.authorizeLibraryArtifact(c)
	if !ok {
		return
	}

	if !strings.EqualFold(c.GetHeader("Sec-Fetch-Dest"), "iframe") || !strings.EqualFold(c.GetHeader("Sec-Fetch-Site"), "same-origin") {
		h := c.Writer.Header()
		h.Set("Content-Security-Policy", artifactInterstitialCSP)
		h.Set("Referrer-Policy", "no-referrer")
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(artifactInterstitialHTML))
		return
	}

	version, ok := parseArtifactVersionQuery(c)
	if !ok {
		return
	}
	// Library frames pass ?n=&version=; allow version beside the nonce.
	nonce, ok := artifactNonceAllowing(c, "version")
	if !ok {
		return
	}

	cfg := a.mmClient.GetConfig()
	secret, err := a.artifactSecret()
	if err != nil {
		a.logArtifactError("Failed to load artifact bridge secret", err, entry.ID)
		c.String(http.StatusInternalServerError, "failed to prepare artifact")
		return
	}
	token := libraryArtifactBridgeToken(secret, c.GetHeader("Mattermost-User-Id"), entry.ID, nonce)

	body, err := a.channelArtifacts.ReadContent(auth.SessionIDFromContext(c.Request.Context()), entry, version, artifactRenderLimit(cfg))
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}

	h := c.Writer.Header()
	h.Set("Content-Security-Policy", artifactCSP)
	h.Set("Connection-Allowlist", artifactConnectionAllowlist)
	h.Set("Permissions-Policy", artifactPermissionsPolicy)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Content-Disposition", "inline")
	c.Data(http.StatusOK, "text/html; charset=utf-8", injectArtifactBridge(body, token, artifactTargetOrigin(cfg)))
}

func (a *API) handleDownloadChannelArtifact(c *gin.Context) {
	entry, ok := a.authorizeLibraryArtifact(c)
	if !ok {
		return
	}
	version, ok := parseArtifactVersionQuery(c)
	if !ok {
		return
	}
	body, err := a.channelArtifacts.ReadContent(auth.SessionIDFromContext(c.Request.Context()), entry, version, artifactRenderLimit(a.mmClient.GetConfig()))
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	h := c.Writer.Header()
	// Saved HTML is agent-authored, so a navigation to this URL must never run it
	// on the Mattermost origin: it is not served as a page, and the sandbox
	// policy gives it an opaque origin even if a browser renders it anyway.
	h.Set("Content-Disposition", attachmentDisposition(entry.Title+".html"))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Referrer-Policy", "no-referrer")
	c.Data(http.StatusOK, "application/octet-stream", body)
}

func attachmentDisposition(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, name)
	if cleaned == "" {
		cleaned = "artifact.html"
	}
	return `attachment; filename="` + cleaned + `"`
}
