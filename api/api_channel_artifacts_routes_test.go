// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type adminChannelMembers struct{}

func (adminChannelMembers) GetMember(string, string) (*model.ChannelMember, error) {
	return &model.ChannelMember{SchemeAdmin: true}, nil
}

func libraryService(kv *artifactTokenKV) *channelartifacts.Service {
	return channelartifacts.NewService(channelartifacts.NewStore(kv), kv, adminChannelMembers{})
}

func saveLibraryEntry(t *testing.T, svc *channelartifacts.Service, channelID, title, html string) *channelartifacts.Entry {
	t.Helper()
	entry, err := svc.CreateFromHTML(testUserID, channelartifacts.CreateRequest{
		ChannelID: channelID,
		PostID:    model.NewId(),
		FileID:    model.NewId(),
		Title:     title,
	}, []byte(html), title+".html", testUserID, "Author", "", model.NewId(), channelartifacts.ContentLimits{MaxSize: 1 << 20})
	require.NoError(t, err)
	return entry
}

func serveLibrary(e *TestEnvironment, method, path, body string, sessionID string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Mattermost-User-Id", testUserID)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, rec, req)
	return rec
}

func TestCreateChannelArtifactGuards(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	channelID := model.NewId()
	postID := model.NewId()
	fileID := model.NewId()
	sessionID := model.NewId()
	html := "<html>notes</html>"

	tests := []struct {
		name       string
		enabled    bool
		humanOnly  bool
		body       string
		setup      func(m *mocks.MockClient)
		wantStatus int
		wantBody   string
	}{
		{name: "feature off", wantStatus: http.StatusNotFound, wantBody: "not found"},
		{
			name: "invalid ids", enabled: true,
			body:       `{"channel_id":"nope","post_id":"nope","file_id":"nope"}`,
			wantStatus: http.StatusBadRequest, wantBody: "invalid request",
		},
		{
			name: "human authored while restricted", enabled: true, humanOnly: true,
			body: `{"channel_id":"` + channelID + `","post_id":"` + postID + `","file_id":"` + fileID + `"}`,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().HasPermissionToFileAction(sessionID, fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true)
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, PostId: postID, Name: "notes.html", Extension: "html", Size: int64(len(html))}, nil)
				m.EXPECT().GetPost(postID).Return(&model.Post{Id: postID, UserId: testUserID, ChannelId: channelID, FileIds: model.StringArray{fileID}}, nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			wantStatus: http.StatusNotFound, wantBody: "not found",
		},
		{
			name: "larger than the render limit", enabled: true,
			body: `{"channel_id":"` + channelID + `","post_id":"` + postID + `","file_id":"` + fileID + `"}`,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().HasPermissionToFileAction(sessionID, fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true)
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, PostId: postID, Name: "notes.html", Extension: "html", Size: 2 << 20}, nil)
				m.EXPECT().GetPost(postID).Return(&model.Post{Id: postID, UserId: testUserID, ChannelId: channelID, FileIds: model.StringArray{fileID}}, nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
				m.EXPECT().GetConfig().Return(&model.Config{})
			},
			wantStatus: http.StatusRequestEntityTooLarge, wantBody: "artifact too large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.config.enableHTMLArtifacts = tt.enabled
			e.config.enableChannelArtifacts = tt.enabled
			e.config.disableHumanChannelArtifacts = tt.humanOnly
			kv := newArtifactTokenKV()
			e.api.SetChannelArtifacts(libraryService(kv))
			if tt.setup != nil {
				m := mocks.NewMockClient(t)
				tt.setup(m)
				e.api.mmClient = m
			}

			rec := serveLibrary(e, http.MethodPost, "/channel-artifacts", tt.body, sessionID)
			require.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestMCPCanSaveChannelArtifact(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	postID := model.NewId()
	fileID := model.NewId()
	channelID := model.NewId()
	body := `{"post_id":"` + postID + `","file_id":"` + fileID + `"}`

	tests := []struct {
		name       string
		enabled    bool
		humanOnly  bool
		client     bool
		setup      func(m *mocks.MockClient)
		wantStatus int
		wantError  string
	}{
		{name: "feature off", wantStatus: http.StatusServiceUnavailable, wantError: "channel artifacts are not available"},
		{
			name: "non-html file", enabled: true, client: true,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, Name: "notes.txt", Extension: "txt"}, nil)
			},
			wantStatus: http.StatusNotFound, wantError: "file is not an HTML artifact",
		},
		{
			name: "human authored while restricted", enabled: true, humanOnly: true, client: true,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, Name: "notes.html", Extension: "html"}, nil)
				m.EXPECT().GetPost(postID).Return(&model.Post{Id: postID, UserId: testUserID, ChannelId: channelID, FileIds: model.StringArray{fileID}}, nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			wantStatus: http.StatusForbidden, wantError: "channel artifacts only accept files posted by agents",
		},
		{
			name: "larger than the render limit", enabled: true, client: true,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, Name: "notes.html", Extension: "html", Size: 2 << 20}, nil)
				m.EXPECT().GetPost(postID).Return(&model.Post{Id: postID, UserId: testUserID, ChannelId: channelID, FileIds: model.StringArray{fileID}}, nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
				m.EXPECT().GetConfig().Return(&model.Config{})
			},
			wantStatus: http.StatusRequestEntityTooLarge, wantError: "artifact too large",
		},
		{
			name: "html file on the post", enabled: true, client: true,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, Name: "notes.html", Extension: "html"}, nil)
				m.EXPECT().GetPost(postID).Return(&model.Post{Id: postID, UserId: testUserID, ChannelId: channelID, FileIds: model.StringArray{fileID}}, nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
				m.EXPECT().GetConfig().Return(&model.Config{})
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.config.enableHTMLArtifacts = tt.enabled
			e.config.enableChannelArtifacts = tt.enabled
			e.config.disableHumanChannelArtifacts = tt.humanOnly
			e.api.SetChannelArtifacts(libraryService(newArtifactTokenKV()))
			if tt.client {
				m := mocks.NewMockClient(t)
				tt.setup(m)
				e.api.mmClient = m
			}

			rec := serveLibrary(e, http.MethodPost, "/channel-artifacts/mcp/can-save", body, "")
			require.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantError != "" {
				var got struct {
					Error string `json:"error"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
				assert.Equal(t, tt.wantError, got.Error)
				return
			}
			assert.JSONEq(t, `{"ok":true}`, rec.Body.String())
		})
	}
}

func TestDownloadChannelArtifactDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.config.enableHTMLArtifacts = true
	e.config.enableChannelArtifacts = true
	e.config.disableChannelArtifactDownload = true
	e.api.SetChannelArtifacts(libraryService(newArtifactTokenKV()))

	rec := serveLibrary(e, http.MethodGet, "/channel-artifacts/"+model.NewId()+"/download", "", "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "channel artifact download is disabled", rec.Body.String())
}

func TestGetChannelArtifactRenderingDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name       string
		iframe     bool
		wantStatus int
		wantBody   string
	}{
		{name: "top-level visit explains that rendering is disabled", wantStatus: http.StatusOK, wantBody: "Channel artifact rendering is disabled"},
		{name: "iframe load is refused", iframe: true, wantStatus: http.StatusForbidden, wantBody: "channel artifact rendering is disabled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.config.enableHTMLArtifacts = true
			e.config.enableChannelArtifacts = true
			e.config.disableChannelArtifactRendering = true
			kv := newArtifactTokenKV()
			svc := libraryService(kv)
			e.api.SetChannelArtifacts(svc)
			e.api.mmClient = kv
			entry := saveLibraryEntry(t, svc, model.NewId(), "Notes", "<html>v1</html>")

			req := httptest.NewRequest(http.MethodGet, "/channel-artifacts/"+entry.ID+"?n="+testArtifactNonce, nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			if tt.iframe {
				req.Header.Set("Sec-Fetch-Dest", "iframe")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
			}
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, rec, req)

			require.Equal(t, tt.wantStatus, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.wantBody)
		})
	}
}

func TestDeleteChannelArtifactPurgeAndRestore(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.config.enableHTMLArtifacts = true
	e.config.enableChannelArtifacts = true
	kv := newArtifactTokenKV()
	svc := libraryService(kv)
	e.api.SetChannelArtifacts(svc)
	e.api.mmClient = kv

	channelID := model.NewId()
	entry := saveLibraryEntry(t, svc, channelID, "Notes", "<html>v1</html>")
	_, err := svc.ReplaceContent(testUserID, entry.ID, channelartifacts.ReplaceRequest{
		PostID: model.NewId(), FileID: model.NewId(),
	}, []byte("<html>v2</html>"), "v2.html", testUserID, "Author", "", channelartifacts.ContentLimits{MaxSize: 1 << 20})
	require.NoError(t, err)

	rejected := serveLibrary(e, http.MethodDelete, "/channel-artifacts/"+entry.ID+"?purge=treu", "", "")
	require.Equal(t, http.StatusBadRequest, rejected.Code)
	still, err := svc.GetReadable(testUserID, entry.ID)
	require.NoError(t, err)
	require.Equal(t, 2, still.Version)

	restored := serveLibrary(e, http.MethodDelete, "/channel-artifacts/"+entry.ID, "", "")
	require.Equal(t, http.StatusOK, restored.Code)
	var got channelartifacts.Entry
	require.NoError(t, json.Unmarshal(restored.Body.Bytes(), &got))
	assert.Equal(t, 1, got.Version)
	assert.Equal(t, "Notes", got.Title)

	purged := serveLibrary(e, http.MethodDelete, "/channel-artifacts/"+entry.ID+"?purge=true", "", "")
	require.Equal(t, http.StatusNoContent, purged.Code)
	_, err = svc.GetReadable(testUserID, entry.ID)
	require.ErrorIs(t, err, channelartifacts.ErrNotFound)
}

func TestMCPListAndGetChannelArtifact(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.config.enableHTMLArtifacts = true
	e.config.enableChannelArtifacts = true
	kv := newArtifactTokenKV()
	svc := libraryService(kv)
	e.api.SetChannelArtifacts(svc)
	e.api.mmClient = kv

	channelID := model.NewId()
	entry := saveLibraryEntry(t, svc, channelID, "Sprint notes", "<html>sprint</html>")

	listed := serveLibrary(e, http.MethodPost, "/channel-artifacts/mcp/list", `{"channel_id":"`+channelID+`"}`, "")
	require.Equal(t, http.StatusOK, listed.Code)
	var list MCPListChannelArtifactsResponse
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &list))
	require.Len(t, list.Entries, 1)
	assert.Equal(t, entry.ID, list.Entries[0].ID)
	assert.Equal(t, "Sprint notes", list.Entries[0].Title)

	got := serveLibrary(e, http.MethodPost, "/channel-artifacts/mcp/get", `{"artifact_id":"`+entry.ID+`","content_limit":4}`, "")
	require.Equal(t, http.StatusOK, got.Code)
	var view MCPGetChannelArtifactResponse
	require.NoError(t, json.Unmarshal(got.Body.Bytes(), &view))
	assert.Equal(t, entry.ID, view.Entry.ID)
	assert.True(t, view.ContentIncluded)
	assert.Equal(t, "<htm", view.Content)
	assert.True(t, view.HasMore)
}

func TestChannelArtifactAuditRecordsNameTheObjects(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	t.Run("create", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)
		e.config.enableHTMLArtifacts = true
		e.config.enableChannelArtifacts = true
		kv := newArtifactTokenKV()
		e.api.SetChannelArtifacts(libraryService(kv))

		channelID := model.NewId()
		postID := model.NewId()
		fileID := model.NewId()
		sessionID := model.NewId()
		html := "<html>notes</html>"
		m := mocks.NewMockClient(t)
		m.EXPECT().HasPermissionToFileAction(sessionID, fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true)
		m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
			Id: fileID, PostId: postID, Name: "notes.html", Extension: "html", Size: int64(len(html)),
		}, nil)
		m.EXPECT().GetPost(postID).Return(&model.Post{
			Id: postID, UserId: testUserID, ChannelId: channelID, FileIds: model.StringArray{fileID},
		}, nil)
		m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
		m.EXPECT().GetConfig().Return(&model.Config{})
		m.EXPECT().GetFile(fileID).Return(io.NopCloser(strings.NewReader(html)), nil)
		m.EXPECT().GetUser(testUserID).Return(&model.User{Id: testUserID, Username: "user"}, nil)
		m.EXPECT().GetChannel(channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil)
		e.api.mmClient = m

		records := e.CaptureAuditRecords()
		rec := serveLibrary(e, http.MethodPost, "/channel-artifacts", `{"channel_id":"`+channelID+`","post_id":"`+postID+`","file_id":"`+fileID+`","title":"Notes"}`, sessionID)
		require.Equal(t, http.StatusCreated, rec.Code)
		var created channelartifacts.Entry
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
		require.Len(t, *records, 1)
		assert.Equal(t, AuditEventCreateChannelArtifact, (*records)[0].EventName)
		assert.Equal(t, created.ID, (*records)[0].EventData.Parameters[audit.KeyChannelArtifactID])
		assert.Equal(t, channelID, (*records)[0].EventData.Parameters[audit.KeyChannelID])
	})

	t.Run("delete", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)
		e.config.enableHTMLArtifacts = true
		e.config.enableChannelArtifacts = true
		kv := newArtifactTokenKV()
		svc := libraryService(kv)
		e.api.SetChannelArtifacts(svc)
		entry := saveLibraryEntry(t, svc, model.NewId(), "Notes", "<html>notes</html>")

		records := e.CaptureAuditRecords()
		rec := serveLibrary(e, http.MethodDelete, "/channel-artifacts/"+entry.ID+"?purge=true", "", "")
		require.Equal(t, http.StatusNoContent, rec.Code)
		require.Len(t, *records, 1)
		assert.Equal(t, AuditEventDeleteChannelArtifact, (*records)[0].EventName)
		assert.Equal(t, entry.ID, (*records)[0].EventData.Parameters[audit.KeyChannelArtifactID])
	})

	t.Run("reorder", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)
		e.config.enableHTMLArtifacts = true
		e.config.enableChannelArtifacts = true
		kv := newArtifactTokenKV()
		svc := libraryService(kv)
		e.api.SetChannelArtifacts(svc)
		channelID := model.NewId()
		entry := saveLibraryEntry(t, svc, channelID, "Notes", "<html>notes</html>")

		records := e.CaptureAuditRecords()
		body := `{"channel_id":"` + channelID + `","ids":["` + entry.ID + `"]}`
		rec := serveLibrary(e, http.MethodPut, "/channel-artifacts/order", body, "")
		require.Equal(t, http.StatusNoContent, rec.Code)
		require.Len(t, *records, 1)
		assert.Equal(t, AuditEventReorderChannelArtifacts, (*records)[0].EventName)
		assert.Equal(t, channelID, (*records)[0].EventData.Parameters[audit.KeyChannelID])
	})

	t.Run("export", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)
		e.config.enableHTMLArtifacts = true
		e.config.enableChannelArtifacts = true
		e.api.SetChannelArtifacts(libraryService(newArtifactTokenKV()))
		e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true)
		channelID := model.NewId()

		records := e.CaptureAuditRecords()
		rec := serveLibrary(e, http.MethodGet, "/channel-artifacts/export?channel_id="+channelID, "", "")
		require.Equal(t, http.StatusOK, rec.Code)
		require.Len(t, *records, 1)
		assert.Equal(t, AuditEventExportChannelArtifacts, (*records)[0].EventName)
		assert.Equal(t, channelID, (*records)[0].EventData.Parameters[audit.KeyChannelID])
	})
}
