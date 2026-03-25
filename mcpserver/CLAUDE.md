# mcpserver Package Guidelines

## Architecture Principles

### Configuration vs Runtime Services
- Config structs must be declarative (strings, ints, bools only)
- **Never put runtime service instances in config structs**
- Runtime services are passed directly as parameters to constructors

### Server Types and Search

**InMemoryServer** (embedded in plugin):
- Takes `searchService tools.SemanticSearchService` directly as a parameter
- The plugin passes `*search.Search` which implements `SemanticSearchService` directly

**HTTP/Stdio/PluginHandlers** (external servers):
- Create their own `HTTPSemanticSearchService` internally
- This service calls back to the plugin's `/api/v1/search/raw` endpoint

### Type Sharing
- **Do NOT duplicate types** from the `search` package in `mcpserver/tools`
- The `SemanticSearchService` interface uses `search.Options` and `search.RAGResult` directly
- HTTP serialization types (e.g., `httpSearchRequest`, `httpSearchResult` in `search_http.go`) are separate DTOs for the HTTP boundary and should remain in their respective files
- If you need a subset of fields, accept the full type and ignore unused fields

### Adding New Optional Capabilities
1. Define interface in `tools/` package, reusing types from their source package
2. For embedded servers: add parameter to `NewInMemoryServer`
3. For external servers: add plugin HTTP endpoint + HTTP client implementation
