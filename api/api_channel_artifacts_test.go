// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost/server/public/model"
)

func init() {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
}

func TestChannelArtifactDefaultsMatchTheConfigDefaults(t *testing.T) {
	assert.Equal(t, config.DefaultChannelArtifactsMaxPerChannel, channelartifacts.DefaultMaxPerChannel)
	assert.Equal(t, config.DefaultChannelArtifactVersionsToKeep, channelartifacts.DefaultVersionsToKeep)
}

func TestChannelArtifactLifecycleThroughTheRoutes(t *testing.T) {
	f := newLibraryFixture(t)
	postID, fileID := f.addSource(t, f.botID, "<html>v1</html>")

	created := f.do(http.MethodPost, "/channel-artifacts", f.createBody(postID, fileID, `,"title":"Sprint notes"`))
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var entry channelartifacts.Entry
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &entry))
	assert.Equal(t, "Sprint notes", entry.Title)
	assert.Equal(t, 1, entry.Version)
	assert.Equal(t, "Helper", entry.SourceAgentDisplayName)
	assert.True(t, entry.Available)

	listed := f.do(http.MethodGet, "/channel-artifacts?channel_id="+f.channelID, "")
	require.Equal(t, http.StatusOK, listed.Code)
	var list channelartifacts.ListResponse
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &list))
	require.Len(t, list.Entries, 1)
	assert.True(t, list.CanAdd && list.CanEdit && list.CanDelete)
	assert.Equal(t, config.DefaultChannelArtifactsMaxPerChannel, list.MaxPerChannel)

	info := f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/info", "")
	require.Equal(t, http.StatusOK, info.Code)

	renamed := f.do(http.MethodPatch, "/channel-artifacts/"+entry.ID, `{"title":"Team notes"}`)
	require.Equal(t, http.StatusOK, renamed.Code)

	secondPost, secondFile := f.addSource(t, f.botID, "<html>v2</html>")
	replaced := f.do(http.MethodPost, "/channel-artifacts", f.createBody(secondPost, secondFile, `,"replace_id":"`+entry.ID+`"`))
	require.Equal(t, http.StatusOK, replaced.Code, replaced.Body.String())
	require.NoError(t, json.Unmarshal(replaced.Body.Bytes(), &entry))
	assert.Equal(t, 2, entry.Version)
	assert.Equal(t, "Team notes", entry.Title)

	versions := f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/versions", "")
	require.Equal(t, http.StatusOK, versions.Code)
	var versionList struct {
		Versions []channelartifacts.VersionRecord `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(versions.Body.Bytes(), &versionList))
	require.Len(t, versionList.Versions, 2)
	assert.True(t, versionList.Versions[0].Head)

	rolledBack := f.do(http.MethodPost, "/channel-artifacts/"+entry.ID+"/rollback", `{"version":1}`)
	require.Equal(t, http.StatusOK, rolledBack.Code)
	require.NoError(t, json.Unmarshal(rolledBack.Body.Bytes(), &entry))
	assert.Equal(t, 1, entry.Version)

	removedVersion := f.do(http.MethodDelete, "/channel-artifacts/"+entry.ID+"/versions/2", "")
	require.Equal(t, http.StatusOK, removedVersion.Code)
	lastVersion := f.do(http.MethodDelete, "/channel-artifacts/"+entry.ID+"/versions/1", "")
	assert.Equal(t, http.StatusBadRequest, lastVersion.Code, "the only version cannot be removed")

	deleted := f.do(http.MethodDelete, "/channel-artifacts/"+entry.ID, "")
	require.Equal(t, http.StatusNoContent, deleted.Code)
	assert.Equal(t, http.StatusNotFound, f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/info", "").Code)

	var files int
	require.NoError(t, f.db.Get(&files, `SELECT COUNT(*) FROM FileInfo WHERE Id IN ($1, $2)`, fileID, secondFile))
	assert.Equal(t, 2, files, "deleting a library entry never deletes the source files")
}

func TestCreateChannelArtifactGuards(t *testing.T) {
	tests := []struct {
		name       string
		disable    bool
		author     func(f *libraryFixture) string
		denyAdd    bool
		body       func(f *libraryFixture, postID, fileID string) string
		wantStatus int
		wantBody   string
	}{
		{name: "library off", disable: true, wantStatus: http.StatusNotFound, wantBody: "not found"},
		{name: "invalid ids", body: func(*libraryFixture, string, string) string {
			return `{"channel_id":"nope","post_id":"nope","file_id":"nope"}`
		}, wantStatus: http.StatusBadRequest, wantBody: "invalid request"},
		{name: "person's file", author: func(*libraryFixture) string { return testUserID }, wantStatus: http.StatusUnprocessableEntity},
		{name: "reader without the bookmark add permission", denyAdd: true, wantStatus: http.StatusForbidden, wantBody: "forbidden"},
		{name: "agent's file", wantStatus: http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLibraryFixture(t)
			f.e.config.enableChannelArtifacts = !tt.disable
			author := f.botID
			if tt.author != nil {
				author = tt.author(f)
			}
			if tt.denyAdd {
				f.denyPermission(model.PermissionAddBookmarkPublicChannel)
			}
			postID, fileID := f.addSource(t, author, "<html>x</html>")
			body := f.createBody(postID, fileID, "")
			if tt.body != nil {
				body = tt.body(f, postID, fileID)
			}

			rec := f.do(http.MethodPost, "/channel-artifacts", body)
			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			if tt.wantBody != "" {
				assert.Equal(t, tt.wantBody, rec.Body.String())
			}
			if tt.wantStatus != http.StatusCreated {
				list, err := f.svc.List(testUserID, f.channelID)
				require.NoError(t, err)
				assert.Empty(t, list.Entries)
			}
		})
	}

	t.Run("identical content names the existing entry", func(t *testing.T) {
		f := newLibraryFixture(t)
		first := f.save(t, "One", "<html>same</html>")
		postID, fileID := f.addSource(t, f.botID, "<html>same</html>")
		rec := f.do(http.MethodPost, "/channel-artifacts", f.createBody(postID, fileID, ""))
		require.Equal(t, http.StatusConflict, rec.Code)
		var body struct {
			ArtifactID string `json:"artifact_id"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, first.ID, body.ArtifactID)
	})
}

