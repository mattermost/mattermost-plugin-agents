// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
)

const (
	// mcpRequestMaxLifetime bounds every request delegated to the plugin MCP
	// handler. The Mattermost plugin RPC layer reconstructs HTTP requests
	// without their original context, so a client that disconnects (for
	// example after opening a long-lived subscriptions/listen stream) never
	// cancels the handler; without a bound, the handler goroutine, SDK
	// session, and host RPC resources would be retained indefinitely. The
	// limit is deliberately generous — far beyond any real tool call — and
	// long-lived streams that hit it are simply re-opened by spec-compliant
	// clients (MCP streams may end at any time).
	mcpRequestMaxLifetime = 30 * time.Minute

	// mcpMaxConcurrentRequestsPerUser and mcpMaxConcurrentRequestsGlobal cap
	// how many delegated MCP requests may be in flight, bounding resource
	// retention from reconnect churn within a request lifetime window. A
	// well-behaved client holds one listen stream plus short-lived calls, so
	// these are generous.
	mcpMaxConcurrentRequestsPerUser = 16
	mcpMaxConcurrentRequestsGlobal  = 256
)

// mcpRequestLimiter tracks in-flight delegated MCP requests per user and
// globally.
type mcpRequestLimiter struct {
	mu      sync.Mutex
	global  int
	perUser map[string]int
}

func newMCPRequestLimiter() *mcpRequestLimiter {
	return &mcpRequestLimiter{perUser: map[string]int{}}
}

// acquire reserves a slot for the user's request. It returns false when the
// per-user or global concurrency cap is reached.
func (l *mcpRequestLimiter) acquire(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.global >= mcpMaxConcurrentRequestsGlobal || l.perUser[userID] >= mcpMaxConcurrentRequestsPerUser {
		return false
	}
	l.global++
	l.perUser[userID]++
	return true
}

func (l *mcpRequestLimiter) release(userID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.global--
	if l.perUser[userID] <= 1 {
		delete(l.perUser, userID)
	} else {
		l.perUser[userID]--
	}
}

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

	if !a.mcpRequestLimiter.acquire(userID) {
		a.pluginAPI.Log.Warn("MCP request concurrency limit reached", "userId", userID)
		c.Header("Retry-After", "10")
		c.AbortWithStatus(http.StatusTooManyRequests)
		return
	}
	defer a.mcpRequestLimiter.release(userID)

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
	// Uses the same context keys as the embedded server for consistency.
	// The lifetime cap substitutes for client-disconnect cancellation, which
	// the plugin RPC layer does not propagate (see mcpRequestMaxLifetime).
	ctx, cancel := context.WithTimeout(c.Request.Context(), mcpRequestMaxLifetime)
	defer cancel()
	ctx = context.WithValue(ctx, auth.SessionIDContextKey, sessionID)
	ctx = context.WithValue(ctx, auth.TokenResolverContextKey, auth.TokenResolver(tokenResolver))
	// Propagate authenticated user ID so proxy MCP tool handlers can inject
	// X-Mattermost-UserID on outbound PluginHTTP calls. userID is trustworthy:
	// the Mattermost server strips Mattermost-User-Id from external callers.
	ctx = context.WithValue(ctx, auth.UserIDContextKey, userID)
	r := c.Request.WithContext(ctx)

	// Delegate to the specified MCP handler
	handler.ServeHTTP(c.Writer, r)
}
