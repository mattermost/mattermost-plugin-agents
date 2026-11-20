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
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	WebsocketEventChannelArtifactCreated = channelartifacts.EventCreated
	WebsocketEventChannelArtifactUpdated = channelartifacts.EventUpdated
	WebsocketEventChannelArtifactDeleted = channelartifacts.EventDeleted
)

// SetChannelArtifacts installs the channel artifact library.
func (a *API) SetChannelArtifacts(library channelartifacts.Manager) {
	a.channelLibrary = library
}

// channelArtifactActor is the signed-in user making the request.
func channelArtifactActor(c *gin.Context) channelartifacts.Actor {
	return channelartifacts.Actor{
		UserID:    c.GetHeader("Mattermost-User-Id"),
		SessionID: auth.SessionIDFromContext(c.Request.Context()),
	}
}

func (a *API) channelArtifactsEnabled() bool {
	return a.config.EnableChannelArtifacts()
}

func (a *API) writeChannelArtifactError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, channelartifacts.ErrNotFound):
		c.String(http.StatusNotFound, "not found")
	case errors.Is(err, channelartifacts.ErrForbidden), errors.Is(err, channelartifacts.ErrSessionRequired):
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
	if !a.channelArtifactsEnabled() || a.channelLibrary == nil {
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
	resp, err := a.channelLibrary.List(c.Request.Context(), channelArtifactActor(c), channelID)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// handleCreateChannelArtifact saves an agent's HTML or Markdown attachment as a
// new library entry, or as a new version of the entry named by replace_id.
func (a *API) handleCreateChannelArtifact(c *gin.Context) {
	if !a.beginChannelArtifactRoute(c) {
		return
	}
	var req channelartifacts.SaveRequest
	if !bindChannelArtifactJSON(c, &req) {
		return
	}
	if !model.IsValidId(req.ChannelID) || !model.IsValidId(req.FileID) ||
		(req.PostID != "" && !model.IsValidId(req.PostID)) ||
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

	entry, err := a.channelLibrary.Write(c.Request.Context(), channelArtifactActor(c), channelartifacts.WriteRequest{SaveRequest: req})
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	audit.AddParam(auditRec(c), audit.KeyChannelArtifactID, entry.ID)
	status := http.StatusCreated
	if req.ArtifactID != "" {
		status = http.StatusOK
	}
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
	entry, err := a.channelLibrary.Get(c.Request.Context(), channelArtifactActor(c), id)
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
	entry, err := a.channelLibrary.Rename(c.Request.Context(), channelArtifactActor(c), id, req.Title)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
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
	entry, err := a.channelLibrary.Rollback(c.Request.Context(), channelArtifactActor(c), id, req.Version)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
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
	entry, err := a.channelLibrary.Remove(c.Request.Context(), channelArtifactActor(c), id)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	audit.AddParam(auditRec(c), audit.KeyChannelID, entry.ChannelID)
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
	entry, err := a.channelLibrary.RemoveVersion(c.Request.Context(), channelArtifactActor(c), id, version)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
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
	versions, err := a.channelLibrary.Versions(c.Request.Context(), channelArtifactActor(c), id)
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
	entry, err := a.channelLibrary.Get(c.Request.Context(), channelArtifactActor(c), id)
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

// requireArtifactFormat answers 415 unless the entry has the format the route
// serves.
func requireArtifactFormat(c *gin.Context, entry *channelartifacts.Entry, want channelartifacts.Format) bool {
	if entry.Format != want {
		c.String(http.StatusUnsupportedMediaType, "this route does not serve "+string(entry.Format)+" artifacts")
		return false
	}
	return true
}

func (a *API) handleGetChannelArtifactToken(c *gin.Context) {
	entry, ok := a.authorizeLibraryArtifact(c)
	if !ok || !requireArtifactFormat(c, entry, channelartifacts.FormatHTML) {
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
	ctx, actor := c.Request.Context(), channelArtifactActor(c)
	if err = a.channelLibrary.CheckReadable(ctx, actor, entry.ID, version); err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	viewed, err := a.channelLibrary.Read(ctx, actor, entry.ID, channelartifacts.ReadOptions{Version: version, MetadataOnly: true})
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	token := libraryArtifactBridgeToken(secret, actor.UserID, entry.ID, nonce)
	c.JSON(http.StatusOK, gin.H{"token": token, "tooLarge": viewed.Entry.Size > artifactRenderLimit(a.mmClient.GetConfig())})
}

func (a *API) handleGetChannelArtifact(c *gin.Context) {
	entry, ok := a.authorizeLibraryArtifact(c)
	if !ok || !requireArtifactFormat(c, entry, channelartifacts.FormatHTML) {
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

	content, err := a.channelLibrary.Read(c.Request.Context(), channelArtifactActor(c), entry.ID, channelartifacts.ReadOptions{Version: version})
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	body := content.Body

	h := c.Writer.Header()
	h.Set("Content-Security-Policy", artifactCSP)
	h.Set("Connection-Allowlist", artifactConnectionAllowlist)
	h.Set("Permissions-Policy", artifactPermissionsPolicy)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Content-Disposition", "inline")
	c.Data(http.StatusOK, "text/html; charset=utf-8", injectArtifactBridge(body, token, artifactTargetOrigin(cfg)))
}

// handleGetChannelArtifactContent serves a Markdown artifact's source for the
// host to render. It is plain text, never HTML, so the browser cannot run it.
func (a *API) handleGetChannelArtifactContent(c *gin.Context) {
	entry, ok := a.authorizeLibraryArtifact(c)
	if !ok || !requireArtifactFormat(c, entry, channelartifacts.FormatMarkdown) {
		return
	}
	version, ok := parseArtifactVersionQuery(c)
	if !ok {
		return
	}
	content, err := a.channelLibrary.Read(c.Request.Context(), channelArtifactActor(c), entry.ID, channelartifacts.ReadOptions{Version: version})
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	h := c.Writer.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", "inline")
	c.Data(http.StatusOK, entry.Format.ContentType(), content.Body)
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
	content, err := a.channelLibrary.Read(c.Request.Context(), channelArtifactActor(c), entry.ID, channelartifacts.ReadOptions{Version: version})
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	h := c.Writer.Header()
	h.Set("Content-Disposition", attachmentDisposition(entry.Title+entry.Format.Extension(), entry.Format.Extension()))
	h.Set("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, entry.Format.ContentType(), content.Body)
}

func attachmentDisposition(name, fallback string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, name)
	if cleaned == "" {
		cleaned = "artifact" + fallback
	}
	return `attachment; filename="` + cleaned + `"`
}