func TestChannelArtifactContentRoutes(t *testing.T) {
	iframe := []string{"Sec-Fetch-Dest", "iframe", "Sec-Fetch-Site", "same-origin"}

	tests := []struct {
		name       string
		setup      func(f *libraryFixture, entry *channelartifacts.Entry)
		method     string
		path       string
		headers    []string
		wantStatus int
		wantBody   string
	}{
		{name: "render", method: http.MethodGet, path: "?n=" + testArtifactNonce, headers: iframe, wantStatus: http.StatusOK, wantBody: "<html>v1</html>"},
		{name: "download", method: http.MethodGet, path: "/download", wantStatus: http.StatusOK, wantBody: "<html>v1</html>"},
		{
			name: "download denied by the file policy", method: http.MethodGet, path: "/download", wantStatus: http.StatusNotFound,
			setup: func(f *libraryFixture, _ *channelartifacts.Entry) { f.setFilePolicy(false) },
		},
		{
			name: "render denied by the file policy", method: http.MethodGet, path: "?n=" + testArtifactNonce, headers: iframe, wantStatus: http.StatusNotFound,
			setup: func(f *libraryFixture, _ *channelartifacts.Entry) { f.setFilePolicy(false) },
		},
		{
			name: "download after the source file was deleted", method: http.MethodGet, path: "/download", wantStatus: http.StatusGone,
			setup: func(f *libraryFixture, e *channelartifacts.Entry) { f.breakSource(t, e.SourceFileID) },
		},
		{
			name: "render after the source file was deleted", method: http.MethodGet, path: "?n=" + testArtifactNonce, headers: iframe, wantStatus: http.StatusGone,
			setup: func(f *libraryFixture, e *channelartifacts.Entry) { f.breakSource(t, e.SourceFileID) },
		},
		{
			name: "token after the source file was deleted", method: http.MethodGet, path: "/token?n=" + testArtifactNonce, wantStatus: http.StatusGone,
			setup: func(f *libraryFixture, e *channelartifacts.Entry) { f.breakSource(t, e.SourceFileID) },
		},
		{
			name: "token denied by the file policy", method: http.MethodGet, path: "/token?n=" + testArtifactNonce, wantStatus: http.StatusNotFound,
			setup: func(f *libraryFixture, _ *channelartifacts.Entry) { f.setFilePolicy(false) },
		},
		{name: "token for the current version", method: http.MethodGet, path: "/token?n=" + testArtifactNonce + "&version=1", wantStatus: http.StatusOK},
		{name: "token rejects unknown keys", method: http.MethodGet, path: "/token?n=" + testArtifactNonce + "&x=1", wantStatus: http.StatusBadRequest},
		{name: "download of a version that does not exist", method: http.MethodGet, path: "/download?version=9", wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLibraryFixture(t)
			f.e.api.artifactSecretCache = []byte(strings.Repeat("k", artifactSecretSize))
			entry := f.save(t, "Notes", "<html>v1</html>")
			if tt.setup != nil {
				tt.setup(f, entry)
			}
			rec := f.do(tt.method, "/channel-artifacts/"+entry.ID+tt.path, "", tt.headers...)
			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			if tt.wantBody != "" {
				assert.Contains(t, rec.Body.String(), tt.wantBody)
			}
		})
	}

	t.Run("expected client errors are not logged as server errors", func(t *testing.T) {
		f := newLibraryFixture(t)
		f.e.api.artifactSecretCache = []byte(strings.Repeat("k", artifactSecretSize))
		entry := f.save(t, "Notes", "<html>v1</html>")
		f.setFilePolicy(false)
		for _, path := range []string{"/download", "/token?n=" + testArtifactNonce, "?n=" + testArtifactNonce + "&version=9"} {
			rec := f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+path, "", "Sec-Fetch-Dest", "iframe", "Sec-Fetch-Site", "same-origin")
			require.Equal(t, http.StatusNotFound, rec.Code, path)
		}
		f.setFilePolicy(true)
		f.breakSource(t, entry.SourceFileID)
		rec := f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/download", "")
		require.Equal(t, http.StatusGone, rec.Code)

		for _, call := range f.e.mockAPI.Calls {
			assert.NotEqual(t, "LogError", call.Method, "%v", call.Arguments)
		}
	})

	t.Run("token matches the bridge token", func(t *testing.T) {
		f := newLibraryFixture(t)
		secret := []byte(strings.Repeat("k", artifactSecretSize))
		f.e.api.artifactSecretCache = secret
		entry := f.save(t, "Notes", "<html>v1</html>")
		rec := f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/token?n="+testArtifactNonce, "")
		require.Equal(t, http.StatusOK, rec.Code)
		var got struct {
			Token    string `json:"token"`
			TooLarge bool   `json:"tooLarge"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, libraryArtifactBridgeToken(secret, testUserID, entry.ID, testArtifactNonce), got.Token)
		assert.False(t, got.TooLarge)
	})

	t.Run("an unavailable entry is still listed as unavailable", func(t *testing.T) {
		f := newLibraryFixture(t)
		entry := f.save(t, "Notes", "<html>v1</html>")
		f.breakSource(t, entry.SourceFileID)
		rec := f.do(http.MethodGet, "/channel-artifacts?channel_id="+f.channelID, "")
		require.Equal(t, http.StatusOK, rec.Code)
		var list channelartifacts.ListResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
		require.Len(t, list.Entries, 1)
		assert.False(t, list.Entries[0].Available)
	})
}

func TestChannelArtifactWritesNeedTheMatchingBookmarkPermission(t *testing.T) {
	tests := []struct {
		name       string
		deny       *model.Permission
		method     string
		path       func(id string) string
		body       string
		wantStatus int
	}{
		{name: "rename needs edit", deny: model.PermissionEditBookmarkPublicChannel, method: http.MethodPatch, path: func(id string) string { return "/channel-artifacts/" + id }, body: `{"title":"x"}`, wantStatus: http.StatusForbidden},
		{name: "rollback needs edit", deny: model.PermissionEditBookmarkPublicChannel, method: http.MethodPost, path: func(id string) string { return "/channel-artifacts/" + id + "/rollback" }, body: `{"version":1}`, wantStatus: http.StatusForbidden},
		{name: "delete needs delete", deny: model.PermissionDeleteBookmarkPublicChannel, method: http.MethodDelete, path: func(id string) string { return "/channel-artifacts/" + id }, wantStatus: http.StatusForbidden},
		{name: "rename is allowed without delete", deny: model.PermissionDeleteBookmarkPublicChannel, method: http.MethodPatch, path: func(id string) string { return "/channel-artifacts/" + id }, body: `{"title":"x"}`, wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLibraryFixture(t)
			entry := f.save(t, "Notes", "<html>v1</html>")
			f.denyPermission(tt.deny)
			rec := f.do(tt.method, tt.path(entry.ID), tt.body)
			assert.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
		})
	}
}

func TestChannelArtifactRoutesRejectMalformedSelectors(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "malformed version escape", query: "/download?version=%zz"},
		{name: "repeated version", query: "/download?version=1&version=2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLibraryFixture(t)
			entry := f.save(t, "Notes", "<html>v1</html>")
			rec := f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+tt.query, "")
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestChannelArtifactMutationsRejectOversizedBodies(t *testing.T) {
	id := model.NewId()
	huge := `{"title":"` + strings.Repeat("a", MaxAgentRequestBodyBytes) + `"}`
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "create", method: http.MethodPost, path: "/channel-artifacts"},
		{name: "patch", method: http.MethodPatch, path: "/channel-artifacts/" + id},
		{name: "rollback", method: http.MethodPost, path: "/channel-artifacts/" + id + "/rollback"},
		{name: "mcp list", method: http.MethodPost, path: tools.ChannelArtifactsListPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLibraryFixture(t)
			assert.Equal(t, http.StatusRequestEntityTooLarge, f.do(tt.method, tt.path, huge).Code)
		})
	}
}

func TestChannelArtifactAuditRecords(t *testing.T) {
	const sentinel = "SENTINEL-CONTENT-DO-NOT-LOG"

	t.Run("create and replace name the objects", func(t *testing.T) {
		f := newLibraryFixture(t)
		postID, fileID := f.addSource(t, f.botID, "<html>"+sentinel+"</html>")
		records := f.e.CaptureAuditRecords()

		created := f.do(http.MethodPost, "/channel-artifacts", f.createBody(postID, fileID, `,"title":"`+sentinel+`"`))
		require.Equal(t, http.StatusCreated, created.Code)
		var entry channelartifacts.Entry
		require.NoError(t, json.Unmarshal(created.Body.Bytes(), &entry))

		secondPost, secondFile := f.addSource(t, f.botID, "<html>again</html>")
		replaced := f.do(http.MethodPost, "/channel-artifacts", f.createBody(secondPost, secondFile, `,"replace_id":"`+entry.ID+`"`))
		require.Equal(t, http.StatusOK, replaced.Code)

		require.Len(t, *records, 2)
		assert.Equal(t, AuditEventCreateChannelArtifact, (*records)[0].EventName)
		assert.Equal(t, AuditEventReplaceChannelArtifact, (*records)[1].EventName)
		for _, rec := range *records {
			assert.Equal(t, entry.ID, rec.EventData.Parameters[audit.KeyChannelArtifactID])
			assert.Equal(t, f.channelID, rec.EventData.Parameters[audit.KeyChannelID])
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), sentinel, "audit records carry IDs only")
		}
	})

	t.Run("other writes", func(t *testing.T) {
		tests := []struct {
			name      string
			method    string
			path      func(id string) string
			body      string
			wantEvent string
			wantVer   string
		}{
			{name: "patch", method: http.MethodPatch, path: func(id string) string { return "/channel-artifacts/" + id }, body: `{"title":"x"}`, wantEvent: AuditEventUpdateChannelArtifact},
			{name: "rollback", method: http.MethodPost, path: func(id string) string { return "/channel-artifacts/" + id + "/rollback" }, body: `{"version":1}`, wantEvent: AuditEventRollbackChannelArtifact, wantVer: "1"},
			{name: "delete version", method: http.MethodDelete, path: func(id string) string { return "/channel-artifacts/" + id + "/versions/2" }, wantEvent: AuditEventDeleteChannelArtifactVersion, wantVer: "2"},
			{name: "delete", method: http.MethodDelete, path: func(id string) string { return "/channel-artifacts/" + id }, wantEvent: AuditEventDeleteChannelArtifact},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := newLibraryFixture(t)
				entry := f.save(t, "Notes", "<html>v1</html>")
				secondPost, secondFile := f.addSource(t, f.botID, "<html>v2</html>")
				_, err := f.svc.Save(testUserID, f.mm, channelartifacts.SaveRequest{
					ChannelID: f.channelID, PostID: secondPost, FileID: secondFile, ArtifactID: entry.ID,
				}, channelartifacts.Limits{MaxSize: 1 << 20})
				require.NoError(t, err)

				records := f.e.CaptureAuditRecords()
				rec := f.do(tt.method, tt.path(entry.ID), tt.body)
				require.Less(t, rec.Code, 300, rec.Body.String())
				require.Len(t, *records, 1)
				assert.Equal(t, tt.wantEvent, (*records)[0].EventName)
				assert.Equal(t, entry.ID, (*records)[0].EventData.Parameters[audit.KeyChannelArtifactID])
				if tt.wantVer != "" {
					assert.Equal(t, tt.wantVer, (*records)[0].EventData.Parameters[audit.KeyChannelArtifactVersion])
				}
			})
		}
	})
}

func TestMCPChannelArtifactCallbacks(t *testing.T) {
	f := newLibraryFixture(t)
	entry := f.save(t, "Sprint notes", "<html>0123456789</html>")

	listed := f.do(http.MethodPost, tools.ChannelArtifactsListPath, `{"channel_id":"`+f.channelID+`"}`)
	require.Equal(t, http.StatusOK, listed.Code)
	var list tools.ChannelArtifactListResponse
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &list))
	require.Len(t, list.Entries, 1)
	assert.Equal(t, entry.ID, list.Entries[0].ID)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		check      func(t *testing.T, got tools.ChannelArtifactGetResponse)
	}{
		{
			name: "first window", body: `{"artifact_id":"` + entry.ID + `","content_limit":6}`, wantStatus: http.StatusOK,
			check: func(t *testing.T, got tools.ChannelArtifactGetResponse) {
				assert.Equal(t, "<html>", got.Content)
				assert.True(t, got.HasMore)
				assert.Equal(t, 6, got.NextOffset)
			},
		},
		{
			name: "continues at the offset", body: `{"artifact_id":"` + entry.ID + `","offset":6,"content_limit":10}`, wantStatus: http.StatusOK,
			check: func(t *testing.T, got tools.ChannelArtifactGetResponse) { assert.Equal(t, "0123456789", got.Content) },
		},
		{
			name: "metadata only", body: `{"artifact_id":"` + entry.ID + `","include_content":false}`, wantStatus: http.StatusOK,
			check: func(t *testing.T, got tools.ChannelArtifactGetResponse) {
				assert.False(t, got.ContentIncluded)
				assert.Equal(t, entry.ID, got.Entry.ID)
			},
		},
		{name: "invalid id", body: `{"artifact_id":"x"}`, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.do(http.MethodPost, tools.ChannelArtifactsGetPath, tt.body)
			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			if tt.check != nil {
				var got tools.ChannelArtifactGetResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
				tt.check(t, got)
			}
		})
	}

	t.Run("policy denial and deleted sources fail closed", func(t *testing.T) {
		f.setFilePolicy(false)
		denied := f.do(http.MethodPost, tools.ChannelArtifactsGetPath, `{"artifact_id":"`+entry.ID+`"}`)
		assert.Equal(t, http.StatusNotFound, denied.Code)
		f.setFilePolicy(true)
		f.breakSource(t, entry.SourceFileID)
		gone := f.do(http.MethodPost, tools.ChannelArtifactsGetPath, `{"artifact_id":"`+entry.ID+`"}`)
		assert.Equal(t, http.StatusGone, gone.Code)
	})

	t.Run("an earlier version comes back with its own metadata", func(t *testing.T) {
		versioned := f.save(t, "Versioned", "<html>0123456789 first</html>")
		secondPost, secondFile := f.addSource(t, f.botID, "<html>second</html>")
		_, err := f.svc.Save(testUserID, f.mm, channelartifacts.SaveRequest{
			ChannelID: f.channelID, PostID: secondPost, FileID: secondFile, ArtifactID: versioned.ID,
		}, channelartifacts.Limits{MaxSize: 1 << 20})
		require.NoError(t, err)

		rec := f.do(http.MethodPost, tools.ChannelArtifactsGetPath, `{"artifact_id":"`+versioned.ID+`","version":1}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var got tools.ChannelArtifactGetResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, "<html>0123456789 first</html>", got.Content)
		assert.Equal(t, 1, got.Entry.Version)
		assert.Equal(t, versioned.SHA256, got.Entry.SHA256)
		assert.Equal(t, versioned.SourceFileID, got.Entry.SourceFileID)
		assert.Equal(t, "Helper", got.Entry.SourceAgentDisplayName)

		metaOnly := f.do(http.MethodPost, tools.ChannelArtifactsGetPath, `{"artifact_id":"`+versioned.ID+`","version":1,"include_content":false}`)
		require.Equal(t, http.StatusOK, metaOnly.Code)
		var meta tools.ChannelArtifactGetResponse
		require.NoError(t, json.Unmarshal(metaOnly.Body.Bytes(), &meta))
		assert.Equal(t, 1, meta.Entry.Version)
		assert.Equal(t, versioned.SHA256, meta.Entry.SHA256)
		assert.False(t, meta.ContentIncluded)

		missing := f.do(http.MethodPost, tools.ChannelArtifactsGetPath, `{"artifact_id":"`+versioned.ID+`","version":9,"include_content":false}`)
		assert.Equal(t, http.StatusNotFound, missing.Code)

		current := f.do(http.MethodPost, tools.ChannelArtifactsGetPath, `{"artifact_id":"`+versioned.ID+`"}`)
		require.NoError(t, json.Unmarshal(current.Body.Bytes(), &got))
		assert.Equal(t, 2, got.Entry.Version)
		assert.Equal(t, secondFile, got.Entry.SourceFileID)
	})

	t.Run("library off", func(t *testing.T) {
		f.e.config.enableChannelArtifacts = false
		assert.Equal(t, http.StatusServiceUnavailable, f.do(http.MethodPost, tools.ChannelArtifactsListPath, `{"channel_id":"`+f.channelID+`"}`).Code)
		assert.Equal(t, http.StatusServiceUnavailable, f.do(http.MethodPost, tools.ChannelArtifactsGetPath, `{"artifact_id":"`+entry.ID+`"}`).Code)
	})
}

