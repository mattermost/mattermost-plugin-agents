---
description: MCP CLIENT — per-user connections to remote/embedded/plugin MCP servers, tool namespacing, OAuth, dynamic tool loading, policy filtering.
tags: [mcp, client, tools, oauth, namespacing]
---

# mcp/AGENTS.md

The MCP **client** (outbound). The MCP **servers** and native Mattermost tools live in `mcpserver/` (see `mcpserver/AGENTS.md`). Root `/AGENTS.md` still applies.

## Key files

- `mcp/client_manager.go` — `ClientManager`, `GetToolsForUser`, `ReInit`, `filterToolsByConfig`.
- `mcp/client.go` — `Client`, `EmbeddedMCPServer` interface, `EmbeddedClientKey`.
- `mcp/user_clients.go` — per-user connections; tool namespacing + collision handling.
- `mcp/dynamic_registry.go`, `mcp/meta_tools.go` — BM25 tool search, `search_tools`/`load_tool` lazy loading.
- `mcp/tool_policy.go`, `mcp/vetted_tools.go` — admin policy + vetted-host seeds.
- `mcp/oauth_*.go`, `mcp/tools_cache.go`, `mcp/plugin_roundtripper.go` — OAuth, shared cache, plugin routing.

## Conventions & gotchas

- **Namespacing:** remote/plugin tools become `llm.NamespaceMCPToolName(serverSlug, bareName)` (`{slug}__{name}`). Allowlist keys use `serverOrigin\x00toolName`.
- **Collision behavior is context-dependent** (don't assume one global rule): user-client aggregation = **first wins, second skipped with a `Warn`**; dynamic registry = **last duplicate wins**; plugin-proxy aggregation (in `mcpserver/plugin_handlers.go`) = native tools win, duplicate proxy names skipped with an `Error` log.
- **Embedded transport** is in-memory (`mcp.InMemoryTransport`), keyed by `EmbeddedClientKey`; session via `ClientManager.EnsureMCPSessionID`.
- **Tool policy:** `filterToolsByConfig` orders by configured server order, drops disabled tools, and defaults unconfigured tools to enabled.
- **Shared tools cache** is used only when a server has no static OAuth creds.
- **Meta-tools** (`search_tools`/`load_tool`) are not MCP protocol tools; they gate lazy loading of large catalogs.

Span `"mcp call tool"` is created in `mcp/client.go` (see `telemetry/AGENTS.md`).
