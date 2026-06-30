---
description: Per-user MCP client — transports, tool namespacing, caching, OAuth, dynamic loading.
tags: [mcp, client, oauth, tools, cache]
---

# mcp/AGENTS.md

Per-user MCP **client** layer: connects to remote HTTP servers, the plugin-embedded in-memory server, and plugin-registered servers; discovers and namespaces tools; applies admin policy. Structure: `ClientManager` → per-user `UserClients` → per-server `Client`.

- **Transports (remote):** try `StreamableClientTransport` (2025-03-26 spec), fall back to `SSEClientTransport` (2024-11-05). Plugin servers use streamable HTTP over a synthetic `http://plugin{path}` with `PluginHTTPRoundTripper`; the embedded server uses `InMemoryTransport`. Remote/plugin calls inject `X-Mattermost-UserID`.
- **Tool namespacing, not bare-name merge:** runtime names are `{serverSlug}__{bareTool}` via `llm.NamespaceMCPToolName` (separator `__`); the embedded slug is `mattermost`. A rare namespaced collision skips the second tool with a warning. ("First-registered wins" applies to the *external aggregate* in `mcpserver/plugin_handlers.go`, not to cross-server discovery here.)
- **Shared tools cache** (`tools_cache.go`, KV, 8-day TTL) is used **only** for servers without static OAuth creds; plugin clients never use it. Cached remote connects use `context.WithoutCancel` so request cancellation can't poison a cached client.
- **Dynamic loading:** when enabled, `search_tools` / `load_tool` meta-tools (`meta_tools.go`) are backed by the BM25 `dynamic_registry.go`. Empty embedded `ToolConfigs` falls back to `SeedVettedToolConfigs` (`vetted_tools.go`).
- **Gotchas:** an empty tool list on connect is a hard error; `CallToolWithMetadata` treats `result.IsError=true` as a Go error; OAuth-needed detection partly relies on string matching (the go-sdk drops error chains). Integration tests are behind `//go:build integration`.
- OTel: the only in-package span is `"mcp call tool"`; `"resolve tool"` spans live in `llm/` and `toolrunner/`.

Tests: `go test ./mcp/...` (add `-tags=integration` for the integration suite).
