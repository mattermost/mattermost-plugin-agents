// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
)

// PluginHTTPRoundTripper routes requests from the Agents plugin to a source
// plugin's MCP endpoint via PluginHTTP. Callers layer user headers above it.
type PluginHTTPRoundTripper struct {
	pluginID string
	basePath string
	// pluginAPI is the agents-plugin mmapi.Client (sourcePluginAPI on
	// ClientManager), NOT the source plugin's own API.
	pluginAPI mmapi.Client
}

// RoundTrip rewrites req.URL.Path to "/{pluginID}{basePath}" — the path format
// PluginHTTP uses to dispatch to a target plugin. The original req.URL.Path is
// discarded; MCP Streamable HTTP uses a single endpoint (verified vs go-sdk v1.4.1).
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

	resp := p.pluginAPI.PluginHTTP(r)
	if resp == nil {
		return nil, fmt.Errorf("PluginHTTP returned nil response for plugin %s", p.pluginID)
	}
	return resp, nil
}
