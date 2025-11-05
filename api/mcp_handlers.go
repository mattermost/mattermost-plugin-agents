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

// delegateToMCPHandler delegates the request to the MCP handler
// It creates a dedicated MCP session and injects session ID + token resolver into the request context
func (a *API) delegateToMCPHandler(c *gin.Context, handler http.Handler) {
	// Get user ID from middleware (set by mcpAuthMiddleware)
	userIDValue, exists := c.Get("userID")
	if !exists {
		a.pluginAPI.Log.Error("User ID not found in context - middleware not configured correctly")
		c.AbortWithStatus(500)
		return
	}

	userID, ok := userIDValue.(string)
	if !ok || userID == "" {
		a.pluginAPI.Log.Error("Invalid user ID type in context")
		c.AbortWithStatus(500)
		return
	}

	// Get or create dedicated MCP session for this user
	sessionID, err := a.mcpClientManager.EnsureMCPSessionID(userID)
	if err != nil {
		a.pluginAPI.Log.Error("Failed to ensure MCP session for user",
			"userId", userID,
			"error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// Create token resolver with closure over pluginAPI
	tokenResolver := func(sid string) (string, error) {
		sess, err := a.pluginAPI.Session.Get(sid)
		if err != nil {
			return "", err
		}
		if sess == nil {
			return "", fmt.Errorf("session not found")
		}
		return sess.Token, nil
	}

	// Add session ID + token resolver to request context
	// Uses the same context keys as the embedded server for consistency
	ctx := c.Request.Context()
	ctx = context.WithValue(ctx, auth.SessionIDContextKey, sessionID)
	ctx = context.WithValue(ctx, auth.TokenResolverContextKey, auth.TokenResolver(tokenResolver))
	r := c.Request.WithContext(ctx)

	// Delegate to the specified MCP handler
	handler.ServeHTTP(c.Writer, r)
}
