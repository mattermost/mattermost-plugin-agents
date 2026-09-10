// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchFileDataForLocal_InvalidURLSpecs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
	}))
	t.Cleanup(server.Close)

	testCases := []struct {
		name     string
		filespec string
	}{
		{
			name:     "URL fetch failure returns file upload failed",
			filespec: server.URL,
		},
		{
			name:     "empty host is handled like other URL errors",
			filespec: "https:///path",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fetchFileDataForLocal(t.Context(), tc.filespec, AccessModeLocal)
			require.Error(t, err)
			require.ErrorIs(t, err, errMCPFileUploadFailed)
			// No raw transport or config detail in the returned value (logs hold the full error)
			low := err.Error()
			require.Equal(t, errMCPFileUploadFailed.Error(), low)
		})
	}
}

// inlineUploadRecorder records what the fake /api/v4/files endpoint received.
type inlineUploadRecorder struct {
	filenames []string
	contents  []string
	fileIDs   []string
}

// newInlineFileUploadServer stubs POST /api/v4/files, recording uploaded filenames and
// contents and returning a fresh file ID per upload. Uploads named failFilename fail
// with HTTP 500.
func newInlineFileUploadServer(t *testing.T, failFilename string) (*httptest.Server, *inlineUploadRecorder) {
	t.Helper()
	rec := &inlineUploadRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/files", func(w http.ResponseWriter, r *http.Request) {
		filename := r.URL.Query().Get("filename")
		if failFilename != "" && filename == failFilename {
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		id := model.NewId()
		rec.filenames = append(rec.filenames, filename)
		rec.contents = append(rec.contents, string(body))
		rec.fileIDs = append(rec.fileIDs, id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.FileUploadResponse{FileInfos: []*model.FileInfo{{Id: id}}})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, rec
}

// makeInlineFiles builds n distinct valid inline files.
func makeInlineFiles(n int) []InlineFile {
	files := make([]InlineFile, n)
	for i := range files {
		files[i] = InlineFile{Name: fmt.Sprintf("file-%d.txt", i), Content: "content"}
	}
	return files
}

func TestUploadInlineFiles(t *testing.T) {
	tests := []struct {
		name            string
		files           []InlineFile
		failFilename    string
		wantErrContains []string // non-empty means an error containing every entry
		wantNoUploads   bool     // error cases: assert nothing reached the upload endpoint
		wantFilenames   []string
		wantContents    []string
	}{
		{
			name:  "nil input uploads nothing",
			files: nil,
		},
		{
			name:  "empty input uploads nothing",
			files: []InlineFile{},
		},
		{
			name:            "more than max files rejected",
			files:           makeInlineFiles(maxFilesPerPost + 1),
			wantErrContains: []string{"at most 10"},
		},
		{
			name: "invalid later file rejects the whole list before any upload",
			files: []InlineFile{
				{Name: "good.md", Content: "fine"},
				{Name: "bad.md", Content: ""},
			},
			wantErrContains: []string{`"bad.md"`, "no content"},
			wantNoUploads:   true,
		},
		{
			name:            "blank name rejected",
			files:           []InlineFile{{Name: "   ", Content: "x"}},
			wantErrContains: []string{"invalid file name"},
		},
		{
			name:            "dot name rejected",
			files:           []InlineFile{{Name: ".", Content: "x"}},
			wantErrContains: []string{"invalid file name"},
		},
		{
			name:            "overlong name rejected",
			files:           []InlineFile{{Name: strings.Repeat("a", 256) + ".txt", Content: "x"}},
			wantErrContains: []string{"255"},
		},
		{
			name:          "255-rune multibyte name accepted (limit counts characters, not bytes)",
			files:         []InlineFile{{Name: strings.Repeat("é", 252) + ".md", Content: "x"}},
			wantFilenames: []string{strings.Repeat("é", 252) + ".md"},
			wantContents:  []string{"x"},
		},
		{
			name:            "256-rune multibyte name rejected",
			files:           []InlineFile{{Name: strings.Repeat("é", 253) + ".md", Content: "x"}},
			wantErrContains: []string{"255"},
		},
		{
			name:          "path traversal uploads under base name",
			files:         []InlineFile{{Name: "../../etc/passwd", Content: "root"}},
			wantFilenames: []string{"passwd"},
			wantContents:  []string{"root"},
		},
		{
			name:            "backslash traversal rejected",
			files:           []InlineFile{{Name: `..\..\evil.md`, Content: "x"}},
			wantErrContains: []string{"invalid file name"},
		},
		{
			name:            "empty content rejected",
			files:           []InlineFile{{Name: "notes.txt"}},
			wantErrContains: []string{`"notes.txt"`, "no content"},
		},
		{
			name:            "oversize content rejected",
			files:           []InlineFile{{Name: "big.txt", Content: strings.Repeat("a", maxInlineFileBytes+1)}},
			wantErrContains: []string{`"big.txt"`, "1048576"},
		},
		{
			name:            "upload failure names the file",
			files:           []InlineFile{{Name: "ok.md", Content: "fine"}, {Name: "bad.md", Content: "boom"}},
			failFilename:    "bad.md",
			wantErrContains: []string{`"bad.md"`},
		},
		{
			name:          "happy path uploads two files",
			files:         []InlineFile{{Name: "report.md", Content: "# Report"}, {Name: "data.csv", Content: "a;b\n1;2"}},
			wantFilenames: []string{"report.md", "data.csv"},
			wantContents:  []string{"# Report", "a;b\n1;2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, rec := newInlineFileUploadServer(t, tt.failFilename)

			fileIDs, err := uploadInlineFiles(t.Context(), newTestClient(server.URL), model.NewId(), tt.files)
			if len(tt.wantErrContains) > 0 {
				require.Error(t, err)
				for _, want := range tt.wantErrContains {
					assert.Contains(t, err.Error(), want)
				}
				if tt.wantNoUploads {
					assert.Empty(t, rec.filenames, "validation failures must reject the list before any upload")
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantFilenames, rec.filenames)
			assert.Equal(t, tt.wantContents, rec.contents)
			assert.Equal(t, rec.fileIDs, fileIDs)
		})
	}
}

func TestFetchFileDataForLocal_AbsolutePathRoots(t *testing.T) {
	allowedDir := t.TempDir()
	otherDir := t.TempDir()

	fileContent := []byte("attachment payload")
	allowedFile := filepath.Join(allowedDir, "report.txt")
	require.NoError(t, os.WriteFile(allowedFile, fileContent, 0600))

	nestedFile := filepath.Join(allowedDir, "sub", "nested.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(nestedFile), 0700))
	require.NoError(t, os.WriteFile(nestedFile, fileContent, 0600))

	outsideFile := filepath.Join(otherDir, "secret.txt")
	require.NoError(t, os.WriteFile(outsideFile, fileContent, 0600))

	linkToOutside := filepath.Join(allowedDir, "escape-link")
	require.NoError(t, os.Symlink(outsideFile, linkToOutside))

	testCases := []struct {
		name     string
		roots    string
		filespec string
		wantData bool
	}{
		{
			name:     "no roots configured rejects absolute paths",
			roots:    "",
			filespec: allowedFile,
			wantData: false,
		},
		{
			name:     "file inside an allowed root is readable",
			roots:    allowedDir,
			filespec: allowedFile,
			wantData: true,
		},
		{
			name:     "nested file inside an allowed root is readable",
			roots:    allowedDir,
			filespec: nestedFile,
			wantData: true,
		},
		{
			name:     "second entry in the root list also matches",
			roots:    otherDir + string(filepath.ListSeparator) + allowedDir,
			filespec: allowedFile,
			wantData: true,
		},
		{
			name:     "file outside every allowed root is rejected",
			roots:    allowedDir,
			filespec: outsideFile,
			wantData: false,
		},
		{
			name:     "path traversal out of an allowed root is rejected",
			roots:    allowedDir,
			filespec: filepath.Join(allowedDir, "..", filepath.Base(otherDir), "secret.txt"),
			wantData: false,
		},
		{
			name:     "symlink escaping an allowed root is rejected",
			roots:    allowedDir,
			filespec: linkToOutside,
			wantData: false,
		},
		{
			name:     "relative root entries are ignored",
			roots:    "relative/dir",
			filespec: allowedFile,
			wantData: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AttachmentRootsEnvVar, tc.roots)

			data, err := fetchFileDataForLocal(t.Context(), tc.filespec, AccessModeLocal)
			if tc.wantData {
				require.NoError(t, err)
				require.Equal(t, fileContent, data)
			} else {
				require.Error(t, err)
			}
		})
	}
}
