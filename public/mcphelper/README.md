# mcphelper

Expose MCP (Model Context Protocol) tools from your Mattermost plugin to the
Agents plugin. This package gives you a small, opinionated wrapper around the
[Anthropic go-sdk for MCP](https://github.com/modelcontextprotocol/go-sdk) that
handles namespacing, inter-plugin authentication, user-ID propagation, and
registration with the Agents plugin.

## Status

Pre-release. The API is frozen for the initial rollout but not yet available
via `go get` as a tagged version — see [Known Limitations](#known-limitations).

## Minimum Mattermost version

**Mattermost 11.3 or newer.**

mcphelper relies on the server-side plumbing for `Plugin.PluginHTTPStream`
(streaming inter-plugin HTTP). That primitive shipped on 2025-11-05 in
Mattermost 11.3. Plugins built against mcphelper will fail to register or
serve tool calls on older servers.

---

## Quick Start

A minimal plugin that exposes three MCP tools. Save each file in a standard
Mattermost plugin scaffold (`mattermost-plugin-starter-template` works).

### `server/plugin.go`

```go
package main

import (
    "net/http"
    "strings"

    "github.com/mattermost/mattermost/server/public/plugin"
    "github.com/mattermost/mattermost/server/public/pluginapi"
    "github.com/pkg/errors"

    "github.com/mattermost/mattermost-plugin-agents/public/mcphelper"
)

const (
    pluginID      = "com.example.plugin-mcp-demo"
    mcpBasePath   = "/mcp"
    mcpServerName = "MCP Demo"
)

type Plugin struct {
    plugin.MattermostPlugin

    client    *pluginapi.Client
    mcpServer *mcphelper.Server
}

func (p *Plugin) OnActivate() error {
    p.client = pluginapi.NewClient(p.API, p.Driver)

    p.mcpServer = mcphelper.NewServer(p.API, mcphelper.PluginMCPServer{
        PluginID: pluginID,
        Name:     mcpServerName,
        Path:     mcpBasePath,
        Version:  "0.0.1",
    })

    p.registerTools()

    if err := p.mcpServer.Register(); err != nil {
        return errors.Wrap(err, "failed to start MCP registration")
    }
    return nil
}

func (p *Plugin) OnDeactivate() error {
    if p.mcpServer == nil {
        return nil
    }
    if err := p.mcpServer.Unregister(); err != nil {
        p.API.LogWarn("MCP unregister failed during deactivate", "err", err.Error())
    }
    return nil
}

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
    if strings.HasPrefix(r.URL.Path, mcpBasePath) {
        p.mcpServer.ServeHTTP(w, r)
        return
    }
    http.NotFound(w, r)
}

func main() {
    plugin.ClientMain(&Plugin{})
}
```

### `server/tools.go`

```go
package main

import (
    "context"
    "fmt"

    "github.com/mattermost/mattermost-plugin-agents/public/mcphelper"
    "github.com/mattermost/mattermost/server/public/model"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type EchoArgs struct {
    Message string `json:"message" jsonschema:"The string to echo back,minLength=1"`
}
type EchoOutput struct {
    Echoed string `json:"echoed" jsonschema:"The same string that was passed in"`
}

type AddTwoNumbersArgs struct {
    A int `json:"a" jsonschema:"First addend"`
    B int `json:"b" jsonschema:"Second addend"`
}
type AddTwoNumbersOutput struct {
    Sum int `json:"sum" jsonschema:"Sum of a and b"`
}

type GetUserDisplayNameArgs struct{}
type GetUserDisplayNameOutput struct {
    UserID      string `json:"user_id" jsonschema:"Mattermost user ID of the caller"`
    Username    string `json:"username" jsonschema:"Username of the caller"`
    DisplayName string `json:"display_name" jsonschema:"Full-name display name of the caller"`
}

func (p *Plugin) registerTools() {
    mcphelper.AddTool(p.mcpServer, &mcp.Tool{
        Name:        "echo",
        Description: "Echo a string back to the caller. Useful for verifying the MCP round-trip.",
    }, p.echoHandler)

    mcphelper.AddTool(p.mcpServer, &mcp.Tool{
        Name:        "add_two_numbers",
        Description: "Return the sum of two integers.",
    }, p.addTwoNumbersHandler)

    mcphelper.AddTool(p.mcpServer, &mcp.Tool{
        Name:        "get_user_display_name",
        Description: "Look up the calling user's display name.",
    }, p.getUserDisplayNameHandler)
}

func (p *Plugin) echoHandler(_ context.Context, _ *mcp.CallToolRequest, in EchoArgs) (*mcp.CallToolResult, EchoOutput, error) {
    return nil, EchoOutput{Echoed: in.Message}, nil
}

func (p *Plugin) addTwoNumbersHandler(_ context.Context, _ *mcp.CallToolRequest, in AddTwoNumbersArgs) (*mcp.CallToolResult, AddTwoNumbersOutput, error) {
    return nil, AddTwoNumbersOutput{Sum: in.A + in.B}, nil
}

func (p *Plugin) getUserDisplayNameHandler(ctx context.Context, _ *mcp.CallToolRequest, _ GetUserDisplayNameArgs) (*mcp.CallToolResult, GetUserDisplayNameOutput, error) {
    userID := mcphelper.GetUserID(ctx)
    if userID == "" {
        return nil, GetUserDisplayNameOutput{}, fmt.Errorf("no Mattermost user ID in tool context (did the request arrive via mcphelper.ServeHTTP?)")
    }

    user, err := p.client.User.Get(userID)
    if err != nil {
        return nil, GetUserDisplayNameOutput{}, fmt.Errorf("failed to fetch user %s: %w", userID, err)
    }

    return nil, GetUserDisplayNameOutput{
        UserID:      user.Id,
        Username:    user.Username,
        DisplayName: user.GetDisplayName(model.ShowFullName),
    }, nil
}
```

That's the entire working plugin. Build it with `make dist`, install it
alongside the Agents plugin, and the three tools appear in the Agents admin
"Tools" tab as `com.example.plugin-mcp-demo__echo`, `...__add_two_numbers`,
and `...__get_user_display_name`.

---

## API Reference

### `PluginMCPServer`

The wire-serialized descriptor this plugin sends to the Agents plugin when
registering. Stored in the Agents plugin's `ClientManager` registry.

```go
type PluginMCPServer struct {
    PluginID string `json:"plugin_id"`         // required; MUST match plugin.json's "id"
    Name     string `json:"name"`              // human-readable; shown in admin UI
    Path     string `json:"path"`              // your plugin's MCP endpoint, e.g. "/mcp"
    Version  string `json:"version,omitempty"` // advertised via go-sdk Implementation.Version; defaults to "0.0.1"
}
```

`PluginID` must match the `id` in your `plugin.json`. The Agents plugin's
bridge handler rejects any registration request where the body's `PluginID`
doesn't match the `Mattermost-Plugin-ID` header (set by the Mattermost server
on inter-plugin RPC). See [Security Model](#security-model).

### `PluginAPI`

The minimal subset of the Mattermost plugin API that mcphelper needs.

```go
type PluginAPI interface {
    PluginHTTP(*http.Request) *http.Response
}
```

Satisfied automatically by `p.API` (the `plugin.MattermostPlugin` embedded
API) and by `pluginapi.NewClient(...).API`. You can also pass a hand-rolled
test double.

### `Server`

Constructed via `NewServer`. Opaque to consumers — only the exposed methods
and the `AddTool` free function are API. Safe for concurrent use after
construction; `AddTool` may be called from any goroutine before the first
`ServeHTTP` call. In practice plugins call `AddTool` from `OnActivate` and
never again.

### `NewServer(pluginAPI PluginAPI, config PluginMCPServer) *Server`

Build a new MCP server. The returned server has no tools registered yet;
call `AddTool` for each tool, wire `ServeHTTP` from your plugin's HTTP
handler, and call `Register()` in `OnActivate`.

### `AddTool[In, Out any](s *Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out])`

Register a typed tool on the server.

```go
mcphelper.AddTool(p.mcpServer, &mcp.Tool{
    Name:        "echo",
    Description: "Echo a string back to the caller.",
}, p.echoHandler)
```

- **`In` and `Out` are type parameters.** The go-sdk introspects them via
  `google/jsonschema-go` to generate the tool's input/output schema. Field
  tags matter: `json:"..."` names the JSON field, and
  `jsonschema:"description,minLength=...,..."` annotates constraints.
- **Namespacing.** `AddTool` prepends `{pluginID}__` to `tool.Name` (using
  a sanitized form of your `PluginID` — see [Namespacing Constraint](#namespacing-constraint)).
  If you already prefixed the name, the prefix is NOT duplicated. This
  prefix is how the Agents plugin attributes tool calls to their source
  plugin.
- **Why a free function, not a method?** Go doesn't permit methods to
  declare type parameters that don't appear on the receiver. The go-sdk's
  own `mcp.AddTool` has the same signature for the same reason.
- **Handler signature.** `mcp.ToolHandlerFor[In, Out]` is
  `func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)`.
  Return `(nil, out, nil)` for a happy path — mcphelper auto-packs `Out`
  into a `CallToolResult`. Return `(customResult, _, nil)` to control the
  `CallToolResult` directly (multi-content replies, explicit `IsError`, etc.).

### `GetUserID(ctx context.Context) string`

Extract the Mattermost user ID of the user whose request is being processed.
Populated by `ServeHTTP` from the `X-Mattermost-UserID` header.

```go
func (p *Plugin) myHandler(ctx context.Context, _ *mcp.CallToolRequest, _ MyArgs) (*mcp.CallToolResult, MyOut, error) {
    userID := mcphelper.GetUserID(ctx)
    if userID == "" {
        return nil, MyOut{}, fmt.Errorf("no Mattermost user ID in tool context")
    }
    // ... use userID to call pluginapi on behalf of the caller
}
```

Returns `""` if no user ID was present. That can only happen in unit tests
that bypass `ServeHTTP`, because the Agents plugin always sets
`X-Mattermost-UserID` on outbound tool calls (and the Agents plugin is the
only caller that can clear the [security gate](#security-model)).

### `(s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)`

The `http.Handler` for this plugin's MCP endpoint. Wire it from your
`Plugin.ServeHTTP` for requests matching `config.Path`:

```go
func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
    if strings.HasPrefix(r.URL.Path, mcpBasePath) {
        p.mcpServer.ServeHTTP(w, r)
        return
    }
    http.NotFound(w, r)
}
```

`ServeHTTP` enforces its own [security gate](#security-model). Do **not**
add a second gate in your outer `ServeHTTP`.

### `(s *Server) Register() error`

**Asynchronous.** Returns `nil` immediately and spawns a background
goroutine that POSTs a registration request to the Agents plugin with
exponential backoff: 1s → 2s → 4s → 8s (cap), up to 15 attempts. Give-up
failures are logged via the standard library `log` package.

Calling from `OnActivate` is safe: `Register` does NOT block on the Agents
plugin being loaded. If the Agents plugin becomes available later, the
retry loop will catch up.

Calling `Register` more than once is permitted but starts a fresh retry
loop; the previous goroutine typically has already exited. If you need to
re-register after a teardown, call `Unregister` first.

### `(s *Server) Unregister() error`

**Synchronous.** Cancels the pending `Register` goroutine (if any), fires
one POST to the Agents plugin's `/bridge/v1/mcp/unregister`, and returns
the HTTP error if any. Called from `OnDeactivate` where a bounded wait
makes sense.

---

## Tool-Count Best Practice

**Target ~10 tools per plugin.** Batch semantically related operations
rather than shipping one tool per method.

LLM context windows are precious. Each tool registered with the Agents
plugin contributes:

- The tool name and description to the model's system prompt (~1-10 tokens).
- The full input schema, serialized as JSON (~20-200 tokens per field).
- A slot in the model's attention budget for "which tool do I call?"

A plugin shipping 100 tiny tools will burn 5-10KB of context before the
model sees the user's message. A plugin shipping 10 well-designed tools
with union-typed arguments costs an order of magnitude less.

**Good:** one `search_documents` tool that takes a `type` enum and a
`query` string.
**Bad:** `search_documents_by_title`, `search_documents_by_body`,
`search_documents_by_author`, `search_documents_by_tag`.

**Good:** one `create_or_update_entity` tool with a discriminated-union
`operation` field.
**Bad:** `create_entity`, `update_entity`, `delete_entity`,
`upsert_entity`, all with overlapping schemas.

If your plugin's surface really needs more than ~20 tools, consider
splitting into multiple plugins (each with its own `PluginID`) so admins
can toggle them independently.

---

## Security Model

mcphelper's security model has two cooperating gates, both enforced by the
Mattermost server + the Agents plugin + mcphelper itself.

### Why the `Mattermost-Plugin-ID` check exists

`ServeHTTP` rejects any request that doesn't carry
`Mattermost-Plugin-ID: mattermost-ai`. This gate closes a trust gap in
`X-Mattermost-UserID`:

- Mattermost's server DOES strip the `Mattermost-Plugin-ID` header on
  external (browser / API client) requests before routing — see the server's
  plugin_requests.go.
- Mattermost's server does NOT strip `X-Mattermost-UserID` on external
  requests.

So if mcphelper trusted `X-Mattermost-UserID` alone, any authenticated API
client could set `X-Mattermost-UserID: <any-user>` in a request to
`/plugins/<your-plugin>/mcp` and impersonate that user inside your tool
handlers. The `Mattermost-Plugin-ID` gate prevents this: any request past
the gate arrived via genuine inter-plugin RPC (the only path that sets
`Mattermost-Plugin-ID`), so the co-arriving `X-Mattermost-UserID` is
trustworthy.

### User-ID propagation chain (end-to-end)

Here is the exact path an authenticated user ID takes from a browser
session to your tool handler:

1. **Browser sends a request** to the Mattermost server (e.g., a chat
   message to the agents bot) with the user's session token in the
   `Cookie` or `Authorization` header.
2. **Mattermost server validates the session**, resolves the user ID, and
   sets `Mattermost-User-Id: <user>` on the inbound request before
   dispatching to the Agents plugin's `ServeHTTP`.
3. **Agents plugin's LLM conversation path** reads `Mattermost-User-Id`
   from the request context and uses it as the caller identity for MCP
   tool invocations.
4. **Agents plugin's `ClientManager`** selects the plugin-server client
   for your tool and issues an MCP `CallTool` over a
   `PluginHTTPRoundTripper`.
5. **The RoundTripper layers a `headerTransport`** that injects
   `X-Mattermost-UserID: <user>` on every outbound request.
6. **`Plugin.PluginHTTP`** on the Agents plugin routes the request to
   your plugin. During this hop, Mattermost **adds**
   `Mattermost-Plugin-ID: mattermost-ai` (the Agents plugin's ID is the
   source) and **preserves** `X-Mattermost-UserID`.
7. **Your plugin's `ServeHTTP`** delegates to `mcphelper.Server.ServeHTTP`.
8. **mcphelper's security gate** confirms
   `Mattermost-Plugin-ID == mattermost-ai` (else 403).
9. **mcphelper extracts `X-Mattermost-UserID`** and injects it into the
   request context via an unexported key.
10. **Your tool handler** calls `mcphelper.GetUserID(ctx)` and gets the
    original authenticated user ID.

### Threats this defends against

| Threat | Defense |
|---|---|
| External API client sets fake `X-Mattermost-UserID` | `Mattermost-Plugin-ID` gate (strip on external requests is server-enforced) |
| A non-Agents plugin calls your MCP endpoint | Gate value check: `Mattermost-Plugin-ID == mattermost-ai` |
| A malicious plugin registers a fake MCP server claiming to be you | Agents plugin rejects `register` if body `PluginID != Mattermost-Plugin-ID` header |
| Your handler accidentally trusts the `X-Mattermost-UserID` header directly | Use `mcphelper.GetUserID(ctx)` — it only reads from the context key set **after** the security gate |

---

## Namespacing Constraint

Every tool registered via `mcphelper.AddTool` is namespaced with
`{pluginID}__{toolName}` so the Agents plugin can attribute calls. The
double-underscore (`__`) is the separator.

**Your `PluginID` must not contain `__`.** A plugin ID like
`com.example__plugin-mcp-demo` would split incorrectly in the agents
plugin's server-origin key (`"plugin://com.example__plugin-mcp-demo"`)
and in any parser that splits tool names on `__`. mcphelper does NOT
currently reject `__` in the sanitizer — it is a caller-side constraint
and using `__` will produce undefined behavior. Pick a plugin ID that
reads as a normal reverse-DNS name: `com.example.plugin-mcp-demo`.

**Silent sanitization of other characters.** Characters outside
`[A-Za-z0-9_\-.]` are silently replaced with `_` in the **tool-name prefix
only**. Your `PluginID` is kept verbatim in every other context (registry
key, wire JSON, PluginHTTP route, header, server-origin string,
filter-config map). Real Mattermost plugin IDs
(`com.mattermost.plugin-foo`) pass through unchanged.

Examples:

| Plugin ID | Tool-name prefix |
|---|---|
| `com.mattermost.plugin-agents` | `com.mattermost.plugin-agents__` |
| `my-plugin` | `my-plugin__` |
| `com.example plugin` | `com.example_plugin__` (space → `_`) |
| `some@weird/id` | `some_weird_id__` |
| `plugin__with__dunders` | **undefined** — do not use |

---

## Known Limitations

- **Not yet `go get`-able.** Until the Agents plugin cuts a release tag
  with `public/mcphelper/` exported, consumers must point their `go.mod`
  at a local checkout of the Agents plugin worktree via a `replace`
  directive. Example:

  ```
  replace github.com/mattermost/mattermost-plugin-agents => ../mattermost-plugin-agents
  ```

  When a release tag lands, drop the `replace` and `go get` directly.

- **No per-tool admin filtering UI for plugin-registered tools.** The
  Agents plugin's admin "Tools" tab shows an enable/disable toggle at the
  server level for plugin-registered servers, but per-tool policy controls
  (auto-run-in-DM, require confirmation, etc.) are remote-server-only in
  the initial release. If you need per-tool control, either (a) split the
  tool onto a separate plugin, or (b) bake the policy into your handler.
  A future release will unify the per-tool UI across remote and
  plugin-registered servers.

- **Stub `.gitignore` footgun.** The standard
  `mattermost-plugin-starter-template` `.gitignore` excludes
  `server/manifest.go`, which regenerates on every `make apply` from
  `plugin.json`. Don't try to commit it — the generated file causes merge
  noise. This is a starter-template convention, not an mcphelper concern,
  but worth knowing if you're scaffolding a fresh plugin.

---

## Troubleshooting

### "My tool doesn't appear in the Agents admin Tools tab"

1. Check the Agents plugin's server log for a line like
   `Connected to plugin MCP server` with your plugin ID. If missing:
   - Is your plugin enabled?
   - Did you call `mcphelper.Server.Register()` in `OnActivate`?
   - Is the Agents plugin loaded? mcphelper's registration retries for
     ~15 attempts with exponential backoff before giving up — check the
     log for `mcphelper: registration with Agents plugin gave up after N
     attempts`.
2. Check the Agents plugin's admin Tools tab. Your entry should appear
   with `ServerType: "plugin"`. If it appears with `Error: ...`, the
   Agents plugin reached your plugin but couldn't list tools — usually a
   schema-generation failure in `AddTool`.
3. Check the tool name. It will be prefixed with
   `{your-sanitized-plugin-id}__`.

### "mcphelper: registration keeps retrying"

The Agents plugin isn't reachable. Common causes:

- Agents plugin is disabled.
- Agents plugin is in a crash loop (check its log).
- You set `PluginID` to something that doesn't match your `plugin.json`
  `id` — the Agents plugin's register handler returns 403, which
  mcphelper treats as non-retriable and logs permanently. Check the log
  for `registration with Agents plugin failed permanently`.

### "mcphelper.GetUserID(ctx) returns empty string"

- Did the request actually arrive via `mcphelper.Server.ServeHTTP`? If
  you're invoking your tool handler directly in a unit test, bypass
  mcphelper and pass a context built with your own value.
- Is your outer `ServeHTTP` delegating correctly? Don't add a second
  security gate — the Agents plugin's outbound headers are already
  correct.

### "Local `golangci-lint` fails with 'Go language version (go1.24) used to build golangci-lint is lower than the targeted Go version (go1.26.1)'"

Your local `golangci-lint` binary was built against an older Go toolchain
than your plugin's `go.mod` declares. Two workarounds:

1. Upgrade your local `golangci-lint` to a version built against Go 1.26
   or newer: `brew upgrade golangci-lint` or follow the install docs.
2. Skip local lint and rely on CI. Agents plugin CI runs the correct
   toolchain.

### "`go mod tidy` pulls go-sdk v1.5.0 even though my plugin doesn't need it"

This is Go's Minimum Version Selection (MVS) at work: if any dependency
in your build graph requires a newer go-sdk than mcphelper pins, MVS picks
the higher version. mcphelper's public API is API-stable across the
v1.4.x → v1.5.x window, so this is safe. Your plugin's tests are the
authority — if they pass, the version is fine.

If you want a hard pin, add an `exclude` or `replace` directive in your
`go.mod` that forces the exact go-sdk version. This is rarely worth the
maintenance cost.

### "`go.mod` `replace` directive breaks when I move the worktree"

`replace` directives use **relative paths from the current `go.mod`'s
directory**. If you move your plugin source tree, you must update the
`replace` line to point at the new location of the Agents plugin
checkout. Prefer a stable parent directory (e.g., `~/workspace/`) so the
relative path is stable. When the Agents plugin cuts a release tag,
remove the `replace` entirely.

### "I get `PluginHTTP returned nil response (Agents plugin likely not loaded)` in my log"

Agents plugin is not active at the moment mcphelper tries to register.
This is retriable — mcphelper backs off and retries. If it persists,
check the Agents plugin's activation state.
