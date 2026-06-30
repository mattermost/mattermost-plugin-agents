---
description: MCP SERVER — native Mattermost tools over in-memory/HTTP/stdio/plugin transports; config-vs-runtime service injection.
tags: [mcp, server, tools, transports, search-service]
---

# mcpserver/AGENTS.md

The MCP **server** (inbound): native Mattermost tools (posts, channels, search, files, agents, automations) over in-memory, HTTP, stdio, and plugin-HTTP transports. The MCP **client** is `mcp/` (see `mcp/AGENTS.md`). Root `/AGENTS.md` still applies; only deviations live here.

## Architecture

### Config vs runtime services

- `BaseConfig`/`InMemoryConfig`/`HTTPConfig`/`StdioConfig` are declarative — strings, ints, bools only.
- Never put runtime service instances in a config struct. Pass them as constructor parameters.

### Server types and injected services

- **`NewInMemoryServer`** (embedded in the plugin) takes `searchService tools.SemanticSearchService` **and** `fileContentService tools.FileContentService` directly. The plugin passes `*search.Search` (implements `SemanticSearchService`). It uses `AccessModeRemote` even though transport is in-memory.
- **HTTP / Stdio / `PluginMCPHandlers`** (external) build their own `HTTPSemanticSearchService` / `HTTPFileContentService` that call back to `{MMServerURL}/plugins/mattermost-ai/api/v1/search/raw`. Stdio can accept injected services instead, and uses `AccessModeLocal`.
- HTTP bind (`0.0.0.0`) requires `SiteURL`; the embedded server also requires `SiteURL`.

### Type sharing

- Don't duplicate `search` types in `mcpserver/tools`. The `SemanticSearchService` interface uses `search.Options` and `search.RAGResult` directly.
- HTTP serialization DTOs (`httpSearchRequest`, etc. in `search_http.go`) are intentionally separate from domain types.
- Need only a subset of fields? Accept the full type and ignore the extras rather than adding a parallel struct.

## Adding a new optional capability

1. Define the interface in `tools/`, reusing types from their source package (as `SemanticSearchService` and `FileContentService` do).
2. Embedded servers: add the parameter to `NewInMemoryServer`.
3. External servers: add a plugin HTTP endpoint plus an HTTP client implementation that calls it.

## Gotchas

- `search_posts` uses the semantic path via `SemanticSearchService`; the keyword path always uses the Mattermost `SearchPosts` API. With semantic disabled, the schema is keyword-only.
- Automation tools are always registered but filtered out of `tools/list` via middleware when the channel-automation plugin is absent.
- Tool output formatting goes through `format/`.
