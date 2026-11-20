// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// nilHeaderResponseWriter simulates Mattermost's RPC ResponseWriter after a
// client disconnect, when Header() returns nil and gin's render.WriteContentType
// panics with "assignment to entry in nil map".
type nilHeaderResponseWriter struct {
	statusCodes []int
	writeCalls  int
}

func (w *nilHeaderResponseWriter) Header() http.Header { return nil }

func (w *nilHeaderResponseWriter) Write(b []byte) (int, error) {
	w.writeCalls++
	return len(b), nil
}

func (w *nilHeaderResponseWriter) WriteHeader(code int) {
	w.statusCodes = append(w.statusCodes, code)
}

func TestServeHTTPRecovery(t *testing.T) {
	const (
		sentinelCookie = "sentinel-cookie-mmcsrf-9f8e7d"
		sentinelHeader = "sentinel-custom-header-1a2b3c"
		sentinelQuery  = "sentinel-query-token-4d5e6f"
		sentinelIP     = "198.51.100.77"
	)
	sentinels := []string{sentinelCookie, sentinelHeader, sentinelQuery, sentinelIP}

	tests := []struct {
		name           string
		cancelContext  bool
		nilHeader      bool
		panicAfterBody bool
		wantStatus     int
		wantErrorLogs  int
		wantDebugLogs  int
		wantBody       string
	}{
		{
			name:          "live context returns 500 and logs panic without request secrets",
			wantStatus:    http.StatusInternalServerError,
			wantErrorLogs: 1,
		},
		{
			name:          "cancelled context logs debug only and does not write 500",
			cancelContext: true,
			wantDebugLogs: 1,
		},
		{
			name:          "cancelled context nil header panic from c.JSON logs debug only",
			cancelContext: true,
			nilHeader:     true,
			wantDebugLogs: 1,
		},
		{
			name:           "live context panic after response started keeps original status",
			panicAfterBody: true,
			wantStatus:     http.StatusOK,
			wantErrorLogs:  1,
			wantBody:       "partial",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prevMode := gin.Mode()
			prevWriter := gin.DefaultWriter
			prevErrWriter := gin.DefaultErrorWriter
			var ginStdout, ginStderr bytes.Buffer
			// Run in DebugMode to match production, where gin.Default's Recovery
			// dumps full request headers on panic.
			gin.SetMode(gin.DebugMode)
			gin.DefaultWriter = &ginStdout
			gin.DefaultErrorWriter = &ginStderr
			t.Cleanup(func() {
				gin.SetMode(prevMode)
				gin.DefaultWriter = prevWriter
				gin.DefaultErrorWriter = prevErrWriter
			})

			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			reqCtx := context.Background()
			if tt.cancelContext {
				var cancel context.CancelFunc
				reqCtx, cancel = context.WithCancel(reqCtx)
				cancel()
			}

			path := "/ai_bots?token=" + sentinelQuery
			if tt.panicAfterBody {
				path = "/test-panic-after-body?token=" + sentinelQuery
			}
			req := httptest.NewRequestWithContext(reqCtx, http.MethodGet, path, nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			req.Header.Set("Cookie", "MMCSRF="+sentinelCookie+"; MMUSERID="+testUserID)
			req.Header.Set("X-Custom-Secret", sentinelHeader)
			req.Header.Set("X-Forwarded-For", sentinelIP)

			switch {
			case tt.nilHeader:
				w := &nilHeaderResponseWriter{}
				e.api.ServeHTTP(&plugin.Context{}, w, req)
				assert.Zero(t, w.writeCalls, "cancelled request must not write response body")
				assert.NotContains(t, w.statusCodes, http.StatusInternalServerError,
					"cancelled request must not write 500")
			case tt.panicAfterBody:
				router := e.api.newRouter(&plugin.Context{}, reqCtx)
				router.GET("/test-panic-after-body", func(c *gin.Context) {
					c.String(http.StatusOK, tt.wantBody)
					panic("boom after write")
				})
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				assert.Equal(t, tt.wantStatus, rec.Code)
				assert.Equal(t, tt.wantBody, rec.Body.String())
			default:
				// Nil out conversationStore so the real GET /ai_bots handler panics
				// inside production ServeHTTP before writing anything.
				e.api.conversationStore = nil
				rec := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, rec, req)
				if tt.wantStatus != 0 {
					assert.Equal(t, tt.wantStatus, rec.Code)
				} else {
					assert.NotEqual(t, http.StatusInternalServerError, rec.Code)
					assert.Empty(t, rec.Body.String())
				}
			}

			var errorCalls, debugCalls []mock.Call
			var allLogged strings.Builder
			for _, call := range e.mockAPI.Calls {
				switch call.Method {
				case "LogError":
					errorCalls = append(errorCalls, call)
				case "LogDebug":
					debugCalls = append(debugCalls, call)
				case "LogInfo", "LogWarn":
				default:
					continue
				}
				fmt.Fprintln(&allLogged, call.Arguments)
			}

			require.Len(t, errorCalls, tt.wantErrorLogs)
			require.Len(t, debugCalls, tt.wantDebugLogs)

			if tt.wantErrorLogs > 0 {
				args := fmt.Sprint(errorCalls[0].Arguments)
				assert.Contains(t, args, "method")
				assert.Contains(t, args, http.MethodGet)
				assert.Contains(t, args, "stack")
				if tt.panicAfterBody {
					assert.Contains(t, args, "/test-panic-after-body")
					assert.Contains(t, args, "boom after write")
				} else {
					assert.Contains(t, args, "/ai_bots")
				}
			}

			if tt.wantDebugLogs > 0 {
				require.NotEmpty(t, debugCalls[0].Arguments)
				assert.Equal(t, "client disconnected before response", debugCalls[0].Arguments[0])
				args := fmt.Sprint(debugCalls[0].Arguments)
				assert.Contains(t, args, http.MethodGet)
				assert.Contains(t, args, "/ai_bots")
			}

			assert.Empty(t, ginStderr.String(), "gin default error writer must not receive recovery output")
			for _, sentinel := range sentinels {
				assert.NotContains(t, allLogged.String(), sentinel, "plugin logs must not leak request secrets")
				assert.NotContains(t, ginStdout.String(), sentinel, "gin stdout must not leak request secrets")
				assert.NotContains(t, ginStderr.String(), sentinel, "gin stderr must not leak request secrets")
			}
		})
	}
}
