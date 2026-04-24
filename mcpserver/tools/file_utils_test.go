// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/shared/httpservice"
	"github.com/stretchr/testify/require"
)

func TestFetchFileDataForLocal_InvalidURLSpecs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
	}))
	t.Cleanup(server.Close)

	testCases := []struct {
		name        string
		filespec    string
		errContains string
	}{
		{
			name:        "loopback URL from httptest is rejected",
			filespec:    server.URL,
			errContains: httpservice.ErrAddressForbidden.Error(),
		},
		{
			name:        "URL missing host",
			filespec:    "https:///path",
			errContains: "URL missing host",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fetchFileDataForLocal(t.Context(), tc.filespec, AccessModeLocal)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errContains)
		})
	}
}
