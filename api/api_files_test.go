// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/search"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

func TestHandleRawFileContent(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	userID := model.NewId()
	sessionID := model.NewId()
	channelID := model.NewId()
	fileID := model.NewId()

	tests := []struct {
		name           string
		nilService     bool
		setup          func(m *mocks.MockClient)
		request        RawFileContentRequest
		omitUserHeader bool
		wantStatus     int
		assertResp     func(t *testing.T, resp RawFileContentResponse)
	}{
		{
			name:       "service unavailable returns 503",
			nilService: true,
			request:    RawFileContentRequest{FileID: fileID},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "invalid file id returns 400",
			request:    RawFileContentRequest{FileID: "too-short"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "no channel permission returns 403",
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, ChannelId: channelID}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(false)
			},
			request:    RawFileContentRequest{FileID: fileID},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "success returns content json",
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, Name: "notes.txt", MimeType: "text/plain", Content: "hello world",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
			},
			request:    RawFileContentRequest{FileID: fileID},
			wantStatus: http.StatusOK,
			assertResp: func(t *testing.T, resp RawFileContentResponse) {
				assert.True(t, resp.HasText)
				assert.Equal(t, "hello world", resp.Text)
				assert.Equal(t, "notes.txt", resp.Name)
				assert.Equal(t, 11, resp.TotalRunes)
				assert.False(t, resp.HasMore)
			},
		},
		{
			// The requesting user comes from the authenticated header, never the
			// body. With no header the permission check runs against an empty user
			// and must fail closed rather than leak the file.
			name: "missing user header is forbidden",
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, ChannelId: channelID}, nil)
				m.EXPECT().HasPermissionToChannel("", channelID, model.PermissionReadChannel).Return(false)
			},
			request:        RawFileContentRequest{FileID: fileID},
			omitUserHeader: true,
			wantStatus:     http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &API{}
			if !tt.nilService {
				m := mocks.NewMockClient(t)
				if model.IsValidId(tt.request.FileID) {
					m.EXPECT().HasPermissionToFileAction(
						sessionID,
						tt.request.FileID,
						model.AccessControlPolicyActionDownloadFileAttachment,
					).Return(true)
				}
				if tt.setup != nil {
					tt.setup(m)
				}
				a.fileService = files.New(m)
			}

			body, err := json.Marshal(tt.request)
			require.NoError(t, err)

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			req := httptest.NewRequest(http.MethodPost, "/files/content", bytes.NewReader(body))
			req = req.WithContext(auth.WithSessionID(req.Context(), sessionID))
			req.Header.Set("Content-Type", "application/json")
			if !tt.omitUserHeader {
				req.Header.Set("Mattermost-User-Id", userID)
			}
			c.Request = req

			a.handleRawFileContent(c)

			require.Equal(t, tt.wantStatus, rec.Code)
			if tt.assertResp != nil {
				var resp RawFileContentResponse
				require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
				tt.assertResp(t, resp)
			}
		})
	}
}

func TestRawFileContentPreservesPluginSession(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	userID := model.NewId()
	sessionID := model.NewId()
	channelID := model.NewId()
	fileID := model.NewId()

	m := mocks.NewMockClient(t)
	m.On(
		"HasPermissionToFileAction",
		sessionID,
		fileID,
		model.AccessControlPolicyActionDownloadFileAttachment,
	).Return(false).Once()

	adminMetadataReadCalled := false
	m.EXPECT().GetFileInfo(fileID).
		Run(func(string) { adminMetadataReadCalled = true }).
		Return(&model.FileInfo{Id: fileID, ChannelId: channelID, MimeType: "text/plain"}, nil).
		Maybe()
	adminContentReadCalled := false
	m.EXPECT().GetFile(fileID).
		Run(func(string) { adminContentReadCalled = true }).
		Return(io.NopCloser(strings.NewReader("sensitive contents")), nil).
		Maybe()
	e.api.fileService = files.New(m)

	body, err := json.Marshal(RawFileContentRequest{FileID: fileID})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/files/content", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mattermost-User-Id", userID)
	rec := httptest.NewRecorder()

	e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, adminMetadataReadCalled, "admin GetFileInfo must not run after the caller's file policy denies access")
	assert.False(t, adminContentReadCalled, "admin GetFile must not run after the caller's file policy denies access")
}

// TestPluginCallbackClientsReachRoutes sends requests built by the real MCP
// callback clients through the plugin router, the way the Mattermost server
// forwards /plugins/mattermost-ai/* (prefix stripped, user header set from the
// bearer token). A path mismatch between client and router yields gin's 404.
func TestPluginCallbackClientsReachRoutes(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	userID := model.NewId()
	sessionID := model.NewId()
	token := model.NewId()
	channelID := model.NewId()
	fileID := model.NewId()

	m := mocks.NewMockClient(t)
	m.EXPECT().HasPermissionToFileAction(sessionID, fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true)
	m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
		Id: fileID, ChannelId: channelID, Name: "notes.txt", MimeType: "text/plain", Content: "hello world",
	}, nil)
	m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
	e.api.fileService = files.New(m)

	const pluginPrefix = "/plugins/mattermost-ai"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, pluginPrefix+"/") || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		r.URL.Path = strings.TrimPrefix(r.URL.Path, pluginPrefix)
		r.Header.Set("Mattermost-User-Id", userID)
		e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, w, r)
	}))
	defer server.Close()

	ctx := context.WithValue(context.Background(), auth.AuthTokenContextKey, token)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, userID)

	content, err := tools.NewHTTPFileContentService(server.URL+pluginPrefix).GetContent(ctx, userID, fileID, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, "hello world", content.Text)

	// No search backend is configured, so reaching the handler yields its 503
	// message rather than gin's "404 page not found".
	_, err = tools.NewHTTPSemanticSearchService(server.URL+pluginPrefix).Search(ctx, "query", search.Options{UserID: userID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "semantic search is not available")
}
