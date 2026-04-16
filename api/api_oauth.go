// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
)

func (a *API) handleOAuthStart(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	serverName := c.Param("serverName")
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

func (a *API) handleOAuthCallback(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	state := c.Query("state")
	code := c.Query("code")
	errorParam := c.Query("error")

	if errorParam != "" {
		errorDescription := c.Query("error_description")
		a.pluginAPI.Log.Error("OAuth authorization failed", "error", errorParam, "description", errorDescription)
		a.renderOAuthWindowClosePage(c, http.StatusBadRequest, "Authorization Failed")
		return
	}

	if state == "" || code == "" {
		a.pluginAPI.Log.Error("Missing required OAuth parameters", "state", state, "code", code)
		a.renderOAuthWindowClosePage(c, http.StatusBadRequest, "Authorization Failed")
		return
	}

	_, err := a.mcpClientManager.ProcessOAuthCallback(c.Request.Context(), userID, state, code)
	if err != nil {
		a.pluginAPI.Log.Error("Failed to process OAuth callback", "error", err)
		a.renderOAuthWindowClosePage(c, http.StatusInternalServerError, "Authorization Failed")
		return
	}

	a.renderOAuthWindowClosePage(c, http.StatusOK, "Authorization Successful")
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
