# pluginmcp

Expose MCP (Model Context Protocol) tools from a Mattermost plugin to the
Agents plugin. `pluginmcp` wraps the [go-sdk MCP server](https://github.com/modelcontextprotocol/go-sdk)
with the namespacing, inter-plugin auth, user-ID propagation, and async
registration that Agents-plugin tool calls require.

Requires Mattermost 11.3 or newer (uses `Plugin.PluginHTTPStream`).

## Install

Until the Agents plugin cuts a release tag with `external/pluginmcp/`
exported, point your `go.mod` at a local checkout:

```go
replace github.com/mattermost/mattermost-plugin-agents/v2 => ../mattermost-plugin-agents
```

## Quickstart

A complete plugin that exposes one tool. The same shape works for any
number of tools, call `AddTool` once per tool from `OnActivate`.

```go
// plugin/plugin.go
package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	pluginID    = "com.example.plugin-mcp-demo"
	mcpBasePath = "/mcp"
)

type Plugin struct {
	plugin.MattermostPlugin
	client    *pluginapi.Client
	mcpServer *pluginmcp.Server
}

type WhoAmIArgs struct{}
type WhoAmIOutput struct {
	Username string `json:"username" jsonschema:"Username of the caller"`
}

func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)

	p.mcpServer = pluginmcp.NewServer(p.API, pluginmcp.Config{
		PluginID: pluginID,
		Name:     "MCP Demo",
		Path:     mcpBasePath,
		Version:  "0.0.1",
	})

	pluginmcp.AddTool(p.mcpServer, &mcp.Tool{
		Name:        "whoami",
		Description: "Return the calling user's username.",
	}, p.whoami)

	return p.mcpServer.Register()
}

func (p *Plugin) OnDeactivate() error {
	if p.mcpServer == nil {
		return nil
	}
	return p.mcpServer.Unregister()
}

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, mcpBasePath) {
		p.mcpServer.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func (p *Plugin) whoami(ctx context.Context, _ *mcp.CallToolRequest, _ WhoAmIArgs) (*mcp.CallToolResult, WhoAmIOutput, error) {
	userID := pluginmcp.GetUserID(ctx)
	if userID == "" {
		return nil, WhoAmIOutput{}, fmt.Errorf("no Mattermost user ID in context")
	}
	user, err := p.client.User.Get(userID)
	if err != nil {
		return nil, WhoAmIOutput{}, fmt.Errorf("get user %s: %w", userID, err)
	}
	return nil, WhoAmIOutput{Username: user.Username}, nil
}

func main() {
	plugin.ClientMain(&Plugin{})
}
```

The tool appears in the Agents admin "Tools" tab as
`com.example.plugin-mcp-demo__whoami`.

## API reference

```go
type Config struct {
	PluginID string // required; must equal plugin.json "id"
	Name     string // human-readable; shown in admin UI
	Path     string // your plugin's MCP endpoint, e.g. "/mcp"
	// ExposeExternal: if true, your tools may be included on the Agents plugin's
	// external MCP aggregate when the server is also Enabled in admin (see below).
	ExposeExternal bool
	Version        string // optional; defaults to "0.0.1"
}

type PluginAPI interface {
	PluginHTTP(*http.Request) *http.Response
}
```

- `NewServer(api PluginAPI, cfg Config) *Server`: construct a
  server. `p.API` satisfies `PluginAPI`.
- `AddTool[In, Out any](s *Server, tool *mcp.Tool, handler ...)`: register
  a typed tool. `In`/`Out` are introspected by the go-sdk to build the
  tool schema. Free function (not a method) because Go doesn't allow
  type parameters on methods.
- `(*Server).ServeHTTP(w, r)`: `http.Handler` for the MCP endpoint. Wire
  it from your plugin's `ServeHTTP` for requests under `cfg.Path`.
- `(*Server).Register() error`: start async registration with the
  Agents plugin. Returns `nil` immediately; retries happen in a
  goroutine.
- `(*Server).Unregister() error`: synchronously cancel pending retries
  and POST one unregister request. Call from `OnDeactivate`.
- `GetUserID(ctx context.Context) string`: read the user ID extracted
  by `ServeHTTP` from `X-Mattermost-UserID`. Returns `""` if missing.

Handler signature for `AddTool` is the go-sdk's
`mcp.ToolHandlerFor[In, Out]`:

```go
func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)
```

Return `(nil, out, nil)` and the helper packs `out` into a
`CallToolResult`. Return a non-nil `*mcp.CallToolResult` to control the
response yourself (multi-content replies, `IsError`, etc.).

Per-field schema annotations come from `json:` and `jsonschema:` tags:

```go
type EchoArgs struct {
	Message string `json:"message" jsonschema:"The string to echo back,minLength=1"`
}
```

## How it works

`Register` POSTs `cfg` to the Agents plugin's
`/bridge/v1/mcp/register` with backoff (1s -> 2s -> 4s -> 8s, max 15
attempts) until the Agents plugin responds 200. The Agents plugin then
issues MCP `tools/list` and `tools/call` requests back through
`PluginHTTP` to your plugin's `ServeHTTP`, which delegates to the go-sdk
streamable HTTP handler in stateless / JSON-response mode (PluginHTTP
buffers the full response, so streaming SSE is not used).

For registration and unregister auth, the Agents plugin treats the trusted
`Mattermost-Plugin-ID` header added by Mattermost inter-plugin RPC as the
canonical plugin identity. The JSON `plugin_id` field is still sent for
compatibility, but it is not trusted for identity.

User-ID flow per call: browser -> Mattermost server (sets
`Mattermost-User-Id`) -> Agents plugin -> `PluginHTTP` (Mattermost adds
`Mattermost-Plugin-ID: mattermost-ai`, Agents adds
`X-Mattermost-UserID`) -> your `ServeHTTP` -> `pluginmcp` checks the
plugin-ID header and stashes the user ID in the request context ->
your handler reads it via `GetUserID`.

**`ExposeExternal` vs admin `Enabled` / tool policy.** Each registration POST
sends `expose_external` from your `Config`, and that plugin-provided
value controls whether the server is eligible for the external MCP server.
Admins still control the server's `Enabled` state in the Agents system
console, and per-tool policy still applies there as well. `Enabled` and per-tool
settings are preserved across your plugin's re-registration, while
`ExposeExternal` continues to come from the plugin registration payload.

## Constraints and gotchas

**Tool-name sanitization (Bifrost / Anthropic).** `AddTool` prepends
`{pluginID}__` to `tool.Name`, replacing any character outside
`[A-Za-z0-9_-]` with `_` so the final name matches Bifrost's
`^[a-zA-Z0-9_-]{1,128}$`. Sanitization applies only to the
LLM-facing tool-name prefix; routing, registry keys, and the wire
`PluginID` are kept verbatim. If `tool.Name` already starts with the
prefix it is not duplicated.

**`PluginID` must not contain `__`.** The double-underscore is the
namespace separator. `pluginmcp` does not reject it, but a plugin ID
containing `__` parses ambiguously on the Agents side. Use a normal
reverse-DNS ID like `com.example.plugin-foo`.

**`GetUserID` is trustworthy only inside a `ServeHTTP` request.**
`ServeHTTP` rejects requests without `Mattermost-Plugin-ID:
mattermost-ai` (Mattermost strips that header on external requests, so
only inter-plugin RPC sees it). The user ID is then read from
`X-Mattermost-UserID` and stored under an unexported context key.
External callers can't inject one. Don't add a second auth gate in
your outer `ServeHTTP`, and don't read `X-Mattermost-UserID` directly
from headers in handlers; always go through `GetUserID`.

**Registration is one-shot per `OnActivate`.** The retry goroutine
exits on success or after 15 attempts. If the Agents plugin restarts
later, the in-memory registration is lost. The Agents plugin restores
admin-persisted entries on its own restart, but a never-saved
registration only comes back when your plugin is re-activated. Permanent
non-retriable errors (4xx other than 404/429) log
`registration with Agents plugin failed permanently` and stop.

**Tool-count budget.** Each tool costs ~20-200 schema tokens in every
LLM request. Aim for ~10 tools per plugin, with union-typed args, over
many narrow tools.

**Per-tool admin policy.** Admins configure per-tool policy
(enabled, auto-run-in-DM, require-confirmation) for plugin-registered
tools from the system console Tools tab, alongside the server-level
enable/disable toggle. Policy applies in both the user-facing tool flow
and (when `ExposeExternal` is true) the external aggregated MCP
endpoint.

## Troubleshooting

- *Tool doesn't appear in the admin Tools tab.* Look for `Connected to
  plugin MCP server <pluginID>` in the Agents plugin log; absence
  indicates `Register()` was never called or kept failing. The retry
  loop logs `registration with Agents plugin gave up after N attempts`
  on terminal failure and `failed permanently` on a 4xx.
- *`GetUserID` returns `""`.* Either the request didn't go through
  `pluginmcp.Server.ServeHTTP` (typical in unit tests, inject a
  context yourself) or your outer `ServeHTTP` isn't routing to it.
- *Registration keeps retrying.* Agents plugin disabled, in a crash
  loop, or `cfg.PluginID` doesn't match `plugin.json`'s `id` (the
  Agents plugin returns 403 in that case, which is non-retriable).
