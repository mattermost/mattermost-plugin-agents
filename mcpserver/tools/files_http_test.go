// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/files"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
)

func TestHTTPFileContentService_GetContent(t *testing.T) {
	tests := []struct {
		name           string
		serverHandler  http.HandlerFunc
		userID         string
		ctxSetup       func(context.Context) context.Context
		expectError    bool
		expectForbid   bool
		errorContains  string
		validateResult func(t *testing.T, c files.Content)
		validateReq    func(t *testing.T, r *http.Request)
	}{
		{
			name: "successful read copies all fields",
			serverHandler: func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(httpFileContentResponse{
					Name: "report.pdf", MimeType: "application/pdf",
					TotalRunes: 100, Offset: 6, Returned: 4, HasMore: true, HasText: true, Text: "wxyz",
				})
			},
			validateResult: func(t *testing.T, c files.Content) {
				assert.Equal(t, "report.pdf", c.Name)
				assert.Equal(t, "application/pdf", c.MimeType)
				assert.Equal(t, 100, c.TotalRunes)
				assert.Equal(t, 6, c.Offset)
				assert.Equal(t, 4, c.Returned)
				assert.True(t, c.HasMore)
				assert.True(t, c.HasText)
				assert.Equal(t, "wxyz", c.Text)
			},
		},
		{
			name: "403 maps to ErrForbidden",
			serverHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(httpFileContentResponse{Error: "no access"})
			},
			expectError:  true,
			expectForbid: true,
		},
		{
			name: "server error with JSON error body",
			serverHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(httpFileContentResponse{Error: "extraction failed"})
			},
			expectError:   true,
			errorContains: "extraction failed",
		},
		{
			name: "server error with non-JSON body includes status",
			serverHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("bad gateway"))
			},
			expectError:   true,
			errorContains: "502",
		},
		{
			name: "invalid JSON success body is a parse error",
			serverHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{invalid json"))
			},
			expectError:   true,
			errorContains: "failed to parse response",
		},
		{
			name: "auth headers come from context when present",
			serverHandler: func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(httpFileContentResponse{HasText: true})
			},
			userID: "param-user",
			ctxSetup: func(ctx context.Context) context.Context {
				ctx = context.WithValue(ctx, auth.AuthTokenContextKey, "test-bearer-token")
				ctx = context.WithValue(ctx, auth.UserIDContextKey, "ctx-user")
				return ctx
			},
			validateReq: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "Bearer test-bearer-token", r.Header.Get("Authorization"))
				// The context user ID must win over the param to avoid trusting
				// a caller-supplied identity over the authenticated one.
				assert.Equal(t, "ctx-user", r.Header.Get("Mattermost-User-Id"))
			},
		},
		{
			name: "user id header falls back to the param when context lacks it",
			serverHandler: func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(httpFileContentResponse{HasText: true})
			},
			userID: "param-user",
			validateReq: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "param-user", r.Header.Get("Mattermost-User-Id"))
			},
		},
		{
			name: "request carries file id and range to the correct endpoint",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				var req httpFileContentRequest
				assert.NoError(t, json.Unmarshal(body, &req))
				assert.Equal(t, "file-123", req.FileID)
				assert.Equal(t, 12, req.Offset)
				assert.Equal(t, 34, req.Limit)
				_ = json.NewEncoder(w).Encode(httpFileContentResponse{HasText: true})
			},
			validateReq: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "/api/v1/files/content", r.URL.Path)
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.validateReq != nil {
					tc.validateReq(t, r)
				}
				tc.serverHandler(w, r)
			}))
			defer server.Close()

			svc := NewHTTPFileContentService(server.URL)

			ctx := context.Background()
			if tc.ctxSetup != nil {
				ctx = tc.ctxSetup(ctx)
			}

			fileID := "file-123"
			c, err := svc.GetContent(ctx, tc.userID, fileID, 12, 34)

			if tc.expectError {
				require.Error(t, err)
				if tc.expectForbid {
					require.ErrorIs(t, err, files.ErrForbidden)
				}
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
				return
			}

			require.NoError(t, err)
			if tc.validateResult != nil {
				tc.validateResult(t, c)
			}
		})
	}
}
