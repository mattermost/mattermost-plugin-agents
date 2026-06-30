---
description: Helper library for other Mattermost plugins to expose MCP tools to the Agents plugin.
tags: [mcp, plugin, integration, external]
---

# external/pluginmcp/AGENTS.md

Helper for **other** Mattermost plugins to register MCP tools with the Agents plugin over inter-plugin HTTP. Read `README.md` here for the full integration guide; this file covers the durable gotchas.

- Same root Go module (no separate `go.mod`); import `.../external/pluginmcp`. No release tag yet — consumers use a `replace` directive.
- Requires Mattermost ≥ 11.3 (`PluginHTTPStream`). `Register()` POSTs to `/bridge/v1/mcp/register` with exponential backoff.
- Trust identity from the `Mattermost-Plugin-ID` header (not a JSON field); `GetUserID(ctx)` is only trustworthy inside `ServeHTTP` after that check.
- `AddTool` namespaces as `{sanitizedPluginID}__{toolName}` to satisfy Bifrost's name regex — a `PluginID` must not contain `__`.
- `ExposeExternal` (plugin-controlled) is separate from admin `Enabled` / per-tool policy (admin-controlled). Keep tool counts modest (~10/plugin).
