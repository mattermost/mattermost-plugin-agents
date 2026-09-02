// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
// PluginHTTP dispatches on.
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

// PluginServerHTTPClient builds the HTTP client used to reach a
// plugin-registered MCP server through base (typically a
// PluginHTTPRoundTripper). When userID is non-empty, it is propagated on every
// request via the X-Mattermost-UserID header.
func PluginServerHTTPClient(base http.RoundTripper, userID string) *http.Client {
	transport := base
	if userID != "" {
		transport = &headerTransport{
			base:    base,
			headers: map[string]string{MMUserIDHeader: userID},
		}
	}
	return &http.Client{Transport: transport}
}

// ConnectPluginServer connects a hardened MCP client (see NewSDKClient) named
// clientName to the plugin-registered MCP server at path, using httpClient for
// PluginHTTP transport.
func ConnectPluginServer(ctx context.Context, clientName, path string, httpClient *http.Client) (*mcp.ClientSession, error) {
	client := NewSDKClient(
		&mcp.Implementation{
			Name:    clientName,
			Version: "1.0",
		},
		nil,
	)
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   "http://plugin" + path,
		HTTPClient: httpClient,
	}, nil)
}
