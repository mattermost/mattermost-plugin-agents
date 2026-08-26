// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
)

// PluginHTTPRoundTripper routes requests to a source plugin's MCP endpoint via
// PluginHTTP. Callers layer user headers above it.
type PluginHTTPRoundTripper struct {
	pluginID string
	basePath string
	// pluginAPI is the Agents plugin's mmapi client used to reach the source plugin.
	pluginAPI mmapi.Client
}

// NewPluginHTTPRoundTripper constructs a PluginHTTP-based transport for a
// source plugin MCP endpoint.
func NewPluginHTTPRoundTripper(pluginID, basePath string, pluginAPI mmapi.Client) *PluginHTTPRoundTripper {
	return &PluginHTTPRoundTripper{
		pluginID:  pluginID,
		basePath:  basePath,
		pluginAPI: pluginAPI,
	}
}

// RoundTrip rewrites req.URL.Path to "/{pluginID}{basePath}", the path
// PluginHTTP dispatches on, and returns as soon as the request context is done.
// The underlying PluginHTTP call cannot be canceled, so it keeps running on its
// own goroutine; a response that arrives after cancellation is closed rather
// than leaked.
func (p *PluginHTTPRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if p == nil || p.pluginAPI == nil {
		return nil, fmt.Errorf("plugin MCP round tripper not initialized")
	}

	r := req.Clone(req.Context())
	basePath := p.basePath
	if basePath != "" && basePath[0] != '/' {
		basePath = "/" + basePath
	}
	r.URL.Path = "/" + p.pluginID + basePath

	// The handoff is unbuffered so a response the caller has stopped waiting for
	// is closed instead of being left unread in the channel.
	responses := make(chan *http.Response)
	go func() {
		resp := p.pluginAPI.PluginHTTP(r)
		select {
		case responses <- resp:
		case <-r.Context().Done():
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
	}()

	select {
	case resp := <-responses:
		if resp == nil {
			return nil, fmt.Errorf("PluginHTTP returned nil response for plugin %s", p.pluginID)
		}
		return resp, nil
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
}
