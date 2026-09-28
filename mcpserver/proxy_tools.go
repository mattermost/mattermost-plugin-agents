// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newProxyHTTPClient(ctx context.Context, cfg mcppkg.PluginServerConfig, sourcePluginAPI mmapi.Client, callerUserID string) *http.Client {
	client := mcppkg.PluginServerHTTPClient(
		mcppkg.NewPluginHTTPRoundTripper(cfg.PluginID, cfg.Path, sourcePluginAPI),
		callerUserID,
	)
	if deadline, ok := ctx.Deadline(); ok {
		client.Timeout = time.Until(deadline)
	}
	return client
}

func connectProxySession(ctx context.Context, cfg mcppkg.PluginServerConfig, sourcePluginAPI mmapi.Client, callerUserID string) (*gosdkmcp.ClientSession, error) {
	return mcppkg.ConnectPluginServer(ctx, "mattermost-agents-plugin-aggregator", cfg.Path,
		newProxyHTTPClient(ctx, cfg, sourcePluginAPI, callerUserID))
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

	remoteTools, err := mcppkg.ListSessionTools(ctx, listSession)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list tools on plugin MCP server %s: %w", cfg.PluginID, err)
	}

	tools := make([]*gosdkmcp.Tool, 0, len(remoteTools))
	handlers := make([]gosdkmcp.ToolHandler, 0, len(remoteTools))

	for _, remote := range remoteTools {
		t := &gosdkmcp.Tool{
			Name:        remote.Name,
			Description: remote.Description,
			InputSchema: remote.InputSchema,
			Annotations: remote.Annotations,
		}
		tools = append(tools, t)

		toolName := t.Name
		handlers = append(handlers, func(hctx context.Context, req *gosdkmcp.CallToolRequest) (*gosdkmcp.CallToolResult, error) {
			callerUserID, ok := hctx.Value(auth.UserIDContextKey).(string)
			if !ok || callerUserID == "" {
				return nil, fmt.Errorf("proxy tool %s: authenticated user ID not found in context", toolName)
			}

			session, err := connectProxySession(hctx, cfg, sourcePluginAPI, callerUserID)
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
