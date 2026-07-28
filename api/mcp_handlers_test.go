// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
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

func TestDelegateToMCPHandler_AuditsSessionGrant(t *testing.T) {
	tests := []struct {
		name        string
		created     bool
		ensureErr   error
		wantRecords int
		wantStatus  string
	}{
		{
			name:        "first connect mints a session and emits one grant record",
			created:     true,
			wantRecords: 1,
			wantStatus:  model.AuditStatusSuccess,
		},
		{
			name:        "reconnect reusing a session emits nothing",
			created:     false,
			wantRecords: 0,
		},
		{
			name:        "session mint failure emits one fail record",
			ensureErr:   errors.New("session unavailable"),
			wantRecords: 1,
			wantStatus:  model.AuditStatusFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.mcp.ensureSessionCreated = tt.created
			e.mcp.ensureSessionErr = tt.ensureErr
			records := e.CaptureAuditRecords()

			downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/plugins/mattermost-ai/mcp-server/mcp", nil)
			c, _ := gin.CreateTestContext(rec)
			c.Request = req.WithContext(context.Background())
			c.Set("pluginContext", &plugin.Context{})
			c.Set("userID", testUserID)

			e.api.delegateToMCPHandler(c, downstream)

			require.Len(t, *records, tt.wantRecords)
			if tt.wantRecords == 0 {
				return
			}

			got := (*records)[0]
			require.Equal(t, AuditEventMCPSessionGrant, got.EventName)
			require.Equal(t, tt.wantStatus, got.Status)
			require.Equal(t, testUserID, got.Actor.UserId)

			if tt.ensureErr != nil {
				require.Equal(t, http.StatusInternalServerError, got.Error.Code)
				require.NotEmpty(t, got.Error.Description)
				return
			}

			// The record documents who got access, never the credential itself:
			// the minted session ID must not appear anywhere in the record.
			raw, err := json.Marshal(got)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "mock-session-id")
		})
	}
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
