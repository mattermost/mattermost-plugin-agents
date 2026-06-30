# mcpserver/AGENTS.md

Scoped instructions for the `mcpserver/` package. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

## Architecture

### Configuration vs runtime services

- Config structs are declarative — strings, ints, bools only.
- Never put runtime service instances inside a config struct.
- Pass runtime services directly as parameters to constructors.

### Server types and optional capabilities

- **`NewInMemoryServer`** (embedded in the plugin; concrete type `MattermostInMemoryMCPServer`) takes optional capabilities directly: `searchService tools.SemanticSearchService` (the plugin passes `*search.Search`) and `fileContentService tools.FileContentService`.
- **HTTP / Stdio** (external servers) build their own HTTP-backed capability clients internally — `HTTPSemanticSearchService` (`search_http.go`) and `NewHTTPFileContentService(pluginURL)` (`files_http.go`) — which call back to the plugin's raw search / file-content HTTP endpoints.
- **`PluginHandlers`** (`plugin_handlers.go`) builds an aggregate external server: native Mattermost tools plus proxy tools from registered plugins (`BuildProxyTools`). On a duplicate tool name the native tool wins and the plugin proxy duplicate is dropped with an **error** log (`toolOwners`).
- Embedded clients are treated as **remote** (`tools.AccessModeRemote`), which affects `validateAccessRestrictions` on tool arguments.

### Type sharing

- Do not duplicate types from the `search` package inside `mcpserver/tools`. The `SemanticSearchService` interface uses `search.Options` and `search.RAGResult` directly.
- HTTP serialization DTOs (e.g., `httpSearchRequest`, `httpSearchResult` in `search_http.go`) are intentionally separate from domain types and stay in their respective files.
- If you only need a subset of fields, accept the full type and ignore the unused fields rather than introducing a parallel struct.

## Adding a new optional capability

Mirror the existing `SemanticSearchService` / `FileContentService` pattern:

1. Define the interface in `tools/`, reusing types from their source package.
2. For embedded servers: add the parameter to `NewInMemoryServer`.
3. For external servers: add a plugin HTTP endpoint plus an HTTP client implementation (e.g. `search_http.go`, `files_http.go`) that calls it.
