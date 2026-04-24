// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
)

// PluginHTTPRoundTripper routes HTTP requests to a source Mattermost plugin
// via the Mattermost plugin-to-plugin HTTP transport (PluginHTTP). It is the
// reverse-direction mirror of public/bridgeclient/transport.go's
// pluginAPIRoundTripper: that RoundTripper lets source plugins call INTO the
// agents plugin's bridge API; this one lets the agents plugin call OUT to a
// source plugin's MCP endpoint (registered via mcphelper.Server).
//
// The caller MUST layer a header-setting RoundTripper (e.g. mcp.headerTransport)
// ABOVE this one to inject X-Mattermost-UserID on every request — PluginHTTP
// routing does NOT strip X-Mattermost-UserID (only Mattermost-Plugin-ID is
// stripped on external requests), but the source plugin's mcphelper.ServeHTTP
// extracts the header from the already-authenticated inter-plugin call.
// This RoundTripper itself knows only pluginID + basePath + the source of
// PluginHTTP; it is intentionally per-user-agnostic so one instance can be
// reused across concurrent user sessions.
type PluginHTTPRoundTripper struct {
	// pluginID is the target source plugin (e.g. "com.mattermost.plugin-mcp-demo").
	pluginID string
	// basePath is the MCP mount path inside the source plugin (e.g. "/mcp").
	// It is the Path field of PluginServerConfig as registered via the bridge.
	basePath string
	// pluginAPI supplies PluginHTTP; this is the agents-plugin mmapi.Client
	// (sourcePluginAPI on ClientManager), NOT the source plugin's own API.
	pluginAPI mmapi.Client
}

// RoundTrip rewrites req.URL.Path to "/{pluginID}{basePath}" — the path format
// PluginHTTP uses to dispatch to a target plugin — and delegates to PluginHTTP.
// The original req.URL.Path is discarded on the assumption that MCP Streamable
// HTTP transports use a single fixed endpoint for all request/response traffic
// (verified against go-sdk v1.4.1).
func (p *PluginHTTPRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if p == nil || p.pluginAPI == nil {
		return nil, fmt.Errorf("plugin MCP round tripper not initialized")
	}

	// Clone the request so we don't mutate a caller's request object.
	r := req.Clone(req.Context())

	// PluginHTTP routes on the first path segment.
	r.URL.Path = "/" + p.pluginID + p.basePath

	resp := p.pluginAPI.PluginHTTP(r)
	if resp == nil {
		return nil, fmt.Errorf("PluginHTTP returned nil response for plugin %s", p.pluginID)
	}
	return resp, nil
}
