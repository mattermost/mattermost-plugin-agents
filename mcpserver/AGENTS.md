# mcpserver/AGENTS.md

Scoped instructions for the `mcpserver/` package. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

## Commands

- Build standalone dev binary: `make mcp-server`.
- Unit and integration tests (Docker required for integration paths): `go test -v ./mcpserver/...`.
- LLM tool-quality evals (provider keys + Docker): `make mcp-evals`.

## Architecture

### Configuration vs runtime services

- Config structs are declarative — strings, ints, bools only.
- Never put runtime service instances inside a config struct.
- Pass runtime services directly as parameters to constructors.

### Server types and optional services

- **`InMemoryServer`** (embedded in the plugin) takes `searchService tools.SemanticSearchService` directly. The plugin passes `*search.Search`, which implements `SemanticSearchService`.
- **`InMemoryServer`** also takes `FileContentService` directly for Mattermost file reads.
- **HTTP / Stdio / PluginHandlers** build HTTP service implementations internally; search calls `/api/v1/search/raw`, and file reads call `/api/v1/files/content`.
- Embedded tools register with `tools.AccessModeRemote` even though transport is in-memory.
- **`PluginMCPHandlers`** is the external plugin aggregate; plugin-visible capabilities may also need proxy wiring plus `/bridge/v1/...` endpoints.

### Type sharing

- Do not duplicate types from the `search` package inside `mcpserver/tools`. The `SemanticSearchService` interface uses `search.Options` and `search.RAGResult` directly.
- HTTP serialization DTOs (for example `httpSearchRequest`, `httpSearchResult`, and file-content DTOs) are intentionally separate from domain types and stay in their respective files.
- If you only need a subset of fields, accept the full type and ignore the unused fields rather than introducing a parallel struct.
- Mattermost entity formatting in tool output goes through `format/`.
- Before-hook callback payloads use shared types in `public/mcptool/`.

## Adding a new optional capability

1. Define the interface in `tools/`, reusing types from their source package.
2. For embedded servers: add the parameter to `NewInMemoryServer`.
3. For external servers: add a plugin HTTP endpoint plus an HTTP client implementation that calls it.
4. If external plugins can expose the capability, update `PluginMCPHandlers` and bridge endpoints.

## Pointers

- Standalone server setup and transport details: `mcpserver/README.md`.
- Admin/operator MCP setup: `docs/admin_guide.md`.
- MCP client-side behavior: `mcp/AGENTS.md`.
