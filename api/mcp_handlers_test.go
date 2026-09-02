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
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestDelegateToMCPHandler_PropagatesUserIDIntoContext(t *testing.T) {
	e := SetupTestEnvironment(t)

	// The session-grant marker check reads/writes KV; this test only cares
	// about context propagation, so accept whatever it does.
	e.mockAPI.On("KVGet", mock.Anything).Return(([]byte)(nil), (*model.AppError)(nil)).Maybe()
	e.mockAPI.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(true, (*model.AppError)(nil)).Maybe()

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

	// The merged session-grant audit path reads/writes KV on every delegated
	// request; this test only exercises the concurrency limiter, so accept
	// those KV calls.
	e.mockAPI.On("KVGet", mock.Anything).Return(([]byte)(nil), (*model.AppError)(nil)).Maybe()
	e.mockAPI.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(true, (*model.AppError)(nil)).Maybe()

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
	for range mcpMaxConcurrentRequestsPerUser {
		go func() {
			defer func() { done <- struct{}{} }()
			run(blocking)
		}()
	}
	for range mcpMaxConcurrentRequestsPerUser {
		<-started
	}

	// Saturated: the next request must be rejected with 429.
	rec := run(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("downstream must not be called over the concurrency limit")
	}))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	// The limit is strictly per-user: one user saturating their own slots
	// must not affect anyone else (there is deliberately no global cap, so a
	// few abusive accounts cannot lock every other user out).
	otherRec := httptest.NewRecorder()
	otherReq := httptest.NewRequest(http.MethodGet, "/plugins/mattermost-ai/mcp-server/mcp", nil)
	otherC, _ := gin.CreateTestContext(otherRec)
	otherC.Request = otherReq.WithContext(context.Background())
	otherC.Set("userID", "other567890123456789012345")
	e.api.delegateToMCPHandler(otherC, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	require.Equal(t, http.StatusOK, otherRec.Code, "another user must be unaffected")

	// Releasing the in-flight requests frees the slots.
	close(release)
	for range mcpMaxConcurrentRequestsPerUser {
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

	// Accept the merged session-grant audit path's KV reads/writes.
	e.mockAPI.On("KVGet", mock.Anything).Return(([]byte)(nil), (*model.AppError)(nil)).Maybe()
	e.mockAPI.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(true, (*model.AppError)(nil)).Maybe()

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

func TestDelegateToMCPHandler_AuditsSessionGrant(t *testing.T) {
	grantMarkerKey := mcpSessionGrantLoggedKeyPrefix + "mock-session-id"

	tests := []struct {
		name         string
		created      bool
		markerLogged bool
		ensureErr    error
		wantRecords  int
		wantStatus   string
		wantReused   bool
	}{
		{
			name:        "first connect mints a session and emits one grant record",
			created:     true,
			wantRecords: 1,
			wantStatus:  model.AuditStatusSuccess,
			wantReused:  false,
		},
		{
			name:         "external adoption of an in-plugin session emits one grant record",
			created:      false,
			markerLogged: false,
			wantRecords:  1,
			wantStatus:   model.AuditStatusSuccess,
			wantReused:   true,
		},
		{
			name:         "reconnect with an already-audited session emits nothing",
			created:      false,
			markerLogged: true,
			wantRecords:  0,
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

			if tt.ensureErr == nil {
				if tt.markerLogged {
					e.mockAPI.On("KVGet", grantMarkerKey).Return([]byte("1"), (*model.AppError)(nil)).Maybe()
				} else {
					e.mockAPI.On("KVGet", grantMarkerKey).Return(([]byte)(nil), (*model.AppError)(nil)).Maybe()
					e.mockAPI.On("KVSetWithOptions", grantMarkerKey, mock.Anything, mock.Anything).Return(true, (*model.AppError)(nil)).Maybe()
				}
			}

			// A real tracer provider so the grant record's trace-ID pivot is
			// exercised, not just the middleware's copy of the snippet.
			prevProvider := otel.GetTracerProvider()
			tp := sdktrace.NewTracerProvider()
			otel.SetTracerProvider(tp)
			t.Cleanup(func() {
				otel.SetTracerProvider(prevProvider)
				_ = tp.Shutdown(context.Background())
			})
			spanCtx, span := tp.Tracer("test").Start(context.Background(), "mcp request")
			defer span.End()

			downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/plugins/mattermost-ai/mcp-server/mcp", nil)
			c, _ := gin.CreateTestContext(rec)
			c.Request = req.WithContext(spanCtx)
			c.Set(pluginContextGinKey, &plugin.Context{})
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

			traceID, ok := got.Meta[audit.MetaTraceID].(string)
			require.True(t, ok, "grant records must carry the trace_id pivot")
			require.Len(t, traceID, 32)

			if tt.ensureErr != nil {
				require.Equal(t, http.StatusInternalServerError, got.Error.Code)
				require.Equal(t, "failed to ensure MCP session", got.Error.Description,
					"grant fail records carry only the static class, never the error text")
				return
			}

			require.Equal(t, tt.wantReused, got.EventData.Parameters["session_reused"])

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
