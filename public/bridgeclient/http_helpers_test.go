// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDoGetJSON(t *testing.T) {
	t.Run("create request failure", func(t *testing.T) {
		client := &Client{}

		var response AgentsResponse
		err := client.doGetJSON("%", &response)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to create request")
	})

	t.Run("execute request failure", func(t *testing.T) {
		client := &Client{}
		client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed")
		})

		var response AgentsResponse
		err := client.doGetJSON("/mattermost-ai/bridge/v1/agents", &response)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to execute request")
	})

	t.Run("non-200 uses parsed error payload", func(t *testing.T) {
		client := &Client{}
		client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"forbidden"}`)),
			}, nil
		})

		var response AgentsResponse
		err := client.doGetJSON("/mattermost-ai/bridge/v1/agents", &response)
		require.Error(t, err)
		require.EqualError(t, err, "request failed with status 403: forbidden")
	})

	t.Run("non-200 empty body returns status-only error", func(t *testing.T) {
		client := &Client{}
		client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(" \n\t")),
			}, nil
		})

		var response AgentsResponse
		err := client.doGetJSON("/mattermost-ai/bridge/v1/agents", &response)
		require.Error(t, err)
		require.EqualError(t, err, "request failed with status 502")
	})

	t.Run("read body failure", func(t *testing.T) {
		client := &Client{}
		client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(&erroringReader{
					err: errors.New("read failure"),
				}),
			}, nil
		})

		var response AgentsResponse
		err := client.doGetJSON("/mattermost-ai/bridge/v1/agents", &response)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to read response body")
	})

	t.Run("unmarshal failure", func(t *testing.T) {
		client := &Client{}
		client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"agents":"invalid"}`)),
			}, nil
		})

		var response AgentsResponse
		err := client.doGetJSON("/mattermost-ai/bridge/v1/agents", &response)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal response")
	})

	t.Run("success", func(t *testing.T) {
		client := &Client{}
		client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, req.Method)
			require.Equal(t, "/mattermost-ai/bridge/v1/agents", req.URL.Path)

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
					"agents":[{"id":"abcdefghijklmnopqrstuvwxyz","displayName":"Agent"}]
				}`)),
			}, nil
		})

		var response AgentsResponse
		err := client.doGetJSON("/mattermost-ai/bridge/v1/agents", &response)
		require.NoError(t, err)
		require.Len(t, response.Agents, 1)
		require.Equal(t, "abcdefghijklmnopqrstuvwxyz", response.Agents[0].ID)
		require.Equal(t, "Agent", response.Agents[0].DisplayName)
	})
}
