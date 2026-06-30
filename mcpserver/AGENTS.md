# mcpserver/AGENTS.md

Scoped instructions for Mattermost MCP server transports and tools. Root rules in `/AGENTS.md` still apply.

## Boundary

- This package owns built-in Mattermost MCP tools, embedded in-memory MCP, plugin HTTP handlers, standalone HTTP, and standalone stdio.
- MCP client management, OAuth, per-user tool catalogs, and meta-tools live in `mcp/`.

## Architecture

### Configuration vs runtime services

- Config structs are declarative: strings, ints, bools, slices, maps.
- Never put runtime service instances inside config structs.
- Pass runtime services directly to constructors.

### Server modes

| Mode | Constructor | Access mode | Search/files |
| --- | --- | --- | --- |
| Embedded | `NewInMemoryServer` | remote | Direct service params |
| Plugin HTTP | `NewPluginMCPHandlers` | remote | Plugin callback HTTP |
| Standalone HTTP | `NewHTTPServer` | remote | Plugin callback HTTP |
| Standalone stdio | `NewStdioServer` | local | Plugin callback HTTP |

- Embedded mode intentionally uses remote access mode; local file path attachments are only for stdio/dev mode.
- `PluginMCPHandlers` registers native tools first, then proxy tools from other plugins.

### Optional services

- Search uses `tools.SemanticSearchService` with `search.Options` and `search.RAGResult`; do not duplicate search types.
- File reads use `tools.FileContentService`; embedded mode receives `*files.Service`, external modes use `/api/v1/files/content`.
- HTTP DTOs stay near their HTTP client/server implementation and remain separate from domain types.

### Tool registration

- Automation tools are registered but filtered from `tools/list` when the Channel Automation plugin is unavailable.
- Native Mattermost tools win over duplicate proxy tool names in the external MCP endpoint.
- Before-hooks are embedded-only callbacks resolved through short-lived KV keys.

## Adding a new optional capability

1. Define the interface in `tools/`, reusing source-package types when possible.
2. Embedded: add the runtime dependency to `NewInMemoryServer`.
3. External: add a plugin API endpoint plus an HTTP client implementation in `tools/*_http.go`.
4. Gate tool registration on `service != nil` and `service.Enabled()` when applicable.

## Commands

- Unit/integration tests: `go test -v ./mcpserver/...`
- Build standalone binary: `make mcp-server`
- MCP evals: `make mcp-evals`

## Gotchas

- `go test ./mcpserver/...` may use Testcontainers; Docker must be available.
- External HTTP search advertises capability optimistically; semantic search can still fail at runtime if disabled in the plugin.
- Keep `mcpserver/README.md` human-facing; this file is the coding-agent source of truth.
