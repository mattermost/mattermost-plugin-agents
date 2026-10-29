// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

//go:embed artifact_bridge.js
var artifactBridgeJS string

// artifactInterstitialCSP locks down the static page served instead of the
// artifact on a top-level visit: no scripts, no subresources.
const artifactInterstitialCSP = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// artifactInterstitialHTML is served instead of the artifact outside the
// host's iframe. It contains no artifact content and no script.
const artifactInterstitialHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>AI-generated artifact</title>` +
	`<style>body{font-family:sans-serif;margin:48px auto;max-width:480px;padding:0 16px;color:#3f4350}</style></head>` +
	`<body><p>This AI-generated artifact can only be viewed inside Mattermost.</p></body></html>`

// artifactCSP gives the document an opaque origin (sandbox) even when opened
// directly in a tab, and blocks all network and external resources.
const artifactCSP = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; " +
	"style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; " +
	"connect-src 'none'; frame-src 'none'; worker-src 'none'; object-src 'none'; " +
	"form-action 'none'; base-uri 'none'; frame-ancestors 'self'"

// artifactConnectionAllowlist allows the artifact no connections at all:
// with an empty list, WebRTC and navigations/redirects are blocked too, and
// local-scheme children (srcdoc, data:, blob:) inherit it. CSP cannot block
// WebRTC. Honored by Chromium 152+; other browsers ignore it.
const artifactConnectionAllowlist = "()"

// artifactPermissionsPolicy denies the features an opaque-origin frame would
// otherwise still get by default, plus powerful features as defense in depth.
const artifactPermissionsPolicy = "accelerometer=(), " +
	"ambient-light-sensor=(), " +
	"attribution-reporting=(), " +
	"autoplay=(), " +
	"bluetooth=(), " +
	"browsing-topics=(), " +
	"camera=(), " +
	"clipboard-read=(), " +
	"clipboard-write=(), " +
	"compute-pressure=(), " +
	"display-capture=(), " +
	"encrypted-media=(), " +
	"fullscreen=(), " +
	"gamepad=(), " +
	"geolocation=(), " +
	"gyroscope=(), " +
	"hid=(), " +
	"identity-credentials-get=(), " +
	"idle-detection=(), " +
	"join-ad-interest-group=(), " +
	"local-fonts=(), " +
	"magnetometer=(), " +
	"microphone=(), " +
	"midi=(), " +
	"otp-credentials=(), " +
	"payment=(), " +
	"picture-in-picture=(), " +
	"private-aggregation=(), " +
	"private-state-token-issuance=(), " +
	"private-state-token-redemption=(), " +
	"publickey-credentials-create=(), " +
	"publickey-credentials-get=(), " +
	"run-ad-auction=(), " +
	"screen-wake-lock=(), " +
	"serial=(), " +
	"shared-storage=(), " +
	"shared-storage-select-url=(), " +
	"storage-access=(), " +
	"sync-xhr=(), " +
	"unload=(), " +
	"usb=(), " +
	"web-share=(), " +
	"window-management=(), " +
	"xr-spatial-tracking=()"

// artifactBridgeTokenPlaceholder is replaced in artifact_bridge.js with the
// viewer's bridge token as a JSON string literal.
const artifactBridgeTokenPlaceholder = "/*MM_ARTIFACT_BRIDGE_TOKEN*/null" //nolint:gosec // placeholder, not a credential

// artifactBridgeTargetOriginPlaceholder is replaced in artifact_bridge.js
// with the origin the bridge posts to, as a JSON string literal.
const artifactBridgeTargetOriginPlaceholder = "/*MM_ARTIFACT_BRIDGE_TARGET_ORIGIN*/null"

// artifactSecretKVKey holds the plugin-wide HMAC secret for bridge tokens.
const artifactSecretKVKey = "html_artifact_bridge_secret_v1" //nolint:gosec // KV key name, not a credential

const artifactSecretSize = 32

// artifactBridgeScript returns the bridge script with token and targetOrigin
// embedded as JSON string literals. json.Marshal escapes <, > and & (and
// U+2028/U+2029), so a literal can never close the script element or the
// string, and the single-pass replacer never rescans an inserted literal.
func artifactBridgeScript(token, targetOrigin string) string {
	return "<script>" + strings.NewReplacer(
		artifactBridgeTokenPlaceholder, jsStringLiteral(token),
		artifactBridgeTargetOriginPlaceholder, jsStringLiteral(targetOrigin),
	).Replace(artifactBridgeJS) + "</script>"
}

func jsStringLiteral(s string) string {
	literal, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(literal)
}

// artifactTargetOrigin returns the origin of the Mattermost site URL, which
// is the host page's origin, or "*" when it is unset or unusable.
func artifactTargetOrigin(cfg *model.Config) string {
	if cfg == nil || cfg.ServiceSettings.SiteURL == nil {
		return "*"
	}
	u, err := url.Parse(*cfg.ServiceSettings.SiteURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "*"
	}
	return u.Scheme + "://" + u.Host
}

// artifactBridgeToken derives the token that authenticates bridge messages
// for one viewer, one artifact file and one host load of its frame (nonce).
func artifactBridgeToken(secret []byte, userID, fileID, nonce string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("html-artifact-bridge:v2|" + userID + "|" + fileID + "|" + nonce))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// artifactNonceRe matches the host's per-load nonce: 16 random bytes,
// base64url without padding.
var artifactNonceRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// artifactNonce returns the request's nonce, which must be the only query
// parameter. On failure it writes a 400 and returns ok=false.
func artifactNonce(c *gin.Context) (string, bool) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["n"]) != 1 || !artifactNonceRe.MatchString(query["n"][0]) {
		c.String(http.StatusBadRequest, "invalid request")
		return "", false
	}
	return query["n"][0], true
}

