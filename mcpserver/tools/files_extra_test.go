// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileExtraToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	// Remote access mode to verify upload_file gating.
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), AccessMode: AccessModeRemote}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_file_info bad", func() (string, error) { return provider.toolGetFileInfo(mcpCtx, GetFileInfoArgs{FileID: "bad"}) }, "must be a valid ID"},
		{"get_post_files bad", func() (string, error) { return provider.toolGetPostFiles(mcpCtx, GetPostFilesArgs{PostID: "bad"}) }, "must be a valid ID"},
		{"get_file_link bad", func() (string, error) { return provider.toolGetFileLink(mcpCtx, GetFileLinkArgs{FileID: "bad"}) }, "must be a valid ID"},
		{"search_files empty", func() (string, error) {
			return provider.toolSearchFiles(mcpCtx, SearchFilesArgs{Terms: "", TeamID: model.NewId()})
		}, "terms cannot be empty"},
		{"upload_file empty path", func() (string, error) {
			return provider.toolUploadFile(mcpCtx, UploadFileArgs{ChannelID: model.NewId(), Path: ""})
		}, "path cannot be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestToolUploadFileRemoteGating(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), AccessMode: AccessModeRemote}

	// In remote mode, upload returns a graceful (non-error) message.
	out, err := provider.toolUploadFile(mcpCtx, UploadFileArgs{ChannelID: model.NewId(), Path: "report.pdf"})
	require.NoError(t, err)
	assert.Contains(t, out, "local access mode")
}

func TestToolGetFileInfo(t *testing.T) {
	fileID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/files/%s/info", fileID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.FileInfo{Id: fileID, Name: "report.pdf", MimeType: "application/pdf", Size: 1024})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetFileInfo(mcpCtx, GetFileInfoArgs{FileID: fileID})
	require.NoError(t, err)
	assert.Contains(t, out, "report.pdf")
	assert.Contains(t, out, fileID)
}

func TestToolGetFileLink(t *testing.T) {
	fileID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/files/%s/link", fileID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"link": "https://mm.example.com/files/public/abc"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetFileLink(mcpCtx, GetFileLinkArgs{FileID: fileID})
	require.NoError(t, err)
	assert.Contains(t, out, "https://mm.example.com/files/public/abc")
}
