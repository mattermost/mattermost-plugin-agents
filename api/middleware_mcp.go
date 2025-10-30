// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// mcpAuthMiddleware handles authentication for MCP endpoints
// It extracts the session ID from the plugin context and retrieves the session token
func (a *API) mcpAuthMiddleware(c *gin.Context) {
	// Extract plugin context
	pluginCtxValue, exists := c.Get("pluginContext")
	if !exists {
		a.sendMCPUnauthorized(c)
		return
	}

	pluginCtx, ok := pluginCtxValue.(*plugin.Context)
	if !ok || pluginCtx == nil {
		a.sendMCPUnauthorized(c)
		return
	}

	// Check if session ID exists
	if pluginCtx.SessionId == "" {
		a.sendMCPUnauthorized(c)
		return
	}

	// Get session from plugin API
	session, err := a.pluginAPI.Session.Get(pluginCtx.SessionId)
	if err != nil {
		a.pluginAPI.Log.Debug("Failed to get session for MCP request",
			"sessionId", pluginCtx.SessionId,
			"error", err)
		a.sendMCPUnauthorized(c)
		return
	}

	// Verify session has a token
	if session.Token == "" {
		a.pluginAPI.Log.Debug("Session has no token for MCP request",
			"sessionId", pluginCtx.SessionId)
		a.sendMCPUnauthorized(c)
		return
	}

	// Store token in context for handlers
	c.Set("mcpToken", session.Token)
	c.Next()
}

// sendMCPUnauthorized sends a 401 response with WWW-Authenticate header for OAuth discovery
func (a *API) sendMCPUnauthorized(c *gin.Context) {
	// Get site URL for OAuth metadata
	config := a.pluginAPI.Configuration.GetConfig()
	if config.ServiceSettings.SiteURL == nil || *config.ServiceSettings.SiteURL == "" {
		a.pluginAPI.Log.Error("Site URL not configured, cannot send OAuth metadata URL")
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	siteURL := *config.ServiceSettings.SiteURL
	// OAuth metadata is now under the plugin mcp-server path
	resourceMetadataURL := fmt.Sprintf("%s/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource", siteURL)

	// Set WWW-Authenticate header for OAuth discovery (RFC 9728)
	c.Header("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, resourceMetadataURL))
	c.AbortWithStatus(http.StatusUnauthorized)
}
