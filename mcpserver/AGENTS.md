# mcpserver/AGENTS.md

Scoped instructions for the `mcpserver/` package. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

## Architecture

### Configuration vs runtime services

- Config structs are declarative — strings, ints, bools only.
- Never put runtime service instances inside a config struct.
- Pass runtime services directly as parameters to constructors.

### Server types and the search service

- **`InMemoryServer`** (embedded in the plugin) takes `searchService tools.SemanticSearchService` directly. The plugin passes `*search.Search`, which implements `SemanticSearchService`.
- **HTTP / Stdio / PluginHandlers** (external servers) build their own `HTTPSemanticSearchService` internally; that service calls back to the plugin's `/api/v1/search/raw` endpoint.

### Type sharing

- Do not duplicate types from the `search` package inside `mcpserver/tools`. The `SemanticSearchService` interface uses `search.Options` and `search.RAGResult` directly.
- HTTP serialization DTOs (e.g., `httpSearchRequest`, `httpSearchResult` in `search_http.go`) are intentionally separate from domain types and stay in their respective files.
- If you only need a subset of fields, accept the full type and ignore the unused fields rather than introducing a parallel struct.

## Adding a new optional capability

1. Define the interface in `tools/`, reusing types from their source package.
2. For embedded servers: add the parameter to `NewInMemoryServer`.
3. For external servers: add a plugin HTTP endpoint plus an HTTP client implementation that calls it.
