// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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

func TestUploadFilesAndUrlsForLocal_FailureAbortsPost(t *testing.T) {
	testCases := []struct {
		name        string
		attachments []string
		accessMode  AccessMode
		wantErr     bool
	}{
		{
			name:        "no attachments is a no-op",
			attachments: nil,
			accessMode:  AccessModeLocal,
			wantErr:     false,
		},
		{
			name:        "attachments in remote mode return an error instead of posting silently",
			attachments: []string{"anything.txt"},
			accessMode:  AccessModeRemote,
			wantErr:     true,
		},
		{
			name:        "unreadable attachment returns an error instead of posting silently",
			attachments: []string{"does-not-exist.txt"},
			accessMode:  AccessModeLocal,
			wantErr:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fileIDs, message, err := uploadFilesAndUrlsForLocal(t.Context(), nil, "channelid", tc.attachments, tc.accessMode)
			if tc.wantErr {
				require.Error(t, err)
				require.Empty(t, fileIDs)
			} else {
				require.NoError(t, err)
				require.Empty(t, fileIDs)
				require.Empty(t, message)
			}
		})
	}
}
