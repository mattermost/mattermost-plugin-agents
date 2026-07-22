// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	testAppResourcePostID = "post12345678901234567890ab"
	testAppResourceConvID = "conv-app-resource-1"
	testAppResourceOrigin = "http://srv-a/mcp"
	testAppResourceURI    = "ui://srv-a/app.html"
)

func TestHandleGetMCPAppResource(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	prefersBorder := true
	successResource := &mcp.AppResource{
		URI:      testAppResourceURI,
		MIMEType: mcp.UIResourceMIMEType,
		HTML:     "<html>app</html>",
		UIMeta: &mcp.AppResourceUIMeta{
			CSP:           &mcp.AppResourceCSP{ConnectDomains: []string{"https://api.example"}},
			PrefersBorder: &prefersBorder,
		},
	}

	seedDefaults := func(e *TestEnvironment, shared *bool, withUIMeta bool, withResult bool, serverConfigured bool) {
		channelID := testChannelID
		e.config.mcpConfig = mcp.Config{
			Enabled: true,
			Servers: []mcp.ServerConfig{},
		}
		if serverConfigured {
			e.config.mcpConfig.Servers = []mcp.ServerConfig{{
				Name:    "srv-a",
				BaseURL: testAppResourceOrigin,
				Enabled: true,
			}}
		}

		e.conversationStore.conversations[testAppResourceConvID] = &store.Conversation{
			ID:        testAppResourceConvID,
			UserID:    testUserID,
			BotID:     testBotUserID,
			ChannelID: &channelID,
		}

		toolUse := conversation.ContentBlock{
			Type:         conversation.BlockTypeToolUse,
			ID:           "tc1",
			Name:         "demo",
			ServerOrigin: testAppResourceOrigin,
			Status:       conversation.StatusSuccess,
			Shared:       conversation.BoolPtr(true),
		}
		if withUIMeta {
			toolUse.UIMeta = &llm.ToolUIMeta{ResourceURI: testAppResourceURI}
		}
		blocks := []conversation.ContentBlock{toolUse}
		if withResult {
			blocks = append(blocks, conversation.ContentBlock{
				Type:      conversation.BlockTypeToolResult,
				ToolUseID: "tc1",
				Content:   "ok",
				Status:    conversation.StatusSuccess,
				Shared:    shared,
			})
		}
		content, err := json.Marshal(blocks)
		require.NoError(t, err)
		postID := testAppResourcePostID
		turn := store.Turn{
			ID:             "turn-1",
			ConversationID: testAppResourceConvID,
			PostID:         &postID,
			Role:           "assistant",
			Content:        content,
			Sequence:       1,
		}
		e.conversationStore.turns[testAppResourceConvID] = []store.Turn{turn}
		e.conversationStore.turnsByPost[testAppResourcePostID] = &turn

		e.mockAPI.On("GetPost", testAppResourcePostID).Return(&model.Post{
			Id:        testAppResourcePostID,
			ChannelId: testChannelID,
		}, nil).Maybe()
		e.mockAPI.On("HasPermissionToChannel", mock.Anything, testChannelID, model.PermissionReadChannel).Return(true).Maybe()
		e.mcp.readUserAppResource = func(_ context.Context, _, _, _ string) (*mcp.AppResource, error) {
			return successResource, nil
		}
	}

	tests := []struct {
		name          string
		caller        string
		query         string
		setup         func(e *TestEnvironment)
		wantStatus    int
		wantErrorCode string
		wantAuthURL   string
		wantHTML      string
		validateBody  func(t *testing.T, body []byte)
	}{
		{
			name:          "missing post_id",
			caller:        testUserID,
			query:         "tool_call_id=tc1",
			wantStatus:    http.StatusBadRequest,
			wantErrorCode: appResourceErrInvalidRequest,
		},
		{
			name:          "missing tool_call_id",
			caller:        testUserID,
			query:         "post_id=" + testAppResourcePostID,
			wantStatus:    http.StatusBadRequest,
			wantErrorCode: appResourceErrInvalidRequest,
		},
		{
			name:          "malformed post_id",
			caller:        testUserID,
			query:         "post_id=not-valid&tool_call_id=tc1",
			wantStatus:    http.StatusBadRequest,
			wantErrorCode: appResourceErrInvalidRequest,
		},
		{
			name:   "post not found",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				e.mockAPI.On("GetPost", testAppResourcePostID).Return(
					nil,
					model.NewAppError("GetPost", "app.post.get.app_error", nil, "", http.StatusNotFound),
				)
			},
			wantStatus:    http.StatusNotFound,
			wantErrorCode: appResourceErrNotFound,
		},
		{
			name:   "caller lacks PermissionReadChannel",
			caller: testOtherUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				e.mockAPI.On("GetPost", testAppResourcePostID).Return(&model.Post{
					Id:        testAppResourcePostID,
					ChannelId: testChannelID,
				}, nil)
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, testChannelID, model.PermissionReadChannel).Return(false)
			},
			wantStatus:    http.StatusForbidden,
			wantErrorCode: appResourceErrForbidden,
		},
		{
			name:   "no turn for post",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				e.mockAPI.On("GetPost", testAppResourcePostID).Return(&model.Post{
					Id:        testAppResourcePostID,
					ChannelId: testChannelID,
				}, nil)
				e.mockAPI.On("HasPermissionToChannel", testUserID, testChannelID, model.PermissionReadChannel).Return(true)
			},
			wantStatus:    http.StatusNotFound,
			wantErrorCode: appResourceErrNotFound,
		},
		{
			name:   "tool_call_id not in any turn",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=missing",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(true), true, true, true)
			},
			wantStatus:    http.StatusNotFound,
			wantErrorCode: appResourceErrNotFound,
		},
		{
			name:   "tool_use has no UIMeta",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(true), false, true, true)
			},
			wantStatus:    http.StatusNotFound,
			wantErrorCode: appResourceErrNotFound,
		},
		{
			name:   "requester, result unshared",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(false), true, true, true)
			},
			wantStatus: http.StatusOK,
			wantHTML:   "<html>app</html>",
		},
		{
			name:   "requester, result shared",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(true), true, true, true)
			},
			wantStatus: http.StatusOK,
			wantHTML:   "<html>app</html>",
		},
		{
			name:   "requester, no result block yet",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, nil, true, false, true)
			},
			wantStatus: http.StatusOK,
			wantHTML:   "<html>app</html>",
		},
		{
			name:   "onlooker with channel access, result shared",
			caller: testOtherUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(true), true, true, true)
			},
			wantStatus: http.StatusOK,
			wantHTML:   "<html>app</html>",
		},
		{
			name:   "onlooker, result unshared",
			caller: testOtherUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(false), true, true, true)
			},
			wantStatus:    http.StatusForbidden,
			wantErrorCode: appResourceErrForbidden,
		},
		{
			name:   "onlooker, no result block",
			caller: testOtherUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, nil, true, false, true)
			},
			wantStatus:    http.StatusForbidden,
			wantErrorCode: appResourceErrForbidden,
		},
		{
			name:   "server origin no longer configured",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(true), true, true, false)
			},
			wantStatus:    http.StatusNotFound,
			wantErrorCode: appResourceErrNotFound,
		},
		{
			name:   "manager returns OAuthNeededError",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(true), true, true, true)
				e.mcp.readUserAppResource = func(_ context.Context, _, _, _ string) (*mcp.AppResource, error) {
					return nil, mcp.NewOAuthNeededError("https://auth.example/start")
				}
			},
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: appResourceErrAuthRequired,
			wantAuthURL:   "https://auth.example/start",
		},
		{
			name:   "manager returns InvalidAppResourceError",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(true), true, true, true)
				e.mcp.readUserAppResource = func(_ context.Context, _, _, _ string) (*mcp.AppResource, error) {
					return nil, &mcp.InvalidAppResourceError{URI: testAppResourceURI, MIMEType: "text/html"}
				}
			},
			wantStatus:    http.StatusBadGateway,
			wantErrorCode: appResourceErrInvalidResourceMime,
		},
		{
			name:   "manager returns ErrServerNotConnected",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(true), true, true, true)
				e.mcp.readUserAppResource = func(_ context.Context, _, _, _ string) (*mcp.AppResource, error) {
					return nil, mcp.ErrServerNotConnected
				}
			},
			wantStatus:    http.StatusBadGateway,
			wantErrorCode: appResourceErrUpstreamUnreachable,
		},
		{
			name:   "success payload shape",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(true), true, true, true)
			},
			wantStatus: http.StatusOK,
			validateBody: func(t *testing.T, body []byte) {
				var resp AppResourceResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Equal(t, testAppResourceOrigin, resp.ServerOrigin)
				require.Equal(t, testAppResourceURI, resp.URI)
				require.Equal(t, mcp.UIResourceMIMEType, resp.MIMEType)
				require.Equal(t, "<html>app</html>", resp.HTML)
				require.NotNil(t, resp.UIMeta)
				require.NotNil(t, resp.UIMeta.CSP)
				require.Equal(t, []string{"https://api.example"}, resp.UIMeta.CSP.ConnectDomains)
				require.NotNil(t, resp.UIMeta.PrefersBorder)
				require.True(t, *resp.UIMeta.PrefersBorder)
			},
		},
		{
			name:       "unauthenticated",
			caller:     "",
			query:      "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:   "generic upstream error",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) {
				seedDefaults(e, conversation.BoolPtr(true), true, true, true)
				e.mcp.readUserAppResource = func(_ context.Context, _, _, _ string) (*mcp.AppResource, error) {
					return nil, errors.New("boom")
				}
			},
			wantStatus:    http.StatusBadGateway,
			wantErrorCode: appResourceErrUpstreamUnreachable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("LogError", mock.Anything).Maybe()
			if tt.setup != nil {
				tt.setup(e)
			}

			req := httptest.NewRequest(http.MethodGet, "/mcp/app-resource?"+tt.query, nil)
			if tt.caller != "" {
				req.Header.Add("Mattermost-User-ID", tt.caller)
			}
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, rec, req)
			resp := rec.Result()
			require.Equal(t, tt.wantStatus, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			if tt.wantErrorCode != "" {
				var errResp AppResourceErrorResponse
				require.NoError(t, json.Unmarshal(body, &errResp))
				require.Equal(t, tt.wantErrorCode, errResp.ErrorCode)
				if tt.wantAuthURL != "" {
					require.Equal(t, tt.wantAuthURL, errResp.AuthURL)
				}
			}
			if tt.wantHTML != "" {
				var okResp AppResourceResponse
				require.NoError(t, json.Unmarshal(body, &okResp))
				require.Equal(t, tt.wantHTML, okResp.HTML)
			}
			if tt.validateBody != nil {
				tt.validateBody(t, body)
			}
		})
	}
}
