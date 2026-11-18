// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
)

func expectArtifactPostReads(m *mocks.MockClient, sessionID, channelID, postID, fileID, authorID, html string) {
	m.EXPECT().HasPermissionToFileAction(sessionID, fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true)
	m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
		Id: fileID, PostId: postID, Name: "notes.html", Extension: "html", Size: int64(len(html)),
	}, nil)
	m.EXPECT().GetPost(postID).Return(&model.Post{
		Id: postID, UserId: authorID, ChannelId: channelID, FileIds: model.StringArray{fileID},
	}, nil)
	m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
	m.EXPECT().GetConfig().Return(&model.Config{})
	m.EXPECT().GetFile(fileID).Return(io.NopCloser(strings.NewReader(html)), nil)
}

func enableLibrary(e *TestEnvironment) {
	e.config.enableHTMLArtifacts = true
	e.config.enableChannelArtifacts = true
}

func TestCreateChannelArtifactRecordsOnlyAgentAuthors(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name        string
		authorID    string
		wantBotID   string
		wantDisplay string
	}{
		{name: "person", authorID: testUserID},
		{name: "agent", authorID: testBotUserID, wantBotID: testBotUserID, wantDisplay: "Helper"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			enableLibrary(e)
			e.setupTestBot(llm.BotConfig{Name: "helper", DisplayName: "Helper"})
			svc := libraryService(newArtifactTokenKV())
			e.api.SetChannelArtifacts(svc)

			channelID, postID, fileID, sessionID := model.NewId(), model.NewId(), model.NewId(), model.NewId()
			html := "<html>notes</html>"
			m := mocks.NewMockClient(t)
			expectArtifactPostReads(m, sessionID, channelID, postID, fileID, tt.authorID, html)
			m.EXPECT().GetChannel(channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil)
			e.api.mmClient = m

			body := `{"channel_id":"` + channelID + `","post_id":"` + postID + `","file_id":"` + fileID + `","title":"Notes"}`
			rec := serveLibrary(e, http.MethodPost, "/channel-artifacts", body, sessionID)
			require.Equal(t, http.StatusCreated, rec.Code)
			var created channelartifacts.Entry
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
			assert.Equal(t, tt.wantBotID, created.SourceBotUserID)
			assert.Equal(t, tt.wantDisplay, created.SourceAgentDisplayName)
		})
	}
}

func TestCreateChannelArtifactFailsWhenTheChannelLookupFails(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	enableLibrary(e)
	svc := libraryService(newArtifactTokenKV())
	e.api.SetChannelArtifacts(svc)

	channelID, postID, fileID, sessionID := model.NewId(), model.NewId(), model.NewId(), model.NewId()
	html := "<html>notes</html>"
	m := mocks.NewMockClient(t)
	expectArtifactPostReads(m, sessionID, channelID, postID, fileID, testUserID, html)
	m.EXPECT().GetChannel(channelID).Return(nil, errors.New("db down"))
	e.api.mmClient = m

	body := `{"channel_id":"` + channelID + `","post_id":"` + postID + `","file_id":"` + fileID + `","title":"Notes"}`
	rec := serveLibrary(e, http.MethodPost, "/channel-artifacts", body, sessionID)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	list, err := svc.List(testUserID, channelID)
	require.NoError(t, err)
	assert.Empty(t, list.Entries)
}

func TestReplaceChannelArtifactAuditRecord(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	enableLibrary(e)
	svc := libraryService(newArtifactTokenKV())
	e.api.SetChannelArtifacts(svc)
	channelID := model.NewId()
	entry := saveLibraryEntry(t, svc, channelID, "Notes", "<html>v1</html>")

	postID, fileID, sessionID := model.NewId(), model.NewId(), model.NewId()
	html := "<html>v2</html>"
	m := mocks.NewMockClient(t)
	expectArtifactPostReads(m, sessionID, channelID, postID, fileID, testUserID, html)
	m.EXPECT().GetChannel(channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil)
	e.api.mmClient = m

	records := e.CaptureAuditRecords()
	body := `{"channel_id":"` + channelID + `","post_id":"` + postID + `","file_id":"` + fileID + `","replace_id":"` + entry.ID + `"}`
	rec := serveLibrary(e, http.MethodPost, "/channel-artifacts", body, sessionID)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, *records, 1)
	assert.Equal(t, AuditEventReplaceChannelArtifact, (*records)[0].EventName)
	assert.Equal(t, entry.ID, (*records)[0].EventData.Parameters[audit.KeyChannelArtifactID])
}