// artifactSecret returns the plugin-wide bridge token secret, creating it on
// first use. Creation is an atomic set-if-absent followed by a read back, so
// every cluster node ends up with the same secret.
func (a *API) artifactSecret() ([]byte, error) {
	a.artifactSecretMu.Lock()
	defer a.artifactSecretMu.Unlock()
	if a.artifactSecretCache != nil {
		return a.artifactSecretCache, nil
	}
	if a.pluginAPI == nil {
		return nil, errors.New("plugin API unavailable")
	}

	var secret []byte
	if err := a.pluginAPI.KV.Get(artifactSecretKVKey, &secret); err != nil {
		return nil, err
	}
	if len(secret) != artifactSecretSize {
		fresh := make([]byte, artifactSecretSize)
		if _, err := rand.Read(fresh); err != nil {
			return nil, err
		}
		if _, err := a.pluginAPI.KV.Set(artifactSecretKVKey, fresh, pluginapi.SetAtomic(nil)); err != nil {
			return nil, err
		}
		// Another node may have won the race; use whatever is stored.
		secret = nil
		if err := a.pluginAPI.KV.Get(artifactSecretKVKey, &secret); err != nil {
			return nil, err
		}
		if len(secret) != artifactSecretSize {
			return nil, errors.New("invalid html artifact bridge secret")
		}
	}
	a.artifactSecretCache = secret
	return secret, nil
}

// injectArtifactBridge inserts the bridge script before any author content:
// after a leading BOM, whitespace, comments and the doctype, and before
// <html>/<head>. The bridge must run before every artifact-authored script
// so it can capture window.parent and intrinsics before artifact code can
// shadow them. The HTML parser accepts a script before <html> (it creates the
// html and head elements implicitly and merges a later <html>'s attributes),
// the doctype stays first so standards mode is kept, and the charset comes
// from the Content-Type header.
func injectArtifactBridge(doc []byte, token, targetOrigin string) []byte {
	script := artifactBridgeScript(token, targetOrigin)
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
	for {
		for i < len(doc) && (doc[i] == ' ' || doc[i] == '\t' || doc[i] == '\n' || doc[i] == '\r' || doc[i] == '\f') {
			i++
		}
		rest := doc[i:]
		if bytes.HasPrefix(rest, []byte("<!--")) {
			end := commentEnd(rest)
			if end < 0 {
				return i
			}
			i += end
			continue
		}
		if hasTagPrefixFold(rest, "<!doctype") {
			// '>' always ends a doctype, even inside a quoted identifier.
			end := bytes.IndexByte(rest, '>')
			if end < 0 {
				return i
			}
			i += end + 1
			continue
		}
		return i
	}
}

// commentEnd returns the length of the comment at the start of b (which
// begins with "<!--"), following the HTML tokenizer: "<!-->" and "<!--->"
// close immediately, otherwise the first "-->" or "--!>" closes it. It
// returns -1 for an unterminated comment.
func commentEnd(b []byte) int {
	if bytes.HasPrefix(b, []byte("<!-->")) {
		return 5
	}
	if bytes.HasPrefix(b, []byte("<!--->")) {
		return 6
	}
	end := -1
	for _, closer := range []string{"-->", "--!>"} {
		if j := bytes.Index(b[4:], []byte(closer)); j >= 0 && (end < 0 || 4+j+len(closer) < end) {
			end = 4 + j + len(closer)
		}
	}
	return end
}

// hasTagPrefixFold reports whether b starts with tag (case-insensitive)
// followed by whitespace, '/', or '>'.
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

// maxArtifactRenderBytes caps how much of an artifact is read into memory to
// render it, independent of the (possibly much larger) file size limit.
const maxArtifactRenderBytes int64 = 5 * 1024 * 1024

// artifactRenderLimit is the largest artifact the content route will serve.
func artifactRenderLimit(cfg *model.Config) int64 {
	return min(maxArtifactRenderBytes, mmtools.CreateFileContentLimit(cfg))
}

func isArtifactExtension(info *model.FileInfo) bool {
	ext := strings.ToLower(strings.TrimPrefix(info.Extension, "."))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(info.Name), "."))
	}
	return ext == "html" || ext == "htm"
}

