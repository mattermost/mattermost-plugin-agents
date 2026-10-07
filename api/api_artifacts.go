// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	_ "embed"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost/server/public/model"
)

//go:embed artifact_bridge.js
var artifactBridgeJS string

// artifactCSP gives the document an opaque origin (sandbox) even when opened
// directly in a tab, and blocks all network and external resources.
const artifactCSP = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; " +
	"style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; " +
	"connect-src 'none'; frame-src 'none'; worker-src 'none'; object-src 'none'; " +
	"form-action 'none'; base-uri 'none'; frame-ancestors 'self'"

// injectArtifactBridge inserts the bridge script at the earliest safe point
// of the document: after any leading BOM, whitespace, doctype, comments and the
// opening <html> and/or <head> tags. It never scans past the first other
// content, so a "<head>" inside a comment or a later script is never matched.
func injectArtifactBridge(doc []byte) []byte {
	script := "<script>" + artifactBridgeJS + "</script>"
	insertAt := artifactBridgeInsertPos(doc)
	out := make([]byte, 0, len(doc)+len(script))
	out = append(out, doc[:insertAt]...)
	out = append(out, script...)
	out = append(out, doc[insertAt:]...)
	return out
}

func artifactBridgeInsertPos(doc []byte) int {
	i := 0
	if bytes.HasPrefix(doc, []byte("\xEF\xBB\xBF")) {
		i = 3
	}
	skipSpace := func() {
		for i < len(doc) && (doc[i] == ' ' || doc[i] == '\t' || doc[i] == '\n' || doc[i] == '\r' || doc[i] == '\f') {
			i++
		}
	}
	// Skip the prolog: whitespace, comments and doctype.
	for {
		skipSpace()
		rest := doc[i:]
		if bytes.HasPrefix(rest, []byte("<!--")) {
			end := bytes.Index(rest[4:], []byte("-->"))
			if end < 0 {
				return i
			}
			i += 4 + end + 3
			continue
		}
		if hasTagPrefixFold(rest, "<!doctype") {
			end := bytes.IndexByte(rest, '>')
			if end < 0 {
				return i
			}
			i += end + 1
			continue
		}
		break
	}
	for _, tag := range []string{"<html", "<head"} {
		saved := i
		for {
			skipSpace()
			if !bytes.HasPrefix(doc[i:], []byte("<!--")) {
				break
			}
			end := bytes.Index(doc[i+4:], []byte("-->"))
			if end < 0 {
				break
			}
			i += 4 + end + 3
		}
		if !hasTagPrefixFold(doc[i:], tag) {
			i = saved
			continue
		}
		end := bytes.IndexByte(doc[i:], '>')
		if end < 0 {
			i = saved
			break
		}
		i += end + 1
	}
	return i
}

// hasTagPrefixFold reports whether b starts with tag (case-insensitive)
// followed by whitespace, '/', or '>' (so "<header" does not match "<head").
func hasTagPrefixFold(b []byte, tag string) bool {
	if len(b) <= len(tag) || !bytes.EqualFold(b[:len(tag)], []byte(tag)) {
		return false
	}
	switch b[len(tag)] {
	case ' ', '\t', '\n', '\r', '\f', '/', '>':
		return true
	}
	return false
}

func isArtifactExtension(info *model.FileInfo) bool {
	ext := strings.ToLower(strings.TrimPrefix(info.Extension, "."))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(info.Name), "."))
	}
	return ext == "html" || ext == "htm"
}

// handleGetArtifact serves an agent-created HTML file as a sandboxed artifact
// with the bridge script injected. Read-only, so it is not audited.
func (a *API) handleGetArtifact(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")

	if !a.config.EnableHTMLArtifacts() {
		c.String(http.StatusNotFound, "not found")
		return
	}

	fileID := c.Param("fileid")
	if !model.IsValidId(fileID) {
		c.String(http.StatusBadRequest, "invalid file id")
		return
	}

	if a.mmClient == nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	userID := c.GetHeader("Mattermost-User-Id")
	// Respect the viewer's file-download access policy.
	client := mmapi.WithFilePolicy(a.mmClient, auth.SessionIDFromContext(c.Request.Context()))

	info, err := client.GetFileInfo(fileID)
	if err != nil || info == nil || !isArtifactExtension(info) {
		c.String(http.StatusNotFound, "not found")
		return
	}

	if info.PostId == "" {
		c.String(http.StatusNotFound, "not found")
		return
	}
	post, err := client.GetPost(info.PostId)
	if err != nil || post == nil || !slices.Contains(post.FileIds, fileID) {
		c.String(http.StatusNotFound, "not found")
		return
	}

	// Check access before revealing anything about the file; 404 so as not
	// to confirm the artifact exists.
	if !client.HasPermissionToChannel(userID, post.ChannelId, model.PermissionReadChannel) {
		c.String(http.StatusNotFound, "not found")
		return
	}

	if a.bots == nil || !a.bots.IsAnyBot(post.UserId) {
		c.String(http.StatusNotFound, "not found")
		return
	}

	limit := mmtools.CreateFileContentLimit(client.GetConfig())
	if info.Size > limit {
		c.String(http.StatusRequestEntityTooLarge, "artifact too large")
		return
	}

	reader, err := client.GetFile(fileID)
	if err != nil {
		a.logArtifactError("Failed to open artifact file", err, fileID)
		c.String(http.StatusInternalServerError, "failed to read artifact")
		return
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		a.logArtifactError("Failed to read artifact file", err, fileID)
		c.String(http.StatusInternalServerError, "failed to read artifact")
		return
	}
	if int64(len(body)) > limit {
		c.String(http.StatusRequestEntityTooLarge, "artifact too large")
		return
	}

	h.Set("Content-Security-Policy", artifactCSP)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Content-Disposition", "inline")
	c.Data(http.StatusOK, "text/html; charset=utf-8", injectArtifactBridge(body))
}

func (a *API) logArtifactError(msg string, err error, fileID string) {
	if a.pluginAPI != nil {
		a.pluginAPI.Log.Error(msg, "error", err, "file_id", fileID)
	}
}
