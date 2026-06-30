# mcpserver/AGENTS.md

Scoped instructions for the `mcpserver/` package. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

## Architecture

### Configuration vs runtime services

- Config structs are declarative — strings, ints, bools only.
- Never put runtime service instances inside a config struct.
- Pass runtime services directly as parameters to constructors.

### Server types and optional services

- `NewInMemoryServer` is embedded in the plugin. It accepts optional `tools.SemanticSearchService` and `tools.FileContentService` instances directly and uses `AccessModeRemote`.
- `NewStdioServer` is the local binary. It uses `AccessModeLocal`, validates a PAT at startup, and defaults optional services to HTTP callbacks when not provided.
- `NewHTTPServer` is the standalone streamable HTTP server. It builds HTTP search/file services and requires a valid `SiteURL` when binding externally.
- `NewPluginMCPHandlers` powers the production plugin MCP endpoint and external plugin aggregate. Rebuild it with `RebuildExternalServer()` after plugin registration changes.
- External search callbacks use `/api/v1/search/raw`; external file callbacks use `/api/v1/files/content`.

### Type sharing

- Do not duplicate types from the `search` package inside `mcpserver/tools`. The `SemanticSearchService` interface uses `search.Options` and `search.RAGResult` directly.
- HTTP serialization DTOs (e.g., `httpSearchRequest`, `httpSearchResult` in `search_http.go`) are intentionally separate from domain types and stay in their respective files.
- If you only need a subset of fields, accept the full type and ignore the unused fields rather than introducing a parallel struct.

### Access mode

- Struct fields tagged `access:"local"` are stripped from remote schemas by `NewJSONSchemaForAccessMode`.
- Local-only attachment inputs are for stdio file reads from `mcpserver/data/`.
- Embedded/in-plugin usage is remote mode even though transport is in-memory.

## Adding a new optional capability

1. Define the interface in `tools/`, reusing types from their source package.
2. For embedded servers: add the parameter to `NewInMemoryServer` and wire it in `server/embedded_mcp_server.go`.
3. For external servers: add a plugin HTTP endpoint under `api/` plus an HTTP client implementation in `mcpserver/tools/`.
4. Propagate `auth.AuthTokenContextKey` and `auth.UserIDContextKey` through HTTP callbacks.

## Adding a Mattermost MCP tool

1. Implement the resolver in `mcpserver/tools/<domain>.go`.
2. Format Mattermost entities through `format/`; do not inline model formatting.
3. Register the tool in `MattermostToolProvider.mcpTools()` or the relevant grouped helper.
4. Add or update unit tests and integration tests when behavior needs real Mattermost state.
5. Update `docs/admin_guide.md` when user-visible tool lists or behavior change.

## External plugin tools

- `BuildProxyTools` skips plugin tools that collide with native Mattermost tools or earlier plugin tools.
- `PluginMCPHandlers` must stay in sync with `api/api_bridge_mcp.go` and `external/pluginmcp/`.
- Automation tools are registered always but filtered from `tools/list` when the channel automation plugin is unavailable.

## Before-hooks

- Embedded MCP can run before-hooks via metadata `before_hook_key`, `mcp.BeforeHookStore`, and `RunBeforeHook`.
- Hook wire types live in `public/mcptool/hooks.go`; keep JSON contracts stable.

## Commands

- Unit tests: `go test -v ./mcpserver/...`
- Tool tests: `go test -v ./mcpserver/tools/...`
- Integration tests, Docker-backed: `go test -v ./mcpserver/ -run TestMCPToolsIntegration -timeout 5m`
- Stdio binary: `make mcp-server`
- MCP evals, needs LLM env: `make mcp-evals`

## Pointers

- MCP client, OAuth, dynamic loading: `/mcp/AGENTS.md`.
- Public plugin registration library: `/external/pluginmcp/AGENTS.md`.
- Production MCP admin docs: `/docs/admin_guide.md`.
