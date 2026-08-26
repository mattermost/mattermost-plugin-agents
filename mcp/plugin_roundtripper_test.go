// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPluginHTTPRoundTripper_RewritesURLPath(t *testing.T) {
	tests := []struct {
		name         string
		pluginID     string
		basePath     string
		inputPath    string
		expectedPath string
	}{
		{
			name:         "basic rewrite",
			pluginID:     "com.mattermost.plugin-mcp-demo",
			basePath:     "/mcp",
			inputPath:    "/placeholder",
			expectedPath: "/com.mattermost.plugin-mcp-demo/mcp",
		},
		{
			name:         "empty base path treats pluginID as target",
			pluginID:     "com.mattermost.plugin-mcp-demo",
			basePath:     "",
			inputPath:    "/any/thing",
			expectedPath: "/com.mattermost.plugin-mcp-demo",
		},
		{
			name:         "base path with trailing segment",
			pluginID:     "com.example.test",
			basePath:     "/api/mcp",
			inputPath:    "/ignored",
			expectedPath: "/com.example.test/api/mcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := mocks.NewMockClient(t)

			var captured *http.Request
			mockAPI.On("PluginHTTP", mock.Anything).
				Run(func(args mock.Arguments) {
					captured = args.Get(0).(*http.Request)
				}).
				Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte("{}"))),
					Header:     http.Header{},
				}).
				Once()

			rt := &PluginHTTPRoundTripper{
				pluginID:  tt.pluginID,
				basePath:  tt.basePath,
				pluginAPI: mockAPI,
			}

			req, err := http.NewRequest(http.MethodPost, "http://placeholder"+tt.inputPath, bytes.NewReader([]byte("{}")))
			require.NoError(t, err)

			resp, err := rt.RoundTrip(req)
			require.NoError(t, err)
			require.NotNil(t, resp)

			require.NotNil(t, captured)
			assert.Equal(t, tt.expectedPath, captured.URL.Path)
		})
	}
}

// Verifies headerTransport layering: the user-ID header set by the enclosing
// http.Client reaches PluginHTTP unchanged.
func TestPluginHTTPRoundTripper_PreservesHeaders(t *testing.T) {
	mockAPI := mocks.NewMockClient(t)
	var captured *http.Request
	mockAPI.On("PluginHTTP", mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(0).(*http.Request) }).
		Return(&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil)), Header: http.Header{}}).
		Once()

	rt := &PluginHTTPRoundTripper{
		pluginID:  "com.mattermost.plugin-mcp-demo",
		basePath:  "/mcp",
		pluginAPI: mockAPI,
	}
	httpClient := &http.Client{
		Transport: &headerTransport{
			base:    rt,
			headers: map[string]string{MMUserIDHeader: "alice"},
		},
	}

	req, err := http.NewRequest(http.MethodPost, "http://placeholder/mcp", nil)
	require.NoError(t, err)
	_, err = httpClient.Do(req)
	require.NoError(t, err)

	require.NotNil(t, captured)
	assert.Equal(t, "alice", captured.Header.Get(MMUserIDHeader))
	assert.Equal(t, "/com.mattermost.plugin-mcp-demo/mcp", captured.URL.Path)
}

func TestPluginHTTPRoundTripper_NilResponse(t *testing.T) {
	mockAPI := mocks.NewMockClient(t)
	mockAPI.On("PluginHTTP", mock.Anything).Return((*http.Response)(nil)).Once()

	rt := &PluginHTTPRoundTripper{
		pluginID:  "com.mattermost.plugin-mcp-demo",
		basePath:  "/mcp",
		pluginAPI: mockAPI,
	}

	req, err := http.NewRequest(http.MethodPost, "http://placeholder/mcp", nil)
	require.NoError(t, err)

	_, err = rt.RoundTrip(req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "com.mattermost.plugin-mcp-demo")
}

func TestPluginHTTPRoundTripper_NilPluginAPI(t *testing.T) {
	rt := &PluginHTTPRoundTripper{
		pluginID:  "com.mattermost.plugin-mcp-demo",
		basePath:  "/mcp",
		pluginAPI: nil,
	}

	req, err := http.NewRequest(http.MethodPost, "http://placeholder/mcp", nil)
	require.NoError(t, err)

	_, err = rt.RoundTrip(req)
	assert.Error(t, err)
}

func TestPluginHTTPRoundTripper_ReturnsWhenContextExpires(t *testing.T) {
	release := make(chan struct{})
	mockAPI := mocks.NewMockClient(t)
	mockAPI.On("PluginHTTP", mock.Anything).
		Run(func(mock.Arguments) {
			<-release
		}).
		Return(&http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Header:     http.Header{},
		}).
		Once()

	rt := NewPluginHTTPRoundTripper("com.example.slow", "/mcp", mockAPI)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://placeholder/mcp", nil)
	require.NoError(t, err)

	start := time.Now()
	resp, err := rt.RoundTrip(req)

	require.Nil(t, resp)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)

	close(release)
}