// authorizeArtifact runs the checks shared by every artifact route: the
// feature is enabled, the file is HTML, attached to a bot post, and readable
// by the viewer. On failure it writes the response and returns ok=false.
// Failures are 404 so as not to confirm the artifact exists.
func (a *API) authorizeArtifact(c *gin.Context) (client mmapi.Client, info *model.FileInfo, ok bool) {
	h := c.Writer.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")

	if !a.config.EnableHTMLArtifacts() {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, false
	}

	fileID := c.Param("fileid")
	if !model.IsValidId(fileID) {
		c.String(http.StatusBadRequest, "invalid file id")
		return nil, nil, false
	}

	if a.mmClient == nil {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, false
	}
	userID := c.GetHeader("Mattermost-User-Id")
	// Respect the viewer's file-download access policy.
	client = mmapi.WithFilePolicy(a.mmClient, auth.SessionIDFromContext(c.Request.Context()))

	info, err := client.GetFileInfo(fileID)
	if err != nil || info == nil || !isArtifactExtension(info) {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, false
	}

	if info.PostId == "" {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, false
	}
	post, err := client.GetPost(info.PostId)
	if err != nil || post == nil || !slices.Contains(post.FileIds, fileID) {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, false
	}

	// Check access before revealing anything about the file.
	if !client.HasPermissionToChannel(userID, post.ChannelId, model.PermissionReadChannel) {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, false
	}

	if a.bots == nil || !a.bots.IsAnyBot(post.UserId) {
		c.String(http.StatusNotFound, "not found")
		return nil, nil, false
	}

	return client, info, true
}

// viewerArtifactToken returns the bridge token for the requesting viewer,
// fileID and nonce, logging and writing a 500 on failure.
func (a *API) viewerArtifactToken(c *gin.Context, fileID, nonce string) (string, bool) {
	secret, err := a.artifactSecret()
	if err != nil {
		a.logArtifactError("Failed to load artifact bridge secret", err, fileID)
		c.String(http.StatusInternalServerError, "failed to prepare artifact")
		return "", false
	}
	return artifactBridgeToken(secret, c.GetHeader("Mattermost-User-Id"), fileID, nonce), true
}

// handleGetArtifactToken returns the token the viewer's host page uses to
// authenticate messages from the artifact's bridge. Read-only, so it is not
// audited.
// tooLarge tells the host the content route would refuse the artifact, so it
// can say so instead of loading the frame.
func (a *API) handleGetArtifactToken(c *gin.Context) {
	client, info, ok := a.authorizeArtifact(c)
	if !ok {
		return
	}
	nonce, ok := artifactNonce(c)
	if !ok {
		return
	}
	token, ok := a.viewerArtifactToken(c, c.Param("fileid"), nonce)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "tooLarge": info.Size > artifactRenderLimit(client.GetConfig())})
}

// handleGetArtifact serves an agent-created HTML file as a sandboxed artifact
// with the bridge script injected. Read-only, so it is not audited.
func (a *API) handleGetArtifact(c *gin.Context) {
	client, info, ok := a.authorizeArtifact(c)
	if !ok {
		return
	}
	fileID := c.Param("fileid")

	// Only render inside the host's frame, where the safety notice and
	// navigation guard exist. A top-level visit (e.g. the frame URL opened in
	// a new tab) would let attacker-authored UI fill a tab on this origin.
	// The host's wrapper frame is same-origin, while the artifact navigating
	// its own frame (opaque initiator) is cross-site.
	if !strings.EqualFold(c.GetHeader("Sec-Fetch-Dest"), "iframe") || !strings.EqualFold(c.GetHeader("Sec-Fetch-Site"), "same-origin") {
		h := c.Writer.Header()
		h.Set("Content-Security-Policy", artifactInterstitialCSP)
		h.Set("Referrer-Policy", "no-referrer")
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(artifactInterstitialHTML))
		return
	}

	nonce, ok := artifactNonce(c)
	if !ok {
		return
	}

	cfg := client.GetConfig()
	limit := artifactRenderLimit(cfg)
	if info.Size > limit {
		c.String(http.StatusRequestEntityTooLarge, "artifact too large")
		return
	}

	token, ok := a.viewerArtifactToken(c, fileID, nonce)
	if !ok {
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

	h := c.Writer.Header()
	h.Set("Content-Security-Policy", artifactCSP)
	h.Set("Connection-Allowlist", artifactConnectionAllowlist)
	h.Set("Permissions-Policy", artifactPermissionsPolicy)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Content-Disposition", "inline")
	c.Data(http.StatusOK, "text/html; charset=utf-8", injectArtifactBridge(body, token, artifactTargetOrigin(cfg)))
}

func (a *API) logArtifactError(msg string, err error, fileID string) {
	if a.pluginAPI != nil {
		a.pluginAPI.Log.Error(msg, "error", err, "file_id", fileID)
	}
}
