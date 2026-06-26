// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
)

func TestHandleRawFileContent(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	userID := model.NewId()
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
