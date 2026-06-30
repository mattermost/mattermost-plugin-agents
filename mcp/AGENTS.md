# mcp/AGENTS.md

Scoped instructions for the in-plugin MCP client. Root rules in `/AGENTS.md` still apply.

## Scope

- This package manages MCP clients, OAuth, tool discovery, dynamic tool loading, vetted tool policies, before-hook state, and plugin-registered MCP servers.
- MCP tool implementations live in `/mcpserver/`; do not add Mattermost MCP resolvers here.

## Architecture

- `ClientManager` owns per-user `UserClients`, idle cleanup, plugin registry, embedded client wiring, and remote server config.
- Tool names are namespaced with `llm.NamespaceMCPToolName`; collisions skip later tools with a warning.
- Config types are aliases from `config/`; avoid circular imports.
- Plugin servers register through bridge APIs, then execute through `PluginHTTPRoundTripper` with `X-Mattermost-UserID`.
- Embedded server key is `embedded://mattermost`; session handling goes through `EnsureMCPSessionID`.
- OAuth servers use `OAuthManager`; non-OAuth servers can use the shared `ToolsCache`.
- Dynamic loading exposes `search_tools` and `load_tool` from `meta_tools.go`.
- Vetted host defaults live in `vetted_tools.go`.

## Gotchas

- Do not hold `pluginServersMu` across plugin HTTP round trips.
- `DiscoverPluginServerTools` bypasses user cache and is for admin probes.
- OAuth `ListTools` before token acquisition must preserve auth UI behavior by returning cached initial errors when applicable.
- Duplicate `MCPToolConfig` entries use last-match-wins behavior.
- MCP meta-tools auto-run without approval.
- DCRP and resource metadata origin checks live in `resource_metadata_origin.go`.

## Commands

- Unit tests: `go test -v ./mcp/...`
- Race tests: `go test -race ./mcp/...`
- Integration tests, Docker-backed: `go test -tags=integration -race -v ./mcp/...`
- Dynamic loading focus: `go test -v ./mcp/ -run TestDynamicMCP`

## Pointers

- MCP server/tool implementations: `/mcpserver/AGENTS.md`.
- External plugin registration library: `/external/pluginmcp/AGENTS.md`.
- Tool loop behavior for unloaded tools: `/toolrunner/`.
