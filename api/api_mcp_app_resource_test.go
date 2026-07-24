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

type readAppResourceCall struct {
	userID       string
	serverOrigin string
	uri          string
}

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

	seedDefaults := func(e *TestEnvironment, toolUseShared *bool, withUIMeta bool, serverConfigured bool) *[]readAppResourceCall {
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
			Shared:       toolUseShared,
		}
		if withUIMeta {
			toolUse.UIMeta = &llm.ToolUIMeta{ResourceURI: testAppResourceURI}
		}
		content, err := json.Marshal([]conversation.ContentBlock{toolUse})
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

		calls := &[]readAppResourceCall{}
		e.mcp.readUserAppResource = func(_ context.Context, userID, serverOrigin, uri string) (*mcp.AppResource, error) {
			*calls = append(*calls, readAppResourceCall{userID: userID, serverOrigin: serverOrigin, uri: uri})
			return successResource, nil
		}
		return calls
	}

	tests := []struct {
		name            string
		caller          string
		query           string
		setup           func(e *TestEnvironment) *[]readAppResourceCall
		wantStatus      int
		wantErrorCode   string
		wantAuthURL     string
		wantHTML        string
		wantManagerCall bool
		validateBody    func(t *testing.T, body []byte)
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
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				e.mockAPI.On("GetPost", testAppResourcePostID).Return(
					nil,
					model.NewAppError("GetPost", "app.post.get.app_error", nil, "", http.StatusNotFound),
				)
				return nil
			},
			wantStatus:    http.StatusNotFound,
			wantErrorCode: appResourceErrNotFound,
		},
		{
			name:   "caller lacks PermissionReadChannel",
			caller: testOtherUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				e.mockAPI.On("GetPost", testAppResourcePostID).Return(&model.Post{
					Id:        testAppResourcePostID,
					ChannelId: testChannelID,
				}, nil)
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, testChannelID, model.PermissionReadChannel).Return(false)
				return nil
			},
			wantStatus:    http.StatusForbidden,
			wantErrorCode: appResourceErrForbidden,
		},
		{
			name:   "no turn for post",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				e.mockAPI.On("GetPost", testAppResourcePostID).Return(&model.Post{
					Id:        testAppResourcePostID,
					ChannelId: testChannelID,
				}, nil)
				e.mockAPI.On("HasPermissionToChannel", testUserID, testChannelID, model.PermissionReadChannel).Return(true)
				return nil
			},
			wantStatus:    http.StatusNotFound,
			wantErrorCode: appResourceErrNotFound,
		},
		{
			name:   "tool_call_id not in post span",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=missing",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				return seedDefaults(e, conversation.BoolPtr(true), true, true)
			},
			wantStatus:    http.StatusNotFound,
			wantErrorCode: appResourceErrNotFound,
		},
		{
			name:   "tool_use has no UIMeta",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				return seedDefaults(e, conversation.BoolPtr(true), false, true)
			},
			wantStatus:    http.StatusNotFound,
			wantErrorCode: appResourceErrNotFound,
		},
		{
			name:   "requester, tool_use unshared",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				return seedDefaults(e, conversation.BoolPtr(false), true, true)
			},
			wantStatus:      http.StatusOK,
			wantHTML:        "<html>app</html>",
			wantManagerCall: true,
		},
		{
			name:   "requester, tool_use shared",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				return seedDefaults(e, conversation.BoolPtr(true), true, true)
			},
			wantStatus:      http.StatusOK,
			wantHTML:        "<html>app</html>",
			wantManagerCall: true,
		},
		{
			name:   "onlooker with channel access, tool_use shared",
			caller: testOtherUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				return seedDefaults(e, conversation.BoolPtr(true), true, true)
			},
			wantStatus:      http.StatusOK,
			wantHTML:        "<html>app</html>",
			wantManagerCall: true,
		},
		{
			name:   "onlooker, tool_use unshared",
			caller: testOtherUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				return seedDefaults(e, conversation.BoolPtr(false), true, true)
			},
			wantStatus:      http.StatusForbidden,
			wantErrorCode:   appResourceErrForbidden,
			wantManagerCall: false,
		},
		{
			name:   "server origin no longer configured",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				calls := seedDefaults(e, conversation.BoolPtr(true), true, true)
				e.mcp.readUserAppResource = func(_ context.Context, userID, serverOrigin, uri string) (*mcp.AppResource, error) {
					*calls = append(*calls, readAppResourceCall{userID: userID, serverOrigin: serverOrigin, uri: uri})
					return nil, mcp.ErrServerNotConfigured
				}
				return calls
			},
			wantStatus:      http.StatusNotFound,
			wantErrorCode:   appResourceErrNotFound,
			wantManagerCall: true,
		},
		{
			name:   "manager returns OAuthNeededError",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				calls := seedDefaults(e, conversation.BoolPtr(true), true, true)
				e.mcp.readUserAppResource = func(_ context.Context, userID, serverOrigin, uri string) (*mcp.AppResource, error) {
					*calls = append(*calls, readAppResourceCall{userID: userID, serverOrigin: serverOrigin, uri: uri})
					return nil, mcp.NewOAuthNeededError("https://auth.example/start")
				}
				return calls
			},
			wantStatus:      http.StatusUnauthorized,
			wantErrorCode:   appResourceErrAuthRequired,
			wantAuthURL:     "https://auth.example/start",
			wantManagerCall: true,
		},
		{
			name:   "manager returns InvalidAppResourceError",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				calls := seedDefaults(e, conversation.BoolPtr(true), true, true)
				e.mcp.readUserAppResource = func(_ context.Context, userID, serverOrigin, uri string) (*mcp.AppResource, error) {
					*calls = append(*calls, readAppResourceCall{userID: userID, serverOrigin: serverOrigin, uri: uri})
					return nil, &mcp.InvalidAppResourceError{URI: testAppResourceURI, MIMEType: "text/html"}
				}
				return calls
			},
			wantStatus:      http.StatusBadGateway,
			wantErrorCode:   appResourceErrInvalidResourceMime,
			wantManagerCall: true,
		},
		{
			name:   "manager returns ErrServerNotConnected",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				calls := seedDefaults(e, conversation.BoolPtr(true), true, true)
				e.mcp.readUserAppResource = func(_ context.Context, userID, serverOrigin, uri string) (*mcp.AppResource, error) {
					*calls = append(*calls, readAppResourceCall{userID: userID, serverOrigin: serverOrigin, uri: uri})
					return nil, mcp.ErrServerNotConnected
				}
				return calls
			},
			wantStatus:      http.StatusBadGateway,
			wantErrorCode:   appResourceErrUpstreamUnreachable,
			wantManagerCall: true,
		},
		{
			name:   "502 bodies are non-sensitive",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				calls := seedDefaults(e, conversation.BoolPtr(true), true, true)
				e.mcp.readUserAppResource = func(_ context.Context, userID, serverOrigin, uri string) (*mcp.AppResource, error) {
					*calls = append(*calls, readAppResourceCall{userID: userID, serverOrigin: serverOrigin, uri: uri})
					return nil, errors.New("dial tcp 10.0.0.1:443: secret internals")
				}
				return calls
			},
			wantStatus:      http.StatusBadGateway,
			wantErrorCode:   appResourceErrUpstreamUnreachable,
			wantManagerCall: true,
			validateBody: func(t *testing.T, body []byte) {
				require.NotContains(t, string(body), "10.0.0.1")
				require.NotContains(t, string(body), "secret")
				var errResp AppResourceErrorResponse
				require.NoError(t, json.Unmarshal(body, &errResp))
				require.Equal(t, "MCP server unreachable", errResp.Message)
			},
		},
		{
			name:   "success payload is ReadResourceResult wire shape",
			caller: testUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				return seedDefaults(e, conversation.BoolPtr(true), true, true)
			},
			wantStatus:      http.StatusOK,
			wantManagerCall: true,
			validateBody: func(t *testing.T, body []byte) {
				raw := string(body)
				require.Contains(t, raw, `"contents"`)
				require.Contains(t, raw, `"mimeType"`)
				require.Contains(t, raw, `"text"`)
				require.Contains(t, raw, `"_meta"`)
				require.Contains(t, raw, `"connectDomains"`)
				require.Contains(t, raw, `"prefersBorder"`)
				require.NotContains(t, raw, `"mime_type"`)
				require.NotContains(t, raw, `"html"`)
				require.NotContains(t, raw, `"ui_meta"`)
				require.NotContains(t, raw, `"server_origin"`)

				var resp AppResourceResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Len(t, resp.Contents, 1)
				require.Equal(t, testAppResourceURI, resp.Contents[0].URI)
				require.Equal(t, mcp.UIResourceMIMEType, resp.Contents[0].MIMEType)
				require.Equal(t, "<html>app</html>", resp.Contents[0].Text)
				require.NotNil(t, resp.Contents[0].Meta)
				require.NotNil(t, resp.Contents[0].Meta.UI)
				require.Equal(t, []string{"https://api.example"}, resp.Contents[0].Meta.UI.CSP.ConnectDomains)
			},
		},
		{
			name:       "unauthenticated",
			caller:     "",
			query:      "post_id=" + testAppResourcePostID + "&tool_call_id=tc1",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:   "duplicate tool_call_id across rounds does not cross-authorize",
			caller: testOtherUserID,
			query:  "post_id=" + testAppResourcePostID + "&tool_call_id=reuse",
			setup: func(e *TestEnvironment) *[]readAppResourceCall {
				channelID := testChannelID
				e.config.mcpConfig = mcp.Config{
					Enabled: true,
					Servers: []mcp.ServerConfig{{Name: "srv-a", BaseURL: testAppResourceOrigin, Enabled: true}},
				}
				e.conversationStore.conversations[testAppResourceConvID] = &store.Conversation{
					ID: testAppResourceConvID, UserID: testUserID, BotID: testBotUserID, ChannelID: &channelID,
				}
				postA := testAppResourcePostID
				postB := "postb23456789012345678901b"
				uiMeta := &llm.ToolUIMeta{ResourceURI: testAppResourceURI}
				unshared, _ := json.Marshal([]conversation.ContentBlock{{
					Type: conversation.BlockTypeToolUse, ID: "reuse", ServerOrigin: testAppResourceOrigin,
					Shared: conversation.BoolPtr(false), UIMeta: uiMeta,
				}})
				shared, _ := json.Marshal([]conversation.ContentBlock{{
					Type: conversation.BlockTypeToolUse, ID: "reuse", ServerOrigin: testAppResourceOrigin,
					Shared: conversation.BoolPtr(true), UIMeta: uiMeta,
				}})
				e.conversationStore.turns[testAppResourceConvID] = []store.Turn{
					{ID: "t1", ConversationID: testAppResourceConvID, PostID: &postA, Role: "assistant", Content: unshared, Sequence: 1},
					{ID: "t2", ConversationID: testAppResourceConvID, PostID: &postB, Role: "assistant", Content: shared, Sequence: 2},
				}
				e.conversationStore.turnsByPost[postA] = &e.conversationStore.turns[testAppResourceConvID][0]
				e.mockAPI.On("GetPost", postA).Return(&model.Post{Id: postA, ChannelId: testChannelID}, nil)
				e.mockAPI.On("HasPermissionToChannel", testOtherUserID, testChannelID, model.PermissionReadChannel).Return(true)
				calls := &[]readAppResourceCall{}
				e.mcp.readUserAppResource = func(_ context.Context, userID, serverOrigin, uri string) (*mcp.AppResource, error) {
					*calls = append(*calls, readAppResourceCall{userID: userID, serverOrigin: serverOrigin, uri: uri})
					return successResource, nil
				}
				return calls
			},
			wantStatus:      http.StatusForbidden,
			wantErrorCode:   appResourceErrForbidden,
			wantManagerCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("LogError", mock.Anything).Maybe()
			var calls *[]readAppResourceCall
			if tt.setup != nil {
				calls = tt.setup(e)
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
				require.Len(t, okResp.Contents, 1)
				require.Equal(t, tt.wantHTML, okResp.Contents[0].Text)
			}
			if tt.validateBody != nil {
				tt.validateBody(t, body)
			}
			if calls != nil {
				if tt.wantManagerCall {
					require.Len(t, *calls, 1)
					require.Equal(t, tt.caller, (*calls)[0].userID)
					require.Equal(t, testAppResourceOrigin, (*calls)[0].serverOrigin)
					require.Equal(t, testAppResourceURI, (*calls)[0].uri)
				} else {
					require.Empty(t, *calls)
				}
			}
		})
	}
}
