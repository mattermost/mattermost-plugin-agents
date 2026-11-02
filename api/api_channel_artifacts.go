// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	WebsocketEventChannelArtifactCreated   = "channel_artifact_created"
	WebsocketEventChannelArtifactUpdated   = "channel_artifact_updated"
	WebsocketEventChannelArtifactDeleted   = "channel_artifact_deleted"
	WebsocketEventChannelArtifactReordered = "channel_artifact_reordered"

	artifactRenderingDisabledHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Artifact unavailable</title></head><body><p>Channel artifact rendering is disabled by the system administrator.</p></body></html>`
)

// SetChannelArtifacts installs the channel artifact library service.
func (a *API) SetChannelArtifacts(svc *channelartifacts.Service) {
	a.channelArtifacts = svc
}

func (a *API) channelArtifactsEnabled() bool {
	return a.config.EnableChannelArtifacts()
}

func (a *API) channelArtifactLimits() channelartifacts.ContentLimits {
	return channelartifacts.ContentLimits{
		MaxPerChannel:  a.config.ChannelArtifactsMaxPerChannel(),
		MaxSitewide:    a.config.ChannelArtifactsMaxSitewide(),
		VersionsToKeep: a.config.ChannelArtifactVersionsToKeep(),
	}
}

func (a *API) writeChannelArtifactError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, channelartifacts.ErrNotFound):
		c.String(http.StatusNotFound, "not found")
	case errors.Is(err, channelartifacts.ErrInvalidInput), errors.Is(err, channelartifacts.ErrNotPermutation):
		c.String(http.StatusBadRequest, "invalid request")
	case errors.Is(err, channelartifacts.ErrLimitReached):
		c.String(http.StatusConflict, "channel artifact limit reached")
	case errors.Is(err, channelartifacts.ErrDuplicate):
		c.JSON(http.StatusConflict, gin.H{
			"error":       "duplicate",
			"artifact_id": channelartifacts.DuplicateID(err),
		})
	case errors.Is(err, channelartifacts.ErrTooLarge):
		c.String(http.StatusRequestEntityTooLarge, "artifact too large")
	case errors.Is(err, channelartifacts.ErrConflict):
		c.String(http.StatusConflict, "conflict")
	default:
		if a.pluginAPI != nil {
			a.pluginAPI.Log.Error("channel artifact operation failed", "error", err)
		}
		c.String(http.StatusInternalServerError, "internal error")
	}
}

func (a *API) handleListChannelArtifacts(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
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
	resp.DownloadEnabled = !a.config.DisableChannelArtifactDownload()
	resp.RenderEnabled = !a.config.DisableChannelArtifactRendering()
	c.JSON(http.StatusOK, resp)
}

func (a *API) handleCreateChannelArtifact(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	var req channelartifacts.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid request")
		return
	}
	if !model.IsValidId(req.ChannelID) || !model.IsValidId(req.PostID) || !model.IsValidId(req.FileID) {
		c.String(http.StatusBadRequest, "invalid request")
		return
	}
	if req.ReplaceID != "" && !model.IsValidId(req.ReplaceID) {
		c.String(http.StatusBadRequest, "invalid request")
		return
	}

	userID := c.GetHeader("Mattermost-User-Id")
	body, info, post, authorDisplay, teamID, limit, ok := a.readPostArtifactBody(c, userID, req.FileID, req.PostID, req.ChannelID)
	if !ok {
		return
	}

	limits := a.channelArtifactLimits()
	limits.MaxSize = limit
	entry, err := a.channelArtifacts.CreateFromHTML(
		userID,
		req,
		body,
		info.Name,
		post.UserId,
		authorDisplay,
		teamID,
		channelartifacts.RootPostID(post),
		limits,
	)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	event := WebsocketEventChannelArtifactCreated
	status := http.StatusCreated
	if req.ReplaceID != "" {
		event = WebsocketEventChannelArtifactUpdated
		status = http.StatusOK
	}
	a.channelArtifacts.Publish(event, entry.ChannelID, map[string]any{
		"artifact": entry,
	})
	c.JSON(status, entry)
}

func (a *API) handleReplaceChannelArtifact(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	id := c.Param("id")
	if !model.IsValidId(id) {
		c.String(http.StatusBadRequest, "invalid id")
		return
	}
	var req channelartifacts.ReplaceRequest
	if err := c.ShouldBindJSON(&req); err != nil || !model.IsValidId(req.PostID) || !model.IsValidId(req.FileID) {
		c.String(http.StatusBadRequest, "invalid request")
		return
	}
	userID := c.GetHeader("Mattermost-User-Id")
	entry, err := a.channelArtifacts.GetReadable(userID, id)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	body, info, post, authorDisplay, _, limit, ok := a.readPostArtifactBody(c, userID, req.FileID, req.PostID, entry.ChannelID)
	if !ok {
		return
	}
	limits := a.channelArtifactLimits()
	limits.MaxSize = limit
	updated, err := a.channelArtifacts.ReplaceContent(
		userID,
		id,
		req,
		body,
		info.Name,
		post.UserId,
		authorDisplay,
		channelartifacts.RootPostID(post),
		limits,
	)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	a.channelArtifacts.Publish(WebsocketEventChannelArtifactUpdated, updated.ChannelID, map[string]any{
		"artifact": updated,
	})
	c.JSON(http.StatusOK, updated)
}

// readPostArtifactBody authorizes an HTML attachment and returns its bytes.
func (a *API) readPostArtifactBody(c *gin.Context, userID, fileID, postID, channelID string) (body []byte, info *model.FileInfo, post *model.Post, authorDisplay, teamID string, limit int64, ok bool) {
	client, info, post, ok := a.lookupPostArtifact(c, userID, fileID)
	if !ok {
		return nil, nil, nil, "", "", 0, false
	}
	if post.Id != postID || post.ChannelId != channelID {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, nil, "", "", 0, false
	}
	limit = artifactRenderLimit(client.GetConfig())
	if info.Size > limit {
		c.String(http.StatusRequestEntityTooLarge, "artifact too large")
		return nil, nil, nil, "", "", 0, false
	}
	reader, err := client.GetFile(fileID)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return nil, nil, nil, "", "", 0, false
	}
	defer reader.Close()
	body, err = io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return nil, nil, nil, "", "", 0, false
	}
	if int64(len(body)) > limit {
		c.String(http.StatusRequestEntityTooLarge, "artifact too large")
		return nil, nil, nil, "", "", 0, false
	}
	if a.bots != nil {
		if bot := a.bots.GetBotByIDOrUsername(post.UserId, ""); bot != nil {
			authorDisplay = bot.GetMMBot().DisplayName
		}
	}
	if authorDisplay == "" {
		if user, err := client.GetUser(post.UserId); err == nil && user != nil {
			authorDisplay = user.GetDisplayName(model.ShowNicknameFullName)
		}
	}
	if channel, _ := client.GetChannel(channelID); channel != nil {
		teamID = channel.TeamId
	}
	return body, info, post, authorDisplay, teamID, limit, true
}

func (a *API) handlePatchChannelArtifact(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	id := c.Param("id")
	if !model.IsValidId(id) {
		c.String(http.StatusBadRequest, "invalid id")
		return
	}
	var req channelartifacts.PatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid request")
		return
	}
	entry, err := a.channelArtifacts.Patch(c.GetHeader("Mattermost-User-Id"), id, req)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	a.channelArtifacts.Publish(WebsocketEventChannelArtifactUpdated, entry.ChannelID, map[string]any{
		"artifact": entry,
	})
	c.JSON(http.StatusOK, entry)
}

func (a *API) handleRollbackChannelArtifact(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	id := c.Param("id")
	if !model.IsValidId(id) {
		c.String(http.StatusBadRequest, "invalid id")
		return
	}
	var req channelartifacts.RollbackRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Version < 1 {
		c.String(http.StatusBadRequest, "invalid request")
		return
	}
	entry, err := a.channelArtifacts.Rollback(c.GetHeader("Mattermost-User-Id"), id, req.Version)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	a.channelArtifacts.Publish(WebsocketEventChannelArtifactUpdated, entry.ChannelID, map[string]any{
		"artifact": entry,
	})
	c.JSON(http.StatusOK, entry)
}

func (a *API) handleDeleteChannelArtifact(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	id := c.Param("id")
	if !model.IsValidId(id) {
		c.String(http.StatusBadRequest, "invalid id")
		return
	}
	userID := c.GetHeader("Mattermost-User-Id")
	entry, err := a.channelArtifacts.GetReadable(userID, id)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	channelID := entry.ChannelID
	purge := strings.EqualFold(c.Query("purge"), "true") || c.Query("purge") == "1"
	hadMultiple := false
	if !purge {
		if versions, err := a.channelArtifacts.ListVersions(userID, id); err == nil {
			hadMultiple = len(versions) > 1
		}
	}
	if err := a.channelArtifacts.Delete(userID, id, purge); err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	if !purge && hadMultiple {
		updated, err := a.channelArtifacts.GetReadable(userID, id)
		if err == nil {
			a.channelArtifacts.Publish(WebsocketEventChannelArtifactUpdated, channelID, map[string]any{
				"artifact": updated,
			})
			c.JSON(http.StatusOK, updated)
			return
		}
	}
	a.channelArtifacts.Publish(WebsocketEventChannelArtifactDeleted, channelID, map[string]any{
		"id": id,
	})
	c.Status(http.StatusNoContent)
}

func (a *API) handleReorderChannelArtifacts(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	var req channelartifacts.OrderRequest
	if err := c.ShouldBindJSON(&req); err != nil || !model.IsValidId(req.ChannelID) {
		c.String(http.StatusBadRequest, "invalid request")
		return
	}
	if err := a.channelArtifacts.Reorder(c.GetHeader("Mattermost-User-Id"), req); err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	a.channelArtifacts.Publish(WebsocketEventChannelArtifactReordered, req.ChannelID, map[string]any{
		"ids": req.IDs,
	})
	c.Status(http.StatusNoContent)
}

func (a *API) handleListChannelArtifactVersions(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	id := c.Param("id")
	if !model.IsValidId(id) {
		c.String(http.StatusBadRequest, "invalid id")
		return
	}
	versions, err := a.channelArtifacts.ListVersions(c.GetHeader("Mattermost-User-Id"), id)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

func (a *API) handleExportChannelArtifacts(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	userID := c.GetHeader("Mattermost-User-Id")
	if !isSystemAdmin(a.pluginAPI, userID) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}
	channelID := c.Query("channel_id")
	if !model.IsValidId(channelID) {
		c.String(http.StatusBadRequest, "invalid channel id")
		return
	}
	resp, err := a.channelArtifacts.ExportChannel(channelID)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (a *API) handleListTeamChannelArtifacts(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	teamID := c.Query("team_id")
	if !model.IsValidId(teamID) {
		c.String(http.StatusBadRequest, "invalid team id")
		return
	}
	userID := c.GetHeader("Mattermost-User-Id")
	if a.pluginAPI == nil {
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	if !isSystemAdmin(a.pluginAPI, userID) &&
		!a.pluginAPI.User.HasPermissionToTeam(userID, teamID, model.PermissionManageTeam) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}
	resp, err := a.channelArtifacts.ListByTeam(teamID)
	if err != nil {
		a.writeChannelArtifactError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// libraryArtifactBridgeToken authenticates bridge messages for a library entry.
func libraryArtifactBridgeToken(secret []byte, userID, artifactID, nonce string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("html-artifact-bridge:v3|library|" + userID + "|" + artifactID + "|" + nonce))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *API) authorizeLibraryArtifact(c *gin.Context) (*channelartifacts.Entry, bool) {
	h := c.Writer.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")

	if !a.channelArtifactsEnabled() || a.channelArtifacts == nil {
		c.String(http.StatusNotFound, "not found")
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

func parseArtifactVersionQuery(c *gin.Context) (int, bool) {
	raw := c.Query("version")
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
	if a.config.DisableChannelArtifactRendering() {
		c.String(http.StatusForbidden, "channel artifact rendering is disabled")
		return
	}
	entry, ok := a.authorizeLibraryArtifact(c)
	if !ok {
		return
	}
	nonce, ok := artifactNonce(c)
	if !ok {
		return
	}
	secret, err := a.artifactSecret()
	if err != nil {
		a.logArtifactError("Failed to load artifact bridge secret", err, entry.ID)
		c.String(http.StatusInternalServerError, "failed to prepare artifact")
		return
	}
	token := libraryArtifactBridgeToken(secret, c.GetHeader("Mattermost-User-Id"), entry.ID, nonce)
	c.JSON(http.StatusOK, gin.H{"token": token, "tooLarge": entry.Size > artifactRenderLimit(a.mmClient.GetConfig())})
}

func (a *API) handleGetChannelArtifact(c *gin.Context) {
	entry, ok := a.authorizeLibraryArtifact(c)
	if !ok {
		return
	}

	if a.config.DisableChannelArtifactRendering() {
		if !strings.EqualFold(c.GetHeader("Sec-Fetch-Dest"), "iframe") || !strings.EqualFold(c.GetHeader("Sec-Fetch-Site"), "same-origin") {
			h := c.Writer.Header()
			h.Set("Content-Security-Policy", artifactInterstitialCSP)
			h.Set("Referrer-Policy", "no-referrer")
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(artifactRenderingDisabledHTML))
			return
		}
		c.String(http.StatusForbidden, "channel artifact rendering is disabled")
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
	limit := artifactRenderLimit(cfg)
	if entry.Size > limit && version == 0 {
		c.String(http.StatusRequestEntityTooLarge, "artifact too large")
		return
	}

	secret, err := a.artifactSecret()
	if err != nil {
		a.logArtifactError("Failed to load artifact bridge secret", err, entry.ID)
		c.String(http.StatusInternalServerError, "failed to prepare artifact")
		return
	}
	token := libraryArtifactBridgeToken(secret, c.GetHeader("Mattermost-User-Id"), entry.ID, nonce)

	body, err := a.channelArtifacts.ReadContentAt(entry, version)
	if err != nil {
		a.logArtifactError("Failed to read channel artifact", err, entry.ID)
		a.writeChannelArtifactError(c, err)
		return
	}
	if int64(len(body)) > limit {
		c.String(http.StatusRequestEntityTooLarge, "artifact too large")
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
	if a.config.DisableChannelArtifactDownload() {
		c.String(http.StatusForbidden, "channel artifact download is disabled")
		return
	}
	entry, ok := a.authorizeLibraryArtifact(c)
	if !ok {
		return
	}
	version, ok := parseArtifactVersionQuery(c)
	if !ok {
		return
	}
	body, err := a.channelArtifacts.ReadContentAt(entry, version)
	if err != nil {
		a.logArtifactError("Failed to read channel artifact", err, entry.ID)
		a.writeChannelArtifactError(c, err)
		return
	}
	name := entry.SourceFileName
	if name == "" {
		name = entry.Title + ".html"
	}
	h := c.Writer.Header()
	h.Set("Content-Disposition", "attachment; filename=\""+strings.ReplaceAll(name, "\"", "")+"\"")
	h.Set("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/html; charset=utf-8", body)
}

// lookupPostArtifact runs the same authorization as authorizeArtifact but
// returns the post as well. On failure it writes the response.
func (a *API) lookupPostArtifact(c *gin.Context, userID, fileID string) (client mmapi.Client, info *model.FileInfo, post *model.Post, ok bool) {
	h := c.Writer.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")

	if !a.config.EnableHTMLArtifacts() {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, nil, false
	}
	if !model.IsValidId(fileID) {
		c.String(http.StatusBadRequest, "invalid file id")
		return nil, nil, nil, false
	}
	if a.mmClient == nil {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, nil, false
	}
	client = mmapi.WithFilePolicy(a.mmClient, auth.SessionIDFromContext(c.Request.Context()))
	info, err := client.GetFileInfo(fileID)
	if err != nil || info == nil || !isArtifactExtension(info) {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, nil, false
	}
	if info.PostId == "" {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, nil, false
	}
	post, err = client.GetPost(info.PostId)
	if err != nil || post == nil || !slices.Contains(post.FileIds, fileID) {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, nil, false
	}
	if !client.HasPermissionToChannel(userID, post.ChannelId, model.PermissionReadChannel) {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, nil, false
	}
	if a.config.DisableHumanChannelArtifacts() {
		if a.bots == nil || !a.bots.IsAnyBot(post.UserId) {
			c.String(http.StatusNotFound, "not found")
			return nil, nil, nil, false
		}
	}
	return client, info, post, true
}
