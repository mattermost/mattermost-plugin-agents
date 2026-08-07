// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

func (a *API) handleOAuthStart(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	serverName := c.Param("serverName")
	// Recorded before any validation so every fail path carries the target
	// server. Never audit the resource_metadata query value or the provider
	// auth URL: both can embed client IDs and PKCE parameters.
	audit.AddParam(auditRec(c), audit.KeyMCPServer, audit.TruncateID(serverName))
	if serverName == "" {
		a.renderOAuthErrorPage(c, http.StatusBadRequest, "Authorization Failed", "Missing MCP server name.")
		return
	}

	oauthManager := a.mcpClientManager.GetOAuthManager()
	if oauthManager == nil {
		a.pluginAPI.Log.Error("OAuth manager is not configured")
		a.renderOAuthErrorPage(c, http.StatusInternalServerError, "Authorization Failed", "OAuth is not configured for this plugin.")
		return
	}

	serverConfig, ok := a.getMCPServerConfig(serverName)
	if !ok {
		a.renderOAuthErrorPage(c, http.StatusNotFound, "Authorization Failed", "The selected MCP server was not found.")
		return
	}
	if !serverConfig.Enabled || serverConfig.BaseURL == "" {
		a.renderOAuthErrorPage(c, http.StatusBadRequest, "Authorization Failed", "The selected MCP server is not available for OAuth.")
		return
	}

	metadataURL := c.Query("resource_metadata")
	if metadataURL != "" {
		if err := mcp.ValidateResourceMetadataURL(metadataURL); err != nil {
			a.pluginAPI.Log.Debug("Rejected MCP OAuth start resource_metadata query", "serverName", serverConfig.Name, "error", err)
			a.renderOAuthErrorPage(c, http.StatusBadRequest, "Authorization Failed", "Invalid resource metadata URL.")
			return
		}
		if err := mcp.ValidateResourceMetadataMatchesServerBaseURL(serverConfig.BaseURL, metadataURL); err != nil {
			a.pluginAPI.Log.Debug("Rejected MCP OAuth start resource_metadata origin mismatch", "serverName", serverConfig.Name, "error", err)
			a.renderOAuthErrorPage(c, http.StatusBadRequest, "Authorization Failed", "Invalid resource metadata URL.")
			return
		}
	}

	authURL, err := oauthManager.InitiateOAuthFlowForServerWithMetadata(c.Request.Context(), userID, serverConfig, metadataURL)
	if err != nil {
		a.pluginAPI.Log.Error("Failed to start OAuth flow", "serverName", serverConfig.Name, "error", err)
		a.renderOAuthErrorPage(c, http.StatusInternalServerError, "Authorization Failed", "Unable to start the MCP authorization flow.")
		return
	}

	c.Redirect(http.StatusFound, authURL)
}

// normalizeOAuthErrorCode clamps an OAuth authorization error to the RFC 6749
// section 4.1.2.1 enum. Anything else becomes "other" so redirect-controlled
// text cannot be injected into audit records.
func normalizeOAuthErrorCode(code string) string {
	switch code {
	case "invalid_request", "unauthorized_client", "access_denied",
		"unsupported_response_type", "invalid_scope", "server_error",
		"temporarily_unavailable":
		return code
	default:
		return "other"
	}
}

func (a *API) handleOAuthCallback(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	state := c.Query("state")
	code := c.Query("code")
	errorParam := c.Query("error")

	if errorParam != "" {
		errorDescription := c.Query("error_description")
		// Only the spec-defined error code enum (e.g. "access_denied") is
		// audited; error_description is provider-controlled free text and
		// must never enter the record. The error code itself is clamped to
		// the RFC 6749 enum so a crafted redirect cannot inject arbitrary
		// text into the audit log either.
		audit.AddParam(auditRec(c), "provider_error", normalizeOAuthErrorCode(errorParam))
		a.pluginAPI.Log.Error("OAuth authorization failed", "error", errorParam, "description", errorDescription)
		a.renderOAuthWindowClosePage(c, http.StatusBadRequest, "Authorization Failed")
		return
	}

	if state == "" || code == "" {
		a.pluginAPI.Log.Error("Missing required OAuth parameters", "state", state, "code", code)
		a.renderOAuthWindowClosePage(c, http.StatusBadRequest, "Authorization Failed")
		return
	}

	session, err := a.mcpClientManager.ProcessOAuthCallback(c.Request.Context(), userID, state, code)
	if err != nil {
		a.pluginAPI.Log.Error("Failed to process OAuth callback", "error", err)
		a.renderOAuthWindowClosePage(c, http.StatusInternalServerError, "Authorization Failed")
		return
	}

	// The callback URL carries no server name; the session resolved from the
	// state is the only source of which server the user just granted.
	audit.AddParam(auditRec(c), audit.KeyMCPServer, session.ServerID)

	a.publishMCPOAuthClusterInvalidation(userID)
	a.publishMCPConnectionUpdated(userID, session)
	a.renderOAuthWindowClosePage(c, http.StatusOK, "Authorization Successful")
}

// publishMCPOAuthClusterInvalidation notifies peer nodes to drop stale per-user MCP clients.
func (a *API) publishMCPOAuthClusterInvalidation(userID string) {
	if a.mcpOAuthNotifier == nil || userID == "" {
		return
	}

	if err := a.mcpOAuthNotifier.PublishMCPOAuthUpdate(userID); err != nil {
		if a.pluginAPI != nil {
			a.pluginAPI.Log.Warn("Failed to publish MCP OAuth cluster invalidation", "userID", userID, "error", err)
		}
	}
}

// publishMCPConnectionUpdated notifies the webapp that the user connected an MCP server (OAuth callback).
func (a *API) publishMCPConnectionUpdated(userID string, session *mcp.OAuthSession) {
	if a.mmClient == nil || userID == "" {
		return
	}

	payload := map[string]interface{}{
		"status": "connected",
	}
	if session != nil {
		if session.ServerID != "" {
			payload["serverName"] = session.ServerID
		}
		if session.ServerURL != "" {
			payload["serverOrigin"] = session.ServerURL
		}
	}

	a.mmClient.PublishWebSocketEvent(
		WebsocketEventMCPConnectionUpdated,
		payload,
		&model.WebsocketBroadcast{UserId: userID},
	)
}

func (a *API) getMCPServerConfig(serverName string) (mcp.ServerConfig, bool) {
	mcpCfg := a.config.MCP()
	for _, serverConfig := range mcpCfg.Servers {
		if serverConfig.Name == serverName {
			return serverConfig, true
		}
	}

	return mcp.ServerConfig{}, false
}

func (a *API) renderOAuthWindowClosePage(c *gin.Context, statusCode int, title string) {
	c.Header("Content-Type", "text/html")
	c.String(statusCode, `<!DOCTYPE html>
<html>
<head>
	<title>`+template.HTMLEscapeString(title)+`</title>
</head>
<body>
	<script>
		window.close();
	</script>
</body>
</html>`)
}

func (a *API) renderOAuthErrorPage(c *gin.Context, statusCode int, title, message string) {
	c.Header("Content-Type", "text/html")
	c.String(statusCode, `<!DOCTYPE html>
<html>
<head>
	<title>`+template.HTMLEscapeString(title)+`</title>
</head>
<body>
	<h1>`+template.HTMLEscapeString(title)+`</h1>
	<p>`+template.HTMLEscapeString(message)+`</p>
</body>
</html>`)
}