func TestMCPChannelArtifactsServeToolsInProcess(t *testing.T) {
	f := newLibraryFixture(t)
	entry := f.save(t, "Sprint notes", "<html>0123456789</html>")
	ctx := t.Context()
	svc := NewMCPChannelArtifacts(f.svc, f.mm, f.e.config)

	entries, err := svc.List(ctx, testUserID, f.channelID)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	_, err = svc.Get(ctx, testUserID, entry.ID, tools.ChannelArtifactReadOptions{IncludeContent: true})
	require.ErrorIs(t, err, channelartifacts.ErrNotFound, "no session means the file policy denies, so the read fails closed")

	withSession := context.WithValue(ctx, auth.SessionIDContextKey, artifactSession)
	got, err := svc.Get(withSession, testUserID, entry.ID, tools.ChannelArtifactReadOptions{IncludeContent: true, Offset: 6, Limit: 4})
	require.NoError(t, err)
	assert.Equal(t, "0123", got.Content)
	assert.True(t, got.HasMore)

	f.e.config.enableChannelArtifacts = false
	assert.False(t, svc.Enabled())
	_, err = svc.List(ctx, testUserID, f.channelID)
	require.ErrorIs(t, err, errChannelArtifactsUnavailable)
}

func TestChannelArtifactRoutesRefuseDirectAndGroupMessages(t *testing.T) {
	for _, channelType := range []model.ChannelType{model.ChannelTypeDirect, model.ChannelTypeGroup} {
		t.Run(string(channelType), func(t *testing.T) {
			f := newLibraryFixture(t)
			postID, fileID := f.addSource(t, f.botID, "<html>x</html>")
			f.setChannelType(channelType)

			tests := []struct {
				name   string
				method string
				path   string
				body   string
			}{
				{name: "list", method: http.MethodGet, path: "/channel-artifacts?channel_id=" + f.channelID},
				{name: "save", method: http.MethodPost, path: "/channel-artifacts", body: f.createBody(postID, fileID, "")},
				{name: "mcp list", method: http.MethodPost, path: tools.ChannelArtifactsListPath, body: `{"channel_id":"` + f.channelID + `"}`},
			}
			for _, tt := range tests {
				rec := f.do(tt.method, tt.path, tt.body)
				assert.Equal(t, http.StatusNotFound, rec.Code, "%s: %s", tt.name, rec.Body.String())
			}

			var rows int
			require.NoError(t, f.db.Get(&rows, `SELECT COUNT(*) FROM Agents_ChannelArtifacts WHERE ChannelID = $1`, f.channelID))
			assert.Zero(t, rows)
		})
	}
}
