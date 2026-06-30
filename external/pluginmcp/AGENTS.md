# external/pluginmcp/AGENTS.md

Scoped instructions for the public cross-plugin MCP registration library. Root rules in `/AGENTS.md` still apply.

## Scope

- This package is for other Mattermost plugins that expose MCP tools to Agents.
- Human API quickstart and examples live in `README.md`; keep this file focused on repo-maintenance contracts.

## Wire contract

- Registration payloads must stay in sync with `api/api_bridge_mcp.go`, `mcp/client_manager.go`, `config/mcp_config.go`, and `mcpserver/proxy_tools.go`.
- The trusted identity is the `Mattermost-Plugin-ID` header set by Mattermost, not any JSON `plugin_id` field.
- Requires Mattermost support for `PluginHTTPStream`; keep compatibility notes in `README.md`.
- `ExposeExternal` affects the external aggregate server and must trigger rebuilds in Agents.

## Changing the library

1. Update this package and its tests.
2. Update Agents-side bridge handlers and MCP client registry code in the same change.
3. Update `README.md` for public API behavior changes.
4. Check admin webapp tool configuration if wire fields change.

## Commands

- Unit tests: `go test -v ./external/pluginmcp/...`
- Public API sweep: `go test -v ./public/... ./external/pluginmcp/...`

## Pointers

- In-plugin MCP client registry: `/mcp/AGENTS.md`.
- External aggregate server/proxy behavior: `/mcpserver/AGENTS.md`.
