// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
)

const testMarkdown = "# Charter\n\n- ship it\n"

func TestMarkdownChannelArtifactsThroughTheRoutes(t *testing.T) {
	f := newLibraryFixture(t)
	f.e.api.artifactSecretCache = []byte(strings.Repeat("k", artifactSecretSize))
	iframe := []string{"Sec-Fetch-Dest", "iframe", "Sec-Fetch-Site", "same-origin"}

	postID, fileID := f.addFile(t, f.botID, "charter.md", testMarkdown)
	created := f.do(http.MethodPost, "/channel-artifacts", fmt.Sprintf(`{"channel_id":%q,"file_id":%q,"title":"Charter"}`, f.channelID, fileID))
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var entry channelartifacts.Entry
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &entry))
	assert.Equal(t, channelartifacts.FormatMarkdown, entry.Format, "the format comes from the file when the request leaves it out")
	assert.Equal(t, postID, entry.SourcePostID, "the post comes from the file when the request leaves it out")

	t.Run("the content route serves the source as plain text", func(t *testing.T) {
		rec := f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/content", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, testMarkdown, rec.Body.String())
		assert.Equal(t, "text/markdown; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
		assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "sandbox")
	})

	t.Run("the content route follows the file policy and the source", func(t *testing.T) {
		f.setFilePolicy(false)
		assert.Equal(t, http.StatusNotFound, f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/content", "").Code)
		f.setFilePolicy(true)
		f.breakSource(t, entry.SourceFileID)
		assert.Equal(t, http.StatusGone, f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/content", "").Code)
	})

	t.Run("the HTML frame routes refuse it", func(t *testing.T) {
		assert.Equal(t, http.StatusUnsupportedMediaType, f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"?n="+testArtifactNonce, "", iframe...).Code)
		assert.Equal(t, http.StatusUnsupportedMediaType, f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/token?n="+testArtifactNonce, "").Code)
	})
}

func TestMarkdownDownloadAndFormatRules(t *testing.T) {
	f := newLibraryFixture(t)
	postID, fileID := f.addFile(t, f.botID, "notes.markdown", testMarkdown)
	created := f.do(http.MethodPost, "/channel-artifacts", f.createBody(postID, fileID, `,"title":"Notes","format":"markdown"`))
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var entry channelartifacts.Entry
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &entry))

	download := f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/download", "")
	require.Equal(t, http.StatusOK, download.Code)
	assert.Equal(t, "text/markdown; charset=utf-8", download.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="Notes.md"`, download.Header().Get("Content-Disposition"))

	htmlPost, htmlFile := f.addSource(t, f.botID, "<html>other</html>")
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "a file of another format than the artifact", body: f.createBody(htmlPost, htmlFile, `,"replace_id":"`+entry.ID+`"`), wantStatus: http.StatusUnprocessableEntity},
		{name: "a stated format that is not the artifact's", body: f.createBody(htmlPost, htmlFile, `,"replace_id":"`+entry.ID+`","format":"html"`), wantStatus: http.StatusBadRequest},
		{name: "an unknown format", body: f.createBody(htmlPost, htmlFile, `,"format":"pdf"`), wantStatus: http.StatusBadRequest},
		{name: "a file that does not match the stated format", body: f.createBody(htmlPost, htmlFile, `,"format":"markdown","title":"x"`), wantStatus: http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.do(http.MethodPost, "/channel-artifacts", tt.body)
			assert.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
		})
	}
}

func TestHTMLArtifactsRefuseTheMarkdownContentRoute(t *testing.T) {
	f := newLibraryFixture(t)
	entry := f.save(t, "Dashboard", "<html>v1</html>")
	assert.Equal(t, channelartifacts.FormatHTML, entry.Format)
	assert.Equal(t, http.StatusUnsupportedMediaType, f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/content", "").Code)
}

func TestMarkdownContentRouteIsNotAudited(t *testing.T) {
	f := newLibraryFixture(t)
	postID, fileID := f.addFile(t, f.botID, "charter.md", testMarkdown)
	entry, err := f.svc.Save(testUserID, f.mm, channelartifacts.SaveRequest{ChannelID: f.channelID, PostID: postID, FileID: fileID, Title: "Charter"}, channelartifacts.Limits{MaxSize: 1 << 20})
	require.NoError(t, err)

	records := f.e.CaptureAuditRecords()
	require.Equal(t, http.StatusOK, f.do(http.MethodGet, "/channel-artifacts/"+entry.ID+"/content", "").Code)
	assert.Empty(t, *records, "reads are not audited")
}

func TestMarkdownCreateAuditRecordCarriesNoContent(t *testing.T) {
	f := newLibraryFixture(t)
	const sentinel = "SENTINEL-MARKDOWN-DO-NOT-LOG"
	postID, fileID := f.addFile(t, f.botID, "charter.md", "# "+sentinel)
	records := f.e.CaptureAuditRecords()

	created := f.do(http.MethodPost, "/channel-artifacts", f.createBody(postID, fileID, `,"title":"Charter","format":"markdown"`))
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var entry channelartifacts.Entry
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &entry))

	require.Len(t, *records, 1)
	assert.Equal(t, AuditEventCreateChannelArtifact, (*records)[0].EventName)
	assert.Equal(t, entry.ID, (*records)[0].EventData.Parameters[audit.KeyChannelArtifactID])
	raw, err := json.Marshal((*records)[0])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), sentinel)
}
