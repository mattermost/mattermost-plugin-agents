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

// boundedRoundTripper bounds how long Agents waits on PluginHTTP, but it cannot
// cancel the underlying PluginHTTP execution once started.
type boundedRoundTripper struct {
	base http.RoundTripper
}

func (b *boundedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if b == nil || b.base == nil {
		return nil, fmt.Errorf("bounded round tripper not initialized")
	}

	type roundTripResult struct {
		resp *http.Response
		err  error
	}

	respCh := make(chan roundTripResult, 1)
	go func() {
		resp, err := b.base.RoundTrip(req)
		select {
		case respCh <- roundTripResult{resp: resp, err: err}:
		case <-req.Context().Done():
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
	}()

	select {
	case result := <-respCh:
		return result.resp, result.err
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

// headerInjector sets fixed headers on every outbound request.
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

func newProxyHTTPClient(ctx context.Context, cfg mcppkg.PluginServerConfig, sourcePluginAPI mmapi.Client, callerUserID string) *http.Client {
	transport := http.RoundTripper(&boundedRoundTripper{
		base: mcppkg.NewPluginHTTPRoundTripper(cfg.PluginID, cfg.Path, sourcePluginAPI),
	})
	if callerUserID != "" {
		transport = &headerInjector{
			base:    transport,
			headers: map[string]string{mmUserIDHeader: callerUserID},
		}
	}

	client := &http.Client{Transport: transport}
	if deadline, ok := ctx.Deadline(); ok {
		client.Timeout = time.Until(deadline)
	}
	return client
}

func connectProxySession(ctx context.Context, cfg mcppkg.PluginServerConfig, sourcePluginAPI mmapi.Client, callerUserID string) (*gosdkmcp.ClientSession, error) {
	client := gosdkmcp.NewClient(
		&gosdkmcp.Implementation{Name: "mattermost-agents-plugin-aggregator", Version: "1.0"},
		&gosdkmcp.ClientOptions{},
	)
	return client.Connect(ctx, &gosdkmcp.StreamableClientTransport{
		Endpoint:   "http://plugin" + cfg.Path,
		HTTPClient: newProxyHTTPClient(ctx, cfg, sourcePluginAPI, callerUserID),
	}, nil)
}

// BuildProxyTools proxies a source plugin's MCP tools into the external server.
func BuildProxyTools(
	ctx context.Context,
	cfg mcppkg.PluginServerConfig,
	sourcePluginAPI mmapi.Client,
) ([]*gosdkmcp.Tool, []gosdkmcp.ToolHandler, error) {
	if sourcePluginAPI == nil {
		return nil, nil, fmt.Errorf("sourcePluginAPI is nil; plugin MCP server %s cannot be reached", cfg.PluginID)
	}

	listSession, err := connectProxySession(ctx, cfg, sourcePluginAPI, "")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to plugin MCP server %s: %w", cfg.PluginID, err)
	}
	defer func() { _ = listSession.Close() }()

	result, err := listSession.ListTools(ctx, &gosdkmcp.ListToolsParams{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list tools on plugin MCP server %s: %w", cfg.PluginID, err)
	}
	if result == nil {
		return nil, nil, fmt.Errorf("plugin MCP server %s returned nil ListTools result", cfg.PluginID)
	}

	tools := make([]*gosdkmcp.Tool, 0, len(result.Tools))
	handlers := make([]gosdkmcp.ToolHandler, 0, len(result.Tools))

	for _, remote := range result.Tools {
		t := &gosdkmcp.Tool{
			Name:        remote.Name,
			Description: remote.Description,
			InputSchema: remote.InputSchema,
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

			session, err := connectProxySession(hctx, pluginCfg, sourcePluginAPI, callerUserID)
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
			if callResult == nil {
				return nil, fmt.Errorf("proxy tool %s: plugin returned nil CallTool result", toolName)
			}
			return callResult, nil
		})
	}

	return tools, handlers, nil
}