func TestChannelArtifactRoutesRejectMalformedSelectors(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name   string
		method string
		query  string
	}{
		{name: "delete with malformed purge escape", method: http.MethodDelete, query: "?purge=%zz"},
		{name: "delete with repeated purge", method: http.MethodDelete, query: "?purge=false&purge=true"},
		{name: "download with malformed version escape", method: http.MethodGet, query: "/download?version=%zz"},
		{name: "download with repeated version", method: http.MethodGet, query: "/download?version=1&version=2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			enableLibrary(e)
			kv := newArtifactTokenKV()
			svc := libraryService(kv)
			e.api.SetChannelArtifacts(svc)
			e.api.mmClient = kv
			entry := saveLibraryEntry(t, svc, model.NewId(), "Notes", "<html>v1</html>")
			_, err := svc.ReplaceContent(testUserID, entry.ID, channelartifacts.ReplaceRequest{
				PostID: model.NewId(), FileID: model.NewId(),
			}, []byte("<html>v2</html>"), "v2.html", testUserID, "Author", "", channelartifacts.ContentLimits{MaxSize: 1 << 20})
			require.NoError(t, err)

			rec := serveLibrary(e, tt.method, "/channel-artifacts/"+entry.ID+tt.query, "", "")
			require.Equal(t, http.StatusBadRequest, rec.Code)
			still, err := svc.GetReadable(testUserID, entry.ID)
			require.NoError(t, err)
			assert.Equal(t, 2, still.Version)
		})
	}
}

func TestChannelArtifactMutationsRejectOversizedBodies(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	id := model.NewId()
	huge := `{"title":"` + strings.Repeat("a", MaxAgentRequestBodyBytes) + `"}`
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "create", method: http.MethodPost, path: "/channel-artifacts"},
		{name: "replace", method: http.MethodPut, path: "/channel-artifacts/" + id + "/content"},
		{name: "patch", method: http.MethodPatch, path: "/channel-artifacts/" + id},
		{name: "rollback", method: http.MethodPost, path: "/channel-artifacts/" + id + "/rollback"},
		{name: "reorder", method: http.MethodPut, path: "/channel-artifacts/order"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			enableLibrary(e)
			e.api.SetChannelArtifacts(libraryService(newArtifactTokenKV()))

			rec := serveLibrary(e, tt.method, tt.path, huge, "")
			assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		})
	}
}

func TestMCPChannelArtifactsServeToolsInProcess(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	enableLibrary(e)
	kv := newArtifactTokenKV()
	svc := libraryService(kv)
	channelID := model.NewId()
	entry := saveLibraryEntry(t, svc, channelID, "Sprint notes", "<html>sprint</html>")

	tests := []struct {
		name    string
		enabled bool
		run     func(t *testing.T, m *MCPChannelArtifacts) error
		wantErr error
	}{
		{
			name: "list", enabled: true,
			run: func(t *testing.T, m *MCPChannelArtifacts) error {
				entries, err := m.List(t.Context(), testUserID, channelID)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				assert.Equal(t, entry.ID, entries[0].ID)
				return nil
			},
		},
		{
			name: "get truncates the content", enabled: true,
			run: func(t *testing.T, m *MCPChannelArtifacts) error {
				got, err := m.Get(t.Context(), testUserID, entry.ID, true, 4)
				require.NoError(t, err)
				assert.Equal(t, "<htm", got.Content)
				assert.True(t, got.HasMore)
				return nil
			},
		},
		{
			name: "get with an unknown id", enabled: true,
			run: func(t *testing.T, m *MCPChannelArtifacts) error {
				_, err := m.Get(t.Context(), testUserID, model.NewId(), false, 0)
				return err
			},
			wantErr: channelartifacts.ErrNotFound,
		},
		{
			name: "list while the feature is off",
			run: func(t *testing.T, m *MCPChannelArtifacts) error {
				_, err := m.List(t.Context(), testUserID, channelID)
				return err
			},
			wantErr: errChannelArtifactsUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.config.enableChannelArtifacts = tt.enabled
			m := NewMCPChannelArtifacts(svc, kv, e.config, e.bots)
			err := tt.run(t, m)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
