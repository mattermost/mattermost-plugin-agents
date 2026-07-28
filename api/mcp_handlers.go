// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"go.opentelemetry.io/otel/trace"
)

// mcpSessionGrantRecord builds the audit record for an MCP session grant
// attempt. A grant is a credential event — an external MCP client obtaining a
// dedicated session that holds API access as the user — so the record carries
// the actor identity and trace ID, never the minted session ID itself.
func mcpSessionGrantRecord(c *gin.Context, userID string) *model.AuditRecord {
	pluginCtx := &plugin.Context{}
	if v, exists := c.Get("pluginContext"); exists {
		if pc, ok := v.(*plugin.Context); ok && pc != nil {
			pluginCtx = pc
		}
	}

	rec := plugin.MakeAuditRecordWithContext(AuditEventMCPSessionGrant, model.AuditStatusFail, pluginCtx, userID, c.Request.URL.Path)
	if sc := trace.SpanFromContext(c.Request.Context()).SpanContext(); sc.HasTraceID() {
		rec.AddMeta(audit.MetaTraceID, sc.TraceID().String())
	}
	return rec
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

	// Get or create dedicated MCP session for this user
	sessionID, created, err := a.mcpClientManager.EnsureMCPSessionID(userID)
	if err != nil {
		a.pluginAPI.Log.Error("Failed to ensure MCP session for user",
			"userId", userID,
			"error", err)
		rec := mcpSessionGrantRecord(c, userID)
		rec.AddErrorCode(http.StatusInternalServerError)
		rec.AddErrorDesc(err.Error())
		a.pluginAPI.Audit.Record(rec)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// A newly minted session means an external MCP client just obtained API
	// access as this user — a credential event worth auditing. Session reuse
	// on reconnect is intentionally silent so it cannot spam the audit log.
	if created {
		rec := mcpSessionGrantRecord(c, userID)
		rec.Success()
		a.pluginAPI.Audit.Record(rec)
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
	// Propagate authenticated user ID so proxy MCP tool handlers can inject
	// X-Mattermost-UserID on outbound PluginHTTP calls. userID is trustworthy:
	// the Mattermost server strips Mattermost-User-Id from external callers.
	ctx = context.WithValue(ctx, auth.UserIDContextKey, userID)
	r := c.Request.WithContext(ctx)

	// Delegate to the specified MCP handler
	handler.ServeHTTP(c.Writer, r)
}
