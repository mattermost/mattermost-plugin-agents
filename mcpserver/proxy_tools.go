// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"
	"net/http"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mmUserIDHeader is the inter-plugin header used to propagate the calling
// Mattermost user ID through PluginHTTP. Duplicated here (see also
// mcp.MMUserIDHeader) to keep the mcpserver package free of mcp-internal
// symbols; the string constant is part of the cross-plugin wire protocol.
const mmUserIDHeader = "X-Mattermost-UserID"

// proxyRoundTripper rewrites an outbound request's URL.Path to
// "/{pluginID}{basePath}" and delegates to PluginHTTP. It is a local, minimal
// mirror of mcp.PluginHTTPRoundTripper (mcp/plugin_roundtripper.go). Duplicated
// here to avoid an mcpserver → mcp import edge that would require exporting
// that type's fields or adding a constructor; the logic is ~10 lines and the
// wire contract (PluginHTTP dispatches on leading path segment) is frozen by
// Mattermost core.
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
	resp := p.pluginAPI.PluginHTTP(r)
	if resp == nil {
		return nil, fmt.Errorf("PluginHTTP returned nil response for plugin %s", p.pluginID)
	}
	return resp, nil
}

// headerInjector is a tiny RoundTripper that sets fixed headers on every
// outbound request before delegating. Equivalent in spirit to an inter-plugin
// header transport but local to mcpserver to avoid importing private types
// from the mcp package.
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

// BuildProxyTools performs an ephemeral MCP connect to a source plugin's MCP
// endpoint (registered via the /bridge/v1/mcp/register handler, served by
// public/mcphelper.Server.ServeHTTP), lists its tools, and synthesizes proxy
// *mcp.Tool + handler pairs that the caller registers on the agents plugin's
// external MCP server at /plugins/mattermost-ai/mcp-server/mcp.
//
// Names and input schemas are copied verbatim from the remote ListTools
// response — the source plugin (via public/mcphelper) already namespaced names
// as {pluginID}__{toolName} and the go-sdk Tool.InputSchema field is already a
// JSON-Schema-compatible value (`any`, serialized as-is over the wire).
// No re-generation of schemas is needed on our side.
//
// Handlers read the authenticated user ID from auth.UserIDContextKey (set by
// delegateToMCPHandler in api/mcp_handlers.go) and inject X-Mattermost-UserID
// on the outbound PluginHTTP call. On each tool invocation the handler opens a
// fresh ephemeral MCP session against the source plugin and issues a single
// CallTool, then closes the session — stateless, no per-user caching at this
// layer. This keeps the proxy simple and matches the
// StreamableHTTPOptions{Stateless: true} semantics of the external MCP endpoint.
//
// BuildProxyTools does NOT cache connections or per-plugin results across
// invocations. RebuildExternalServer calls it fresh for every enabled plugin
// server on every rebuild — the rebuild path is rare (only on
// /bridge/v1/mcp/register and /unregister) and the extra simplicity beats the
// coherency headache of a stale cache.
func BuildProxyTools(
	ctx context.Context,
	cfg mcppkg.PluginServerConfig,
	sourcePluginAPI mmapi.Client,
) ([]*gosdkmcp.Tool, []gosdkmcp.ToolHandler, error) {
	if sourcePluginAPI == nil {
		return nil, nil, fmt.Errorf("sourcePluginAPI is nil; plugin MCP server %s cannot be reached", cfg.PluginID)
	}

	// Ephemeral connect. No X-Mattermost-UserID header here — ListTools does
	// not require user-scoped state in the source plugin's mcphelper. Tool
	// *invocation* (in the handler closure below) injects the header per-call.
	rt := &proxyRoundTripper{
		pluginID:  cfg.PluginID,
		basePath:  cfg.Path,
		pluginAPI: sourcePluginAPI,
	}
	listClient := gosdkmcp.NewClient(
		&gosdkmcp.Implementation{Name: "mattermost-agents-plugin-aggregator", Version: "1.0"},
		&gosdkmcp.ClientOptions{},
	)
	listSession, err := listClient.Connect(ctx, &gosdkmcp.StreamableClientTransport{
		Endpoint:   "http://plugin" + cfg.Path,
		HTTPClient: &http.Client{Transport: rt},
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
		// Defensive copy — do not share pointers with the remote session's
		// internal bookkeeping after the session closes.
		t := &gosdkmcp.Tool{
			Name:        remote.Name,
			Description: remote.Description,
			InputSchema: remote.InputSchema, // JSON-marshalable `any`; pass-through is correct
			Annotations: remote.Annotations,
		}
		tools = append(tools, t)

		// Bind the cfg + rt for this tool's handler. Intentional capture-by-value
		// of cfg so future iterations cannot aliasing-mutate it.
		pluginCfg := cfg
		toolName := t.Name
		handlers = append(handlers, func(hctx context.Context, req *gosdkmcp.CallToolRequest) (*gosdkmcp.CallToolResult, error) {
			callerUserID, ok := hctx.Value(auth.UserIDContextKey).(string)
			if !ok || callerUserID == "" {
				return nil, fmt.Errorf("proxy tool %s: authenticated user ID not found in context", toolName)
			}

			// Fresh per-call session. Stateless, no retry. If PluginHTTP yields
			// a transport error the go-sdk surfaces it here and the caller
			// (external MCP client) sees it as a tool-call failure.
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
