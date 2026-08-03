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
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/stretchr/testify/require"
)

func TestDelegateToMCPHandler_PropagatesUserIDIntoContext(t *testing.T) {
	e := SetupTestEnvironment(t)

	const userID = "uzr1234567890123456789012X"

	var gotUserID string
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := r.Context().Value(auth.UserIDContextKey).(string); ok {
			gotUserID = v
		}
		w.WriteHeader(http.StatusOK)
	})

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

// TestDelegateToMCPHandler_ConcurrencyLimit verifies the per-user in-flight
// cap: once a user saturates it (e.g. via orphaned long-lived streams that
// plugin RPC cannot cancel), further requests get 429 instead of accumulating
// unbounded handler resources — and slots are released when requests finish.
func TestDelegateToMCPHandler_ConcurrencyLimit(t *testing.T) {
	e := SetupTestEnvironment(t)

	const userID = "uzr1234567890123456789012X"

	release := make(chan struct{})
	started := make(chan struct{}, mcpMaxConcurrentRequestsPerUser)
	blocking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})

	gin.SetMode(gin.TestMode)
	run := func(handler http.Handler) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/plugins/mattermost-ai/mcp-server/mcp", nil)
		c, _ := gin.CreateTestContext(rec)
		c.Request = req.WithContext(context.Background())
		c.Set("userID", userID)
		e.api.delegateToMCPHandler(c, handler)
		return rec
	}

	done := make(chan struct{})
	for i := 0; i < mcpMaxConcurrentRequestsPerUser; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			run(blocking)
		}()
	}
	for i := 0; i < mcpMaxConcurrentRequestsPerUser; i++ {
		<-started
	}

	// Saturated: the next request must be rejected with 429.
	rec := run(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("downstream must not be called over the concurrency limit")
	}))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	// Releasing the in-flight requests frees the slots.
	close(release)
	for i := 0; i < mcpMaxConcurrentRequestsPerUser; i++ {
		<-done
	}
	rec = run(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestDelegateToMCPHandler_RequestLifetimeBounded verifies the delegated
// request context carries a deadline (the plugin RPC layer cannot propagate
// client-disconnect cancellation, so orphaned streams must expire).
func TestDelegateToMCPHandler_RequestLifetimeBounded(t *testing.T) {
	e := SetupTestEnvironment(t)

	var hasDeadline bool
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/plugins/mattermost-ai/mcp-server/mcp", nil)
	c, _ := gin.CreateTestContext(rec)
	c.Request = req.WithContext(context.Background())
	c.Set("userID", "uzr1234567890123456789012X")

	e.api.delegateToMCPHandler(c, downstream)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, hasDeadline, "delegated MCP requests must carry a lifetime deadline")
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
			e.mockAPI.On("LogError", "User ID not found in context - middleware not configured correctly").Maybe()
			e.mockAPI.On("LogError", "Invalid user ID type in context").Maybe()
			if tt.ensureErr != nil {
				e.mockAPI.On("LogError", "Failed to ensure MCP session for user", "userId", testUserID, "error", tt.ensureErr).Maybe()
			}

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
