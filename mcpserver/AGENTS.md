---
description: Mattermost-native MCP servers and tool handlers — server types, config-vs-runtime, AccessMode, adding tools.
tags: [mcp, server, tools, access-mode]
---

# mcpserver/AGENTS.md

Scoped instructions for the `mcpserver/` package. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here. The MCP **client** side is `mcp/`; standalone-binary usage is in `mcpserver/README.md`.

## Server types

Four deployment shapes share `mcpserver/tools` but differ in transport, auth, and `AccessMode`:

| Constructor | Use | Transport | AccessMode |
| --- | --- | --- | --- |
| `NewInMemoryServer` | embedded in the plugin (production) | in-memory pairs | **Remote** |
| `NewPluginMCPHandlers` | plugin HTTP endpoint (external aggregate) | streamable HTTP | Remote |
| `NewHTTPServer` / `NewStdioServer` | standalone dev binary (`make mcp-server`) | HTTP / stdio | Remote / Local |

The embedded server is **`AccessModeRemote` despite being in-process** — fields tagged `access:"local"` (e.g. local file-path attachments in `create_post`) are stripped from the schema, so they're unavailable in embedded/production MCP.

## Architecture

### Configuration vs runtime services

- Config structs are declarative — strings, ints, bools only.
- Never put runtime service instances inside a config struct.
- Pass runtime services directly as parameters to constructors.

### Optional services (search + files)

- **`InMemoryServer`** (embedded) takes `tools.SemanticSearchService` and `tools.FileContentService` directly. The plugin passes `*search.Search` (which implements `SemanticSearchService`) and `*files.Service`.
- **HTTP / Stdio / PluginHandlers** (external servers) build their own `HTTPSemanticSearchService` / `HTTPFileContentService` internally; those call back to the plugin's search/file HTTP endpoints.

### Type sharing

- Do not duplicate types from the `search` package inside `mcpserver/tools`. The `SemanticSearchService` interface uses `search.Options` and `search.RAGResult` directly.
- HTTP serialization DTOs (e.g., `httpSearchRequest`, `httpSearchResult` in `search_http.go`) are intentionally separate from domain types and stay in their respective files.
- If you only need a subset of fields, accept the full type and ignore the unused fields rather than introducing a parallel struct.

## Adding a tool

Add an `MCPTool` entry to the appropriate `get*Tools()` slice in `mcpserver/tools/`, implement the resolver on `MattermostToolProvider`, and **format any Mattermost entity output through the `format/` package** (root rule — never `fmt.Sprintf` model types). Registration flows through `ProvideTools`. Automation tools are always registered but stripped from `tools/list` by `automationToolFilterMiddleware` when the channel-automation plugin is absent.

## Adding a new optional capability

1. Define the interface in `tools/`, reusing types from their source package.
2. For embedded servers: add the parameter to `NewInMemoryServer`.
3. For external servers: add a plugin HTTP endpoint plus an HTTP client implementation that calls it (mirror the search/files pattern).

## Other notes

- Requires the Mattermost `SiteURL` (becomes `MMServerURL`); embedded server creation fails without it (logged, plugin continues without embedded MCP).
- On the external aggregate, native tools register first; a plugin proxy tool with a duplicate bare name is skipped with an error log (first-registered wins).
- Build/run: `make mcp-server` (binary, dev only), `make mcp-evals` (eval tests).
