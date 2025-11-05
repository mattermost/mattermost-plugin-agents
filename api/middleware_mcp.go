// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// mcpAuthMiddleware handles authentication for MCP endpoints
// It extracts and validates the user ID from the Mattermost-User-Id header
func (a *API) mcpAuthMiddleware(c *gin.Context) {
	// Get user ID from header (set by Mattermost)
	userID := c.GetHeader("Mattermost-User-Id")
	if userID == "" {
		a.sendMCPUnauthorized(c)
		return
	}

	// Store user ID for the handler
	c.Set("userID", userID)
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
