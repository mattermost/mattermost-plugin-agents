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

	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-ai/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPSemanticSearchService_Search(t *testing.T) {
	tests := []struct {
		name           string
		serverHandler  http.HandlerFunc
		query          string
		opts           search.Options
		ctxSetup       func(context.Context) context.Context
		expectedCount  int
		expectError    bool
		errorContains  string
		validateResult func(t *testing.T, results []search.RAGResult)
		validateReq    func(t *testing.T, r *http.Request)
	}{
		{
			name: "successful search returns results",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				resp := httpSearchResponse{
					Results: []httpSearchResult{
						{PostID: "p1", ChannelID: "c1", ChannelName: "General", UserID: "u1", Username: "alice", Content: "hello world", Score: 0.95},
						{PostID: "p2", ChannelID: "c2", ChannelName: "Dev", UserID: "u2", Username: "bob", Content: "test post", Score: 0.80},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			},
			query:         "hello",
			expectedCount: 2,
			validateResult: func(t *testing.T, results []search.RAGResult) {
				assert.Equal(t, "p1", results[0].PostID)
				assert.Equal(t, "alice", results[0].Username)
				assert.InDelta(t, 0.95, float64(results[0].Score), 0.01)
				assert.Equal(t, "p2", results[1].PostID)
			},
		},
		{
			name: "server error with JSON error body",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(httpSearchResponse{Error: "search unavailable"})
			},
			query:         "test",
			expectError:   true,
			errorContains: "search unavailable",
		},
		{
			name: "server error with non-JSON body",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("bad gateway"))
			},
			query:         "test",
			expectError:   true,
			errorContains: "502",
		},
		{
			name: "invalid JSON response",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{invalid json"))
			},
			query:         "test",
			expectError:   true,
			errorContains: "failed to parse response",
		},
		{
			name: "empty results",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				resp := httpSearchResponse{Results: []httpSearchResult{}}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			},
			query:         "nonexistent",
			expectedCount: 0,
		},
		{
			name: "auth headers are sent from context",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				// Verify headers were sent (checked in validateReq)
				resp := httpSearchResponse{Results: []httpSearchResult{}}
				_ = json.NewEncoder(w).Encode(resp)
			},
			query: "test",
			ctxSetup: func(ctx context.Context) context.Context {
				ctx = context.WithValue(ctx, auth.AuthTokenContextKey, "test-bearer-token")
				ctx = context.WithValue(ctx, auth.UserIDContextKey, "user-123")
				return ctx
			},
			expectedCount: 0,
			validateReq: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "Bearer test-bearer-token", r.Header.Get("Authorization"))
				assert.Equal(t, "user-123", r.Header.Get("Mattermost-User-Id"))
			},
		},
		{
			name: "request body contains search parameters",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				var req httpSearchRequest
				assert.NoError(t, json.Unmarshal(body, &req))
				assert.Equal(t, "search query", req.Query)
				assert.Equal(t, "team1", req.TeamID)
				assert.Equal(t, "chan1", req.ChannelID)
				assert.Equal(t, 20, req.Limit)
				assert.Equal(t, 5, req.Offset)

				resp := httpSearchResponse{Results: []httpSearchResult{}}
				_ = json.NewEncoder(w).Encode(resp)
			},
			query: "search query",
			opts: search.Options{
				TeamID:    "team1",
				ChannelID: "chan1",
				Limit:     20,
				Offset:    5,
			},
			expectedCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var lastReq *http.Request
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lastReq = r.Clone(r.Context())
				// Read body before clone loses it
				if tc.validateReq != nil {
					tc.validateReq(t, r)
				}
				tc.serverHandler(w, r)
			}))
			defer server.Close()

			svc := NewHTTPSemanticSearchService(server.URL)

			ctx := context.Background()
			if tc.ctxSetup != nil {
				ctx = tc.ctxSetup(ctx)
			}

			results, err := svc.Search(ctx, tc.query, tc.opts)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Len(t, results, tc.expectedCount)

			if tc.validateResult != nil {
				tc.validateResult(t, results)
			}

			// Verify request went to correct endpoint
			if lastReq != nil {
				assert.Equal(t, "/api/v1/search/raw", lastReq.URL.Path)
				assert.Equal(t, http.MethodPost, lastReq.Method)
				assert.Equal(t, "application/json", lastReq.Header.Get("Content-Type"))
			}
		})
	}
}
