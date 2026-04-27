// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mmUserIDHeader propagates the calling Mattermost user ID through PluginHTTP.
const mmUserIDHeader = "X-Mattermost-UserID"

// proxyRoundTripper rewrites an outbound request's URL.Path to
// "/{pluginID}{basePath}" and delegates to PluginHTTP.
type proxyRoundTripper struct {
	pluginID  string
	basePath  string
	pluginAPI mmapi.Client
}

func (p *proxyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if p == nil || p.pluginAPI == nil {
		return nil, fmt.Errorf("proxy round tripper not initialized")
	}
	r := req.Clone(req.Context())
	r.URL.Path = "/" + p.pluginID + p.basePath

	respCh := make(chan *http.Response, 1)
	go func() {
		respCh <- p.pluginAPI.PluginHTTP(r)
	}()

	var resp *http.Response
	select {
	case resp = <-respCh:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	if resp == nil {
		return nil, fmt.Errorf("PluginHTTP returned nil response for plugin %s", p.pluginID)
	}
	return resp, nil
}

// headerInjector sets fixed headers on every outbound request before delegating.
type headerInjector struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerInjector) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return h.base.RoundTrip(r)
}

// BuildProxyTools lists a source plugin's MCP tools and returns proxy tool
// definitions plus handlers for the Agents plugin's external MCP server.
// Names and input schemas are copied verbatim from the source plugin.
func BuildProxyTools(
	ctx context.Context,
	cfg mcppkg.PluginServerConfig,
	sourcePluginAPI mmapi.Client,
) ([]*gosdkmcp.Tool, []gosdkmcp.ToolHandler, error) {
	if sourcePluginAPI == nil {
		return nil, nil, fmt.Errorf("sourcePluginAPI is nil; plugin MCP server %s cannot be reached", cfg.PluginID)
	}

	rt := &proxyRoundTripper{
		pluginID:  cfg.PluginID,
		basePath:  cfg.Path,
		pluginAPI: sourcePluginAPI,
	}
	httpClient := &http.Client{Transport: rt}
	if deadline, ok := ctx.Deadline(); ok {
		httpClient.Timeout = time.Until(deadline)
	}
	listClient := gosdkmcp.NewClient(
		&gosdkmcp.Implementation{Name: "mattermost-agents-plugin-aggregator", Version: "1.0"},
		&gosdkmcp.ClientOptions{},
	)
	listSession, err := listClient.Connect(ctx, &gosdkmcp.StreamableClientTransport{
		Endpoint:   "http://plugin" + cfg.Path,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to plugin MCP server %s: %w", cfg.PluginID, err)
	}
	defer func() { _ = listSession.Close() }()

	result, err := listSession.ListTools(ctx, &gosdkmcp.ListToolsParams{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list tools on plugin MCP server %s: %w", cfg.PluginID, err)
	}

	tools := make([]*gosdkmcp.Tool, 0, len(result.Tools))
	handlers := make([]gosdkmcp.ToolHandler, 0, len(result.Tools))

	for _, remote := range result.Tools {
		t := &gosdkmcp.Tool{
			Name:        remote.Name,
			Description: remote.Description,
			InputSchema: remote.InputSchema, // JSON-marshalable `any`; pass-through is correct
			Annotations: remote.Annotations,
		}
		tools = append(tools, t)

		pluginCfg := cfg
		toolName := t.Name
		handlers = append(handlers, func(hctx context.Context, req *gosdkmcp.CallToolRequest) (*gosdkmcp.CallToolResult, error) {
			callerUserID, ok := hctx.Value(auth.UserIDContextKey).(string)
			if !ok || callerUserID == "" {
				return nil, fmt.Errorf("proxy tool %s: authenticated user ID not found in context", toolName)
			}

			perCallRT := &proxyRoundTripper{
				pluginID:  pluginCfg.PluginID,
				basePath:  pluginCfg.Path,
				pluginAPI: sourcePluginAPI,
			}
			httpClient := &http.Client{Transport: &headerInjector{
				base:    perCallRT,
				headers: map[string]string{mmUserIDHeader: callerUserID},
			}}
			callClient := gosdkmcp.NewClient(
				&gosdkmcp.Implementation{Name: "mattermost-agents-plugin-aggregator", Version: "1.0"},
				&gosdkmcp.ClientOptions{},
			)
			session, err := callClient.Connect(hctx, &gosdkmcp.StreamableClientTransport{
				Endpoint:   "http://plugin" + pluginCfg.Path,
				HTTPClient: httpClient,
			}, nil)
			if err != nil {
				return nil, fmt.Errorf("proxy tool %s: connect failed: %w", toolName, err)
			}
			defer func() { _ = session.Close() }()

			callResult, callErr := session.CallTool(hctx, &gosdkmcp.CallToolParams{
				Name:      toolName,
				Arguments: req.Params.Arguments,
				Meta:      req.Params.Meta,
			})
			if callErr != nil {
				return nil, fmt.Errorf("proxy tool %s: call failed: %w", toolName, callErr)
			}
			return callResult, nil
		})
	}

	return tools, handlers, nil
}
