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

func TestGetAgentToolsValidation(t *testing.T) {
	client := &Client{}

	_, err := client.GetAgentTools("bad", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid agent ID")

	validID := "abcdefghijklmnopqrstuvwxyz"
	_, err = client.GetAgentTools(validID, "bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user ID")
}

func TestGetAgentsValidation(t *testing.T) {
	client := &Client{}

	_, err := client.GetAgents("bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user ID")
}

func TestGetServicesValidation(t *testing.T) {
	client := &Client{}

	_, err := client.GetServices("bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user ID")
}

func TestGetAgentToolsSuccess(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/agents/abcdefghijklmnopqrstuvwxyz/tools", req.URL.Path)
		require.Equal(t, "user_id=zyxwvutsrqponmlkjihgfedcba", req.URL.RawQuery)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"tools": [
					{"name":"weather_lookup","description":"Looks up weather"},
					{"name":"stock_quote","description":"Gets stock quote"}
				]
			}`)),
		}, nil
	})

	tools, err := client.GetAgentTools("abcdefghijklmnopqrstuvwxyz", "zyxwvutsrqponmlkjihgfedcba")
	require.NoError(t, err)
	require.Len(t, tools, 2)
	require.Equal(t, "weather_lookup", tools[0].Name)
	require.Equal(t, "Looks up weather", tools[0].Description)
	require.Equal(t, "stock_quote", tools[1].Name)
	require.Equal(t, "Gets stock quote", tools[1].Description)
}

func TestGetAgentToolsErrorResponse(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"missing permission"}`)),
		}, nil
	})

	_, err := client.GetAgentTools("abcdefghijklmnopqrstuvwxyz", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 403")
	require.Contains(t, err.Error(), "missing permission")
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
