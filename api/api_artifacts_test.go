// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

func TestHandleGetArtifact(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	sessionID := model.NewId()
	fileID := model.NewId()
	postID := model.NewId()
	channelID := model.NewId()
	const htmlDoc = "<html><head><title>x</title></head><body><script>run()</script></body></html>"

	htmlInfo := func() *model.FileInfo {
		return &model.FileInfo{Id: fileID, PostId: postID, ChannelId: channelID, Name: "chart.html", Extension: "html", Size: int64(len(htmlDoc))}
	}
	botPost := func() *model.Post {
		return &model.Post{Id: postID, UserId: testBotUserID, ChannelId: channelID, FileIds: model.StringArray{fileID}}
	}

	tests := []struct {
		name       string
		disabled   bool
		fileID     string
		setup      func(m *mocks.MockClient)
		wantStatus int
	}{
		{name: "disabled returns 404", disabled: true, fileID: fileID, wantStatus: http.StatusNotFound},
		{name: "invalid file id returns 400", fileID: "bad", wantStatus: http.StatusBadRequest},
		{
			name:   "file info not found returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(nil, &model.AppError{Message: "nope"})
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "non-html file returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				info := htmlInfo()
				info.Name, info.Extension = "notes.txt", "txt"
				m.EXPECT().GetFileInfo(fileID).Return(info, nil)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "file without post returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				info := htmlInfo()
				info.PostId = ""
				m.EXPECT().GetFileInfo(fileID).Return(info, nil)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "post not found returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				m.EXPECT().GetPost(postID).Return(nil, &model.AppError{Message: "nope"})
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "file not attached to post returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				p := botPost()
				p.FileIds = model.StringArray{model.NewId()}
				m.EXPECT().GetPost(postID).Return(p, nil)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "post by non-bot user returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				p := botPost()
				p.UserId = model.NewId()
				m.EXPECT().GetPost(postID).Return(p, nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "non-bot post in unreadable channel returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				p := botPost()
				p.UserId = model.NewId()
				m.EXPECT().GetPost(postID).Return(p, nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(false)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "viewer without channel permission returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				m.EXPECT().GetPost(postID).Return(botPost(), nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(false)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "file over size limit returns 413",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				info := htmlInfo()
				info.Size = 11
				m.EXPECT().GetFileInfo(fileID).Return(info, nil)
				m.EXPECT().GetPost(postID).Return(botPost(), nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
				m.EXPECT().GetConfig().Return(&model.Config{FileSettings: model.FileSettings{MaxFileSize: model.NewPointer(int64(10))}})
			},
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:   "success serves injected html",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				m.EXPECT().GetPost(postID).Return(botPost(), nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
				m.EXPECT().GetConfig().Return(&model.Config{})
				m.EXPECT().GetFile(fileID).Return(io.NopCloser(strings.NewReader(htmlDoc)), nil)
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})
			e.config.enableHTMLArtifacts = !tt.disabled

			m := mocks.NewMockClient(t)
			m.EXPECT().HasPermissionToFileAction(sessionID, tt.fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true).Maybe()
			if tt.setup != nil {
				tt.setup(m)
			}
			e.api.mmClient = m

			req := httptest.NewRequest(http.MethodGet, "/artifacts/"+tt.fileID, nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, rec, req)

			require.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
			if tt.wantStatus != http.StatusOK {
				assert.NotContains(t, rec.Body.String(), "run()")
				return
			}
			assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
			assert.Equal(t, artifactCSP, rec.Header().Get("Content-Security-Policy"))
			assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "sandbox allow-scripts;")
			assert.NotContains(t, rec.Header().Get("Content-Security-Policy"), "allow-same-origin")
			assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
			assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Resource-Policy"))
			assert.Equal(t, "inline", rec.Header().Get("Content-Disposition"))
			assert.Equal(t, string(injectArtifactBridge([]byte(htmlDoc))), rec.Body.String())
		})
	}
}

