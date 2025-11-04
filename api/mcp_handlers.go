// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
)

// handleMCP delegates to the embedded MCP server for the /mcp endpoint
func (a *API) handleMCP(c *gin.Context) {
	if !a.config.MCP().EnablePluginServer {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	a.delegateToMCPServer(c, a.mcpHandlers.MCPHandler)
}

// handleSSE delegates to the embedded MCP server for the /sse endpoint
func (a *API) handleSSE(c *gin.Context) {
	if !a.config.MCP().EnablePluginServer {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	a.delegateToMCPServer(c, a.mcpHandlers.SSEHandler)
}

// handleMessage delegates to the embedded MCP server for the /message endpoint
func (a *API) handleMessage(c *gin.Context) {
	if !a.config.MCP().EnablePluginServer {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	a.delegateToMCPServer(c, a.mcpHandlers.MessageHandler)
}

// handleOAuthResourceMetadata delegates to the embedded MCP server for OAuth metadata
func (a *API) handleOAuthResourceMetadata(c *gin.Context) {
	if !a.config.MCP().EnablePluginServer {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	// OAuth metadata endpoint doesn't require authentication
	a.mcpHandlers.OAuthMetadataHandler(c.Writer, c.Request)
}

// delegateToMCPServer delegates the request to the embedded MCP server
// It injects the session token into the request context (not as a header)
// because the OAuth provider expects the token in context.WithValue
func (a *API) delegateToMCPServer(c *gin.Context, handler http.Handler) {
	// Get token from middleware (set by mcpAuthMiddleware)
	tokenValue, exists := c.Get("mcpToken")
	if !exists {
		// This should not happen if middleware is properly configured
		a.pluginAPI.Log.Error("MCP token not found in context - middleware not configured correctly")
		c.AbortWithStatus(500)
		return
	}

	token, ok := tokenValue.(string)
	if !ok || token == "" {
		a.pluginAPI.Log.Error("Invalid MCP token type in context")
		c.AbortWithStatus(500)
		return
	}

	// Clone request and add token to context (NOT as header)
	// The OAuth provider expects the token in the request context
	ctx := c.Request.Context()
	ctx = context.WithValue(ctx, auth.AuthTokenContextKey, token)
	r := c.Request.WithContext(ctx)

	// Also set Authorization header for compatibility
	r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	// Delegate to the specified MCP handler
	handler.ServeHTTP(c.Writer, r)
}
