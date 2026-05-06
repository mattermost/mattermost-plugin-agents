// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakePluginAPI struct {
	response *http.Response
}

func (f *fakePluginAPI) PluginHTTP(_ *http.Request) *http.Response {
	return f.response
}

type fakeAppAPI struct {
	called              bool
	gotUserID           string
	gotSourcePluginID   string
	gotDestinationID    string
	gotPath             string
	responseStatusCode  int
	responseBodyContent string
}

func (f *fakeAppAPI) ServeInternalPluginRequest(userID string, w http.ResponseWriter, r *http.Request, sourcePluginID, destinationPluginID string) {
	f.called = true
	f.gotUserID = userID
	f.gotSourcePluginID = sourcePluginID
	f.gotDestinationID = destinationPluginID
	f.gotPath = r.URL.Path

	w.WriteHeader(f.responseStatusCode)
	_, _ = w.Write([]byte(f.responseBodyContent))
}

func TestPluginAPIRoundTripper(t *testing.T) {
	t.Run("returns transport error when plugin returns nil response", func(t *testing.T) {
		rt := &pluginAPIRoundTripper{
			api: &fakePluginAPI{response: nil},
		}

		req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents", nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "failed to make interplugin request")
	})

	t.Run("returns plugin response as-is", func(t *testing.T) {
		rt := &pluginAPIRoundTripper{
			api: &fakePluginAPI{
				response: &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       io.NopCloser(strings.NewReader("")),
				},
			},
		}

		req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/services", nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
}

func TestRemoveFirstPath(t *testing.T) {
	t.Run("removes first path segment", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/agents", nil)
		require.NoError(t, err)

		removeFirstPath(req)
		require.Equal(t, "/bridge/v1/agents", req.URL.Path)
	})

	t.Run("sets root when only one segment exists", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "/mattermost-ai", nil)
		require.NoError(t, err)

		removeFirstPath(req)
		require.Equal(t, "/", req.URL.Path)
	})
}

func TestAppAPIRoundTripper(t *testing.T) {
	fakeAPI := &fakeAppAPI{
		responseStatusCode:  http.StatusOK,
		responseBodyContent: "ok",
	}

	rt := &appAPIRoundTripper{
		api:    fakeAPI,
		userID: "abcdefghijklmnopqrstuvwxyz",
	}

	req, err := http.NewRequest(http.MethodGet, "/mattermost-ai/bridge/v1/services", nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	require.True(t, fakeAPI.called)
	require.Equal(t, "abcdefghijklmnopqrstuvwxyz", fakeAPI.gotUserID)
	require.Equal(t, mattermostServerID, fakeAPI.gotSourcePluginID)
	require.Equal(t, AiPluginID, fakeAPI.gotDestinationID)
	require.Equal(t, "/bridge/v1/services", fakeAPI.gotPath)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	bodyBytes, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Equal(t, "ok", string(bodyBytes))
}