func TestInjectArtifactBridge(t *testing.T) {
	bridge := "<script>" + artifactBridgeJS + "</script>"

	tests := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "after head",
			doc:  "<!doctype html><html><head><script>a()</script></head><body></body></html>",
			want: "<!doctype html><html><head>" + bridge + "<script>a()</script></head><body></body></html>",
		},
		{
			name: "after head with attributes",
			doc:  `<html lang="en"><head data-x="1"><script>a()</script></head></html>`,
			want: `<html lang="en"><head data-x="1">` + bridge + "<script>a()</script></head></html>",
		},
		{
			name: "uppercase tags",
			doc:  "<HTML><HEAD><SCRIPT>a()</SCRIPT></HEAD></HTML>",
			want: "<HTML><HEAD>" + bridge + "<SCRIPT>a()</SCRIPT></HEAD></HTML>",
		},
		{
			name: "header element is not head",
			doc:  "<html><body><header>h</header><script>a()</script></body></html>",
			want: "<html>" + bridge + "<body><header>h</header><script>a()</script></body></html>",
		},
		{
			name: "only html tag",
			doc:  `<html class="c"><body><script>a()</script></body></html>`,
			want: `<html class="c">` + bridge + "<body><script>a()</script></body></html>",
		},
		{
			name: "bom and doctype",
			doc:  "\xEF\xBB\xBF<!DOCTYPE html>\n<html>\n<head><script>a()</script></head></html>",
			want: "\xEF\xBB\xBF<!DOCTYPE html>\n<html>\n<head>" + bridge + "<script>a()</script></head></html>",
		},
		{
			name: "comment containing head before real head",
			doc:  "<!-- <head> --><!doctype html><html><head><script>a()</script></head></html>",
			want: "<!-- <head> --><!doctype html><html><head>" + bridge + "<script>a()</script></head></html>",
		},
		{
			name: "head text inside earlier script",
			doc:  "<script>a('<head>')</script><div>x</div>",
			want: bridge + "<script>a('<head>')</script><div>x</div>",
		},
		{
			name: "head text inside script after html",
			doc:  "<html><script>a('<head>')</script></html>",
			want: "<html>" + bridge + "<script>a('<head>')</script></html>",
		},
		{
			name: "bare header element",
			doc:  "<header>h</header><script>a()</script>",
			want: bridge + "<header>h</header><script>a()</script>",
		},
		{
			name: "head without html",
			doc:  "<!doctype html><HEAD><script>a()</script></HEAD>",
			want: "<!doctype html><HEAD>" + bridge + "<script>a()</script></HEAD>",
		},
		{
			name: "gt inside double-quoted html attribute",
			doc:  `<html data-note="a > b"><head><script>a()</script></head></html>`,
			want: `<html data-note="a > b"><head>` + bridge + "<script>a()</script></head></html>",
		},
		{
			name: "gt inside single-quoted head attribute",
			doc:  `<html><head data-x = 'x>"y'><script>a()</script></head></html>`,
			want: `<html><head data-x = 'x>"y'>` + bridge + "<script>a()</script></head></html>",
		},
		{
			name: "quote not after equals does not open a value",
			doc:  `<html a"b><head><script>a()</script></head></html>`,
			want: `<html a"b><head>` + bridge + "<script>a()</script></head></html>",
		},
		{
			name: "unterminated quoted attribute",
			doc:  `<html data-x="a><script>a()</script>`,
			want: bridge + `<html data-x="a><script>a()</script>`,
		},
		{
			name: "neither tag",
			doc:  "<div>hi</div><script>a()</script>",
			want: bridge + "<div>hi</div><script>a()</script>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(injectArtifactBridge([]byte(tt.doc)))
			require.Equal(t, tt.want, got)
			assert.Equal(t, 1, strings.Count(got, artifactBridgeJS))
			assert.Less(t, strings.Index(got, artifactBridgeJS), strings.Index(strings.ToLower(got), "a("))
		})
	}
}
