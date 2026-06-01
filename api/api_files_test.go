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

	"github.com/mattermost/mattermost-plugin-agents/files"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
)

func TestHandleRawFileContent(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	userID := model.NewId()
	channelID := model.NewId()
	fileID := model.NewId()

	doPost := func(t *testing.T, a *API, body any) *httptest.ResponseRecorder {
		t.Helper()
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodPost, "/files/content", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Mattermost-User-Id", userID)
		c.Request = req
		a.handleRawFileContent(c)
		return rec
	}

	t.Run("service unavailable returns 503", func(t *testing.T) {
		a := &API{fileService: nil}
		rec := doPost(t, a, RawFileContentRequest{FileID: fileID})
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("invalid file id returns 400", func(t *testing.T) {
		m := mocks.NewMockClient(t)
		a := &API{fileService: files.New(m)}
		rec := doPost(t, a, RawFileContentRequest{FileID: "too-short"})
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("no channel permission returns 403", func(t *testing.T) {
		m := mocks.NewMockClient(t)
		m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, ChannelId: channelID}, nil)
		m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(false)
		a := &API{fileService: files.New(m)}

		rec := doPost(t, a, RawFileContentRequest{FileID: fileID})
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("success returns content json", func(t *testing.T) {
		m := mocks.NewMockClient(t)
		m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
			Id: fileID, ChannelId: channelID, Name: "notes.txt", MimeType: "text/plain", Content: "hello world",
		}, nil)
		m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
		a := &API{fileService: files.New(m)}

		rec := doPost(t, a, RawFileContentRequest{FileID: fileID})
		require.Equal(t, http.StatusOK, rec.Code)

		var resp RawFileContentResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		assert.True(t, resp.HasText)
		assert.Equal(t, "hello world", resp.Text)
		assert.Equal(t, "notes.txt", resp.Name)
		assert.Equal(t, 11, resp.TotalRunes)
		assert.False(t, resp.HasMore)
	})

	t.Run("missing user header is forbidden", func(t *testing.T) {
		// The requesting user comes from the authenticated header, never the
		// body. With no header the permission check runs against an empty user
		// and must fail closed rather than leak the file.
		m := mocks.NewMockClient(t)
		m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, ChannelId: channelID}, nil)
		m.EXPECT().HasPermissionToChannel("", channelID, model.PermissionReadChannel).Return(false)
		a := &API{fileService: files.New(m)}

		b, err := json.Marshal(RawFileContentRequest{FileID: fileID})
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodPost, "/files/content", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		// Intentionally no Mattermost-User-Id header.
		c.Request = req
		a.handleRawFileContent(c)

		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}
