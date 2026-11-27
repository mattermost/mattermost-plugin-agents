// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"time"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/trace"
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

// detachFromInboundMCPRequest returns a context for calling the source plugin
// that keeps ctx's cancellation, deadline and trace span but none of its other
// values. The external server's go-sdk handler stores the caller's
// Mcp-Protocol-Version in the request context under an unexported key, and the
// go-sdk client sends that version instead of the one it negotiates with the
// source plugin. A source plugin on an older SDK rejects a version it does not
// know with a 400.
func detachFromInboundMCPRequest(ctx context.Context) (context.Context, context.CancelFunc) {
	detached := trace.ContextWithSpan(context.Background(), trace.SpanFromContext(ctx))
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		detached, cancel = context.WithDeadline(detached, deadline)
	} else {
		detached, cancel = context.WithCancel(detached)
	}
	stop := context.AfterFunc(ctx, cancel)
	return detached, func() {
		stop()
		cancel()
	}
}

// callerSessionMetaKeys are the per-request _meta keys that describe the
// caller's session with the external server (SEP-2575). The proxy's own client
// session fills them for the version it negotiated with the source plugin, and
// a forwarded protocolVersion would take precedence over it.
var callerSessionMetaKeys = []string{
	gosdkmcp.MetaKeyProtocolVersion,
	gosdkmcp.MetaKeyClientInfo,
	gosdkmcp.MetaKeyClientCapabilities,
}

func withoutCallerSessionMeta(meta gosdkmcp.Meta) gosdkmcp.Meta {
	if len(meta) == 0 {
		return nil
	}
	forwarded := maps.Clone(meta)
	for _, key := range callerSessionMetaKeys {
		delete(forwarded, key)
	}
	if len(forwarded) == 0 {
		return nil
	}
	return forwarded
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

			ctx, cancel := detachFromInboundMCPRequest(hctx)
			defer cancel()

			session, err := connectProxySession(ctx, cfg, sourcePluginAPI, callerUserID)
			if err != nil {
				return nil, fmt.Errorf("proxy tool %s: connect failed: %w", toolName, err)
			}
			defer func() { _ = session.Close() }()

			callResult, callErr := session.CallTool(ctx, &gosdkmcp.CallToolParams{
				Name:      toolName,
				Arguments: req.Params.Arguments,
				Meta:      withoutCallerSessionMeta(req.Params.Meta),
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
