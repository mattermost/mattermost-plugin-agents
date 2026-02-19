# mcpserver Package Guidelines

## Architecture Principles

### Configuration vs Runtime Services
- Config structs must be declarative (strings, ints, bools only)
- **Never put runtime service instances in config structs**
- Runtime services are passed directly as parameters to constructors

### Server Types and Search

**InMemoryServer** (embedded in plugin):
- Takes `searchService SemanticSearchService` directly as a parameter
- The plugin passes its search service instance

**HTTP/Stdio/PluginHandlers** (external servers):
- Create their own `HTTPSemanticSearchService` internally
- This service calls back to the plugin's `/api/v1/search/raw` endpoint

### Adding New Optional Capabilities
1. Define interface in `tools/` package
2. For embedded servers: add parameter to `NewInMemoryServer`
3. For external servers: add plugin HTTP endpoint + HTTP client implementation
