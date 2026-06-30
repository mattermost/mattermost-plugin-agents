# mcp/AGENTS.md

Scoped instructions for the MCP client package. Root rules in `/AGENTS.md` apply; `mcpserver/AGENTS.md` covers server tools and transports.

## Scope

- `mcp/` connects to remote, embedded, and plugin-registered MCP servers.
- `mcpserver/` implements Mattermost MCP tools and transports.
- `external/pluginmcp/` is the SDK for other plugins that register MCP servers.

## Architecture

- MCP config structs live in `config/` and are re-exported here; do not duplicate them.
- `ClientManager` owns per-user `UserClients`, which own per-server `Client` instances.
- Connection kinds include remote HTTP/SSE/streamable, embedded in-memory, and plugin `PluginHTTP`.
- Plugin servers register through `/bridge/v1/mcp/register`.
- Dynamic loading uses `meta_tools.go`, `dynamic_registry.go`, BM25 ranking, and does not bypass agent MCP grants.

## Commands

- Unit tests: `go test -v ./mcp/...`.
- Integration tests (Docker + Mattermost testcontainer): `go test -tags=integration -v ./mcp/...`.
- Repo gates: `make test` and `make check` do not include the integration tag.

## OAuth and policy

- Per-user tokens flow through `OAuthManager`; auth failures surface as LLM tool auth errors.
- DCRP and resource metadata validation live in `dcrp.go`, `resource_metadata_origin.go`, and `www_authenticate.go`.
- Tool filtering combines admin policy, agent allowlists, user provider preferences, and dynamic loading state.
- Runtime tool names use `{serverSlug}__{tool}`; bare names are only safe when unambiguous.

## Adding behavior

- Remote server config changes start in `config/mcp_config.go` and usually need admin UI/API updates.
- New meta-tool or registry behavior belongs in `mcp/`; do not copy implementations from `mcpserver/tools/`.
- Bridge allowlists are documented in `public/bridgeclient/AGENTS.md`.
