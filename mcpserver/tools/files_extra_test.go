// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileExtraToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), AccessMode: AccessModeRemote}
	// Local access mode: upload_file passes the access-mode gate, so downstream
	// validation (e.g. empty path) is exercised.
	localCtx := &MCPToolContext{Client: client, Ctx: t.Context(), AccessMode: AccessModeLocal}

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
			return provider.toolUploadFile(localCtx, UploadFileArgs{ChannelID: model.NewId(), Path: ""})
		}, "path cannot be empty"},
		{"copy_file_to_channel bad file_id", func() (string, error) {
			return provider.toolCopyFileToChannel(mcpCtx, CopyFileToChannelArgs{FileID: "bad", TargetChannelID: model.NewId()})
		}, "must be a valid ID"},
		{"copy_file_to_channel bad target_channel_id", func() (string, error) {
			return provider.toolCopyFileToChannel(mcpCtx, CopyFileToChannelArgs{FileID: model.NewId(), TargetChannelID: "bad"})
		}, "must be a valid ID"},
		{"copy_file_to_channel path traversal filename", func() (string, error) {
			return provider.toolCopyFileToChannel(mcpCtx, CopyFileToChannelArgs{FileID: model.NewId(), TargetChannelID: model.NewId(), Filename: "../secret.png"})
		}, "invalid filename"},
		{"copy_file_to_channel backslash filename", func() (string, error) {
			return provider.toolCopyFileToChannel(mcpCtx, CopyFileToChannelArgs{FileID: model.NewId(), TargetChannelID: model.NewId(), Filename: `a\b.png`})
		}, "invalid filename"},
		{"copy_file_to_channel dot filename", func() (string, error) {
			return provider.toolCopyFileToChannel(mcpCtx, CopyFileToChannelArgs{FileID: model.NewId(), TargetChannelID: model.NewId(), Filename: ".."})
		}, "invalid filename"},
		{"copy_file_to_channel overlong filename", func() (string, error) {
			return provider.toolCopyFileToChannel(mcpCtx, CopyFileToChannelArgs{FileID: model.NewId(), TargetChannelID: model.NewId(), Filename: strings.Repeat("a", 256) + ".png"})
		}, "at most 255 characters"},
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

