// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
)

// flushableResponseWriter wraps gin's ResponseWriter to add Flusher support
// The plugin RPC layer doesn't support http.Flusher, so we provide a no-op implementation
type flushableResponseWriter struct {
	http.ResponseWriter
}

func (f *flushableResponseWriter) Flush() {
	// No-op: Plugin RPC layer doesn't support flushing
	// The MCP SDK expects this method to exist for streaming responses
}

// delegateToMCPHandler delegates the request to the MCP handler
// It injects the session ID and token resolver into the request context
// because the Session provider expects these in context.WithValue
func (a *API) delegateToMCPHandler(c *gin.Context, handler http.Handler) {
	// Get session ID from middleware (set by mcpAuthMiddleware)
	sessionIDValue, exists := c.Get("mcpSessionID")
	if !exists {
		// This should not happen if middleware is properly configured
		a.pluginAPI.Log.Error("MCP session ID not found in context - middleware not configured correctly")
		c.AbortWithStatus(500)
		return
	}

	sessionID, ok := sessionIDValue.(string)
	if !ok || sessionID == "" {
		a.pluginAPI.Log.Error("Invalid MCP session ID type in context")
		c.AbortWithStatus(500)
		return
	}

	// Get token resolver from middleware
	resolverValue, exists := c.Get("mcpTokenResolver")
	if !exists {
		a.pluginAPI.Log.Error("MCP token resolver not found in context - middleware not configured correctly")
		c.AbortWithStatus(500)
		return
	}

	resolver, ok := resolverValue.(func(string) (string, error))
	if !ok {
		a.pluginAPI.Log.Error("Invalid MCP token resolver type in context")
		c.AbortWithStatus(500)
		return
	}

	// Clone request and add session ID + token resolver to context
	// The Session provider expects both of these in the request context
	ctx := c.Request.Context()
	ctx = context.WithValue(ctx, auth.SessionIDContextKey, sessionID)
	ctx = context.WithValue(ctx, auth.TokenResolverContextKey, auth.TokenResolver(resolver))
	r := c.Request.WithContext(ctx)

	// Wrap the response writer to provide Flusher interface
	// The plugin RPC layer doesn't support flushing, but the MCP SDK requires it
	wrappedWriter := &flushableResponseWriter{ResponseWriter: c.Writer}

	// Delegate to the specified MCP handler
	handler.ServeHTTP(wrappedWriter, r)
}
