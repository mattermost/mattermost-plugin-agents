// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"net/http"
	"net/http/httptest"
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