func TestToolCopyFileToChannel(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01}

	tests := []struct {
		name             string
		filenameOverride string
		sourceInfo       *model.FileInfo
		infoStatus       int
		fileStatus       int
		fileBody         []byte
		uploadStatus     int
		uploadEmptyInfos bool
		wantErrContains  string
		wantUploadedName string
		wantNoDownload   bool
		wantNoUpload     bool
	}{
		{
			name:             "copies binary image preserving exact bytes and source filename",
			sourceInfo:       &model.FileInfo{Name: "bug-shot.png", MimeType: "image/png", Size: int64(len(pngBytes))},
			fileBody:         pngBytes,
			wantUploadedName: "bug-shot.png",
		},
		{
			name:             "copies zero-byte file via multipart upload",
			sourceInfo:       &model.FileInfo{Name: "empty.txt", MimeType: "text/plain", Size: 0},
			fileBody:         []byte{},
			wantUploadedName: "empty.txt",
		},
		{
			name:             "uses optional filename override",
			filenameOverride: "renamed.png",
			sourceInfo:       &model.FileInfo{Name: "bug-shot.png", MimeType: "image/png", Size: int64(len(pngBytes))},
			fileBody:         pngBytes,
			wantUploadedName: "renamed.png",
		},
		{
			name:             "falls back to attachment when source filename is empty",
			sourceInfo:       &model.FileInfo{Name: "", MimeType: "image/png", Size: int64(len(pngBytes))},
			fileBody:         pngBytes,
			wantUploadedName: "attachment",
		},
		{
			name:            "propagates forbidden on source file info without downloading or uploading",
			infoStatus:      http.StatusForbidden,
			wantErrContains: "error fetching source file info",
			wantNoDownload:  true,
			wantNoUpload:    true,
		},
		{
			name:            "rejects deleted source file without downloading or uploading",
			sourceInfo:      &model.FileInfo{Name: "gone.png", DeleteAt: 12345, Size: 10},
			wantErrContains: "was not found",
			wantNoDownload:  true,
			wantNoUpload:    true,
		},
		{
			name:            "rejects oversized source file before downloading",
			sourceInfo:      &model.FileInfo{Name: "huge.bin", Size: maxMCPFetchBytes + 1},
			wantErrContains: "too large",
			wantNoDownload:  true,
			wantNoUpload:    true,
		},
		{
			name:            "propagates forbidden on source file download without uploading",
			sourceInfo:      &model.FileInfo{Name: "secret.png", MimeType: "image/png", Size: 10},
			fileStatus:      http.StatusForbidden,
			wantErrContains: "error downloading source file",
			wantNoUpload:    true,
		},
		{
			name:            "propagates upload failure in target channel",
			sourceInfo:      &model.FileInfo{Name: "shot.png", MimeType: "image/png", Size: int64(len(pngBytes))},
			fileBody:        pngBytes,
			uploadStatus:    http.StatusForbidden,
			wantErrContains: "error uploading copied file",
		},
		{
			name:             "fails when upload returns no file info",
			sourceInfo:       &model.FileInfo{Name: "shot.png", MimeType: "image/png", Size: int64(len(pngBytes))},
			fileBody:         pngBytes,
			uploadEmptyInfos: true,
			wantErrContains:  "upload produced no file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceFileID := model.NewId()
			targetChannelID := model.NewId()
			copiedFileID := model.NewId()

			var downloaded bool
			var uploaded bool
			var uploadedChannel string
			var uploadedFilename string
			var uploadedBody []byte

			mux := http.NewServeMux()
			mux.HandleFunc(fmt.Sprintf("/api/v4/files/%s/info", sourceFileID), func(w http.ResponseWriter, _ *http.Request) {
				if tt.infoStatus != 0 {
					w.WriteHeader(tt.infoStatus)
					_ = json.NewEncoder(w).Encode(&model.AppError{Message: "forbidden", StatusCode: tt.infoStatus})
					return
				}
				info := *tt.sourceInfo
				info.Id = sourceFileID
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&info)
			})
			mux.HandleFunc(fmt.Sprintf("/api/v4/files/%s", sourceFileID), func(w http.ResponseWriter, _ *http.Request) {
				downloaded = true
				if tt.fileStatus != 0 {
					w.WriteHeader(tt.fileStatus)
					_ = json.NewEncoder(w).Encode(&model.AppError{Message: "forbidden", StatusCode: tt.fileStatus})
					return
				}
				_, _ = w.Write(tt.fileBody)
			})
			mux.HandleFunc("/api/v4/files", func(w http.ResponseWriter, r *http.Request) {
				uploaded = true
				if r.ContentLength == 0 {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(&model.AppError{Message: "Content-Length should not be 0", StatusCode: http.StatusBadRequest})
					return
				}
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
					if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					uploadedChannel = r.FormValue("channel_id")
					file, header, formErr := r.FormFile("files")
					if !assert.NoError(t, formErr) {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					defer file.Close()
					uploadedFilename = header.Filename
					uploadedBody, _ = io.ReadAll(file)
				} else {
					uploadedChannel = r.URL.Query().Get("channel_id")
					uploadedFilename = r.URL.Query().Get("filename")
					uploadedBody, _ = io.ReadAll(r.Body)
				}

				if tt.uploadStatus != 0 {
					w.WriteHeader(tt.uploadStatus)
					_ = json.NewEncoder(w).Encode(&model.AppError{Message: "upload denied", StatusCode: tt.uploadStatus})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if tt.uploadEmptyInfos {
					_ = json.NewEncoder(w).Encode(&model.FileUploadResponse{})
					return
				}
				_ = json.NewEncoder(w).Encode(&model.FileUploadResponse{
					FileInfos: []*model.FileInfo{{
						Id:        copiedFileID,
						ChannelId: targetChannelID,
						Name:      uploadedFilename,
						MimeType:  "image/png",
						Size:      int64(len(uploadedBody)),
					}},
				})
			})

			ts := httptest.NewServer(mux)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), AccessMode: AccessModeRemote}

			out, err := provider.toolCopyFileToChannel(mcpCtx, CopyFileToChannelArgs{
				FileID:          sourceFileID,
				TargetChannelID: targetChannelID,
				Filename:        tt.filenameOverride,
			})

			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				if tt.wantNoDownload {
					assert.False(t, downloaded, "source file must not be downloaded")
				}
				if tt.wantNoUpload {
					assert.False(t, uploaded, "target upload must not run")
				}
				return
			}

			require.NoError(t, err)
			assert.True(t, downloaded)
			assert.True(t, uploaded)
			assert.Equal(t, targetChannelID, uploadedChannel)
			assert.Equal(t, tt.wantUploadedName, uploadedFilename)
			assert.Equal(t, tt.fileBody, uploadedBody)
			assert.Contains(t, out, targetChannelID)
			assert.Contains(t, out, copiedFileID)
			assert.Contains(t, out, tt.wantUploadedName)
		})
	}
}
