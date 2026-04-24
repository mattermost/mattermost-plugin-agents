// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/shared/httpservice"
	"github.com/stretchr/testify/require"
)

func TestFetchFileDataForLocal_LoopbackURLRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
	}))
	t.Cleanup(server.Close)

	_, err := fetchFileDataForLocal(t.Context(), server.URL, AccessModeLocal)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), httpservice.ErrAddressForbidden.Error()),
		"expected loopback URL to be rejected, got: %v", err)
}

func TestFetchFileDataForLocal_URLEmptyHostRejected(t *testing.T) {
	_, err := fetchFileDataForLocal(t.Context(), "https:///path", AccessModeLocal)
	require.Error(t, err)
}
