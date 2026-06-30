# mcp/AGENTS.md

Scoped instructions for the MCP client/runtime package. Root rules in `/AGENTS.md` still apply.

## Boundary

- This package manages MCP clients, OAuth, tool policies, plugin-registered servers, meta-tools, and per-user isolation.
- Mattermost MCP server transports and built-in tools live in `mcpserver/`.

## Architecture

- Client hierarchy is `ClientManager` -> `UserClients` -> `Client`.
- Tool sources include embedded Mattermost (`embedded://mattermost`), remote HTTP MCP servers, and plugin-registered MCP servers (`plugin://<id>`).
- OAuth is implemented per user. Respect discovery/cache invalidation when changing list-tools behavior.
- Tool names are namespaced by server slug; collisions after namespacing are skipped with a warning.
- Admin tool policy filtering is applied before tools reach agents.
- Dynamic loading uses meta-tools such as `search_tools` and `load_tool`.
- Plugin HTTP routing uses the plugin round-tripper and `X-Mattermost-UserID`.

## Commands

- Unit tests: `go test -v ./mcp/...`
- Integration tests with Docker: `go test -tags=integration -v ./mcp/...`

## Gotchas

- Do not register Mattermost built-in tools here; add those in `mcpserver/tools/`.
- Before-hook callback keys are short-lived KV entries used by embedded tool callbacks.
- `make test` does not run `-tags=integration` tests.
- Tool policy changes usually need webapp admin UI and docs updates.
