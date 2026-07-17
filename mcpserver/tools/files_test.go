// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeFileContentService struct {
	content files.Content
	err     error

	gotUserID string
	gotFileID string
	gotOffset int
	gotLimit  int
}

func (f *fakeFileContentService) GetContent(_ context.Context, userID, fileID string, offset, limit int) (files.Content, error) {
	f.gotUserID = userID
	f.gotFileID = fileID
	f.gotOffset = offset
	f.gotLimit = limit
	return f.content, f.err
}

// newTestFileServer stubs the file-info and file-download endpoints toolReadFile
// touches. fileData is returned by the download endpoint.
func newTestFileServer(t *testing.T, info *model.FileInfo, fileData []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/files/"+info.Id+"/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})
	mux.HandleFunc("/api/v4/files/"+info.Id, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fileData)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// contentText concatenates the text of all TextContent blocks.
func contentText(contents []mcp.Content) string {
	var sb strings.Builder
	for _, c := range contents {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// firstImage returns the first ImageContent block, or nil.
func firstImage(contents []mcp.Content) *mcp.ImageContent {
	for _, c := range contents {
		if ic, ok := c.(*mcp.ImageContent); ok {
			return ic
		}
	}
	return nil
}

func TestToolReadFile(t *testing.T) {
	fileID := model.NewId()

	tests := []struct {
		name            string
		fileID          string
		info            *model.FileInfo
		fileData        []byte
		service         FileContentService
		wantErr         bool
		wantErrContains string
		wantText        []string // substring matches against concatenated text content
		wantImage       bool
	}{
		{
			name:            "invalid file id",
			fileID:          "too-short",
			info:            &model.FileInfo{Id: fileID},
			service:         &fakeFileContentService{},
			wantErr:         true,
			wantErrContains: "file_id must be a valid ID",
		},
		{
			name:     "image is returned as inline image content",
			fileID:   fileID,
			info:     &model.FileInfo{Id: fileID, Name: "photo.png", MimeType: "image/png", Size: 4, Width: 2, Height: 2},
			fileData: []byte{0x89, 0x50, 0x4E, 0x47},
			service:  &fakeFileContentService{},
			wantText: []string{"Image: photo.png (image/png"},

			wantImage: true,
		},
		{
			name:      "oversized image returns metadata only",
			fileID:    fileID,
			info:      &model.FileInfo{Id: fileID, Name: "huge.png", MimeType: "image/png", Size: maxInlineImageBytes + 1},
			service:   &fakeFileContentService{},
			wantText:  []string{"larger than", "get_file_link"},
			wantImage: false,
		},
		{
			name:            "forbidden from the content service is a tool error",
			fileID:          fileID,
			info:            &model.FileInfo{Id: fileID, Name: "report.pdf", MimeType: "application/pdf"},
			service:         &fakeFileContentService{err: files.ErrForbidden},
			wantErr:         true,
			wantErrContains: "permission",
		},
		{
			name:            "service failure for a document is not masked by the fallback",
			fileID:          fileID,
			info:            &model.FileInfo{Id: fileID, Name: "report.pdf", MimeType: "application/pdf"},
			service:         &fakeFileContentService{err: assert.AnError},
			wantErr:         true,
			wantErrContains: "error reading file",
		},
		{
			name:            "oversized text file is rejected by the fallback",
			fileID:          fileID,
			info:            &model.FileInfo{Id: fileID, Name: "big.txt", MimeType: "text/plain", Size: maxStandaloneTextBytes + 1},
			service:         nil,
			wantErr:         true,
			wantErrContains: "larger than",
		},
		{
			name:   "document text comes from the content service",
			fileID: fileID,
			info:   &model.FileInfo{Id: fileID, Name: "report.pdf", MimeType: "application/pdf"},
			service: &fakeFileContentService{content: files.Content{
				Name: "report.pdf", MimeType: "application/pdf",
				TotalRunes: 100, Offset: 0, Returned: 6, HasMore: true, HasText: true,
				Text: "abcdef",
			}},
			wantText: []string{
				"File: report.pdf (application/pdf)",
				"Showing characters 0-6 of 100. More content remains; call read_file again with offset=6 to continue.",
				"abcdef",
			},
		},
		{
			name:     "nil service falls back to direct read for text files",
			fileID:   fileID,
			info:     &model.FileInfo{Id: fileID, Name: "notes.txt", MimeType: "text/plain"},
			fileData: []byte("plain text body"),
			service:  nil,
			wantText: []string{"File: notes.txt (text/plain)", "plain text body"},
		},
		{
			name:     "failing service falls back to direct read for text files",
			fileID:   fileID,
			info:     &model.FileInfo{Id: fileID, Name: "notes.txt", MimeType: "text/plain"},
			fileData: []byte("plain text body"),
			service:  &fakeFileContentService{err: assert.AnError},
			wantText: []string{"File: notes.txt (text/plain)", "plain text body"},
		},
		{
			name:     "nil service with a binary file reports no extractable text",
			fileID:   fileID,
			info:     &model.FileInfo{Id: fileID, Name: "archive.zip", MimeType: "application/zip"},
			service:  nil,
			wantText: []string{`File "archive.zip" (application/zip) has no extractable text content`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newTestFileServer(t, tt.info, tt.fileData)
			p := &MattermostToolProvider{fileContentService: tt.service, logger: &testLogger{t: t}}
			ctx := &MCPToolContext{Ctx: context.Background(), UserID: model.NewId(), Client: newTestClient(server.URL)}

			contents, err := p.toolReadFile(ctx, ReadFileArgs{FileID: tt.fileID})

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
				return
			}
			require.NoError(t, err)

			text := contentText(contents)
			for _, sub := range tt.wantText {
				assert.Contains(t, text, sub)
			}

			image := firstImage(contents)
			if tt.wantImage {
				require.NotNil(t, image, "expected an ImageContent block")
				assert.Equal(t, tt.info.MimeType, image.MIMEType)
				assert.Equal(t, tt.fileData, image.Data)
			} else {
				assert.Nil(t, image, "expected no ImageContent block")
			}
		})
	}
}

// TestToolReadFilePassesRequestingUser pins the permission-relevant contract:
// the read flows the authenticated user's ID and the requested range through to
// the content service, which is what enforces channel access.
func TestToolReadFilePassesRequestingUser(t *testing.T) {
	fileID := model.NewId()
	server := newTestFileServer(t, &model.FileInfo{Id: fileID, Name: "a.pdf", MimeType: "application/pdf"}, nil)

	fake := &fakeFileContentService{content: files.Content{Name: "a.pdf", HasText: true, Text: "hi"}}
	p := &MattermostToolProvider{fileContentService: fake, logger: &testLogger{t: t}}

	userID := model.NewId()
	ctx := &MCPToolContext{Ctx: context.Background(), UserID: userID, Client: newTestClient(server.URL)}

	_, err := p.toolReadFile(ctx, ReadFileArgs{FileID: fileID, Offset: 12, Limit: 34})
	require.NoError(t, err)

	assert.Equal(t, userID, fake.gotUserID)
	assert.Equal(t, fileID, fake.gotFileID)
	assert.Equal(t, 12, fake.gotOffset)
	assert.Equal(t, 34, fake.gotLimit)
}
