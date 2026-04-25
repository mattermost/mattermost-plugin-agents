// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestDelegateToMCPHandler_PropagatesUserIDIntoContext is a release-gate test
// for Task 1G-0. The proxy tool handlers built by mcpserver/proxy_tools.go
// require auth.UserIDContextKey to be readable from the request context —
// without this propagation they have no way to set X-Mattermost-UserID on
// outbound PluginHTTP calls.
func TestDelegateToMCPHandler_PropagatesUserIDIntoContext(t *testing.T) {
	e := SetupTestEnvironment(t)

	const userID = "uzr1234567890123456789012X"

	// Downstream handler captures the request context and asserts the key is set.
	var gotUserID string
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := r.Context().Value(auth.UserIDContextKey).(string); ok {
			gotUserID = v
		}
		w.WriteHeader(http.StatusOK)
	})

	// Minimal gin context with the middleware-populated "userID" key already
	// present (we skip the middleware — that behavior is tested elsewhere).
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/plugins/mattermost-ai/mcp-server/mcp", nil)
	c, _ := gin.CreateTestContext(rec)
	c.Request = req.WithContext(context.Background())
	c.Set("userID", userID)

	e.api.delegateToMCPHandler(c, downstream)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, userID, gotUserID, "expected auth.UserIDContextKey propagated to downstream context")
}

func TestDelegateToMCPHandler_FailurePathsDoNotCallDownstream(t *testing.T) {
	tests := []struct {
		name             string
		setUserIDContext func(c *gin.Context)
		ensureErr        error
	}{
		{
			name: "missing userID",
		},
		{
			name: "empty userID",
			setUserIDContext: func(c *gin.Context) {
				c.Set("userID", "")
			},
		},
		{
			name: "wrong userID type",
			setUserIDContext: func(c *gin.Context) {
				c.Set("userID", 123)
			},
		},
		{
			name: "EnsureMCPSessionID error",
			setUserIDContext: func(c *gin.Context) {
				c.Set("userID", testUserID)
			},
			ensureErr: errors.New("session unavailable"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.mcp.ensureSessionErr = tt.ensureErr
			e.mockAPI.On("LogError", mock.Anything).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

			downstreamCalled := false
			downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downstreamCalled = true
				w.WriteHeader(http.StatusOK)
			})

			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/plugins/mattermost-ai/mcp-server/mcp", nil)
			c, _ := gin.CreateTestContext(rec)
			c.Request = req.WithContext(context.Background())
			if tt.setUserIDContext != nil {
				tt.setUserIDContext(c)
			}

			e.api.delegateToMCPHandler(c, downstream)

			require.Equal(t, http.StatusInternalServerError, rec.Code)
			require.False(t, downstreamCalled, "downstream handler must not be called after auth/session setup failure")
		})
	}
}
