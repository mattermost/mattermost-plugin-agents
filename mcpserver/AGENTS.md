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

## Adding a tool

Tools live in `tools/<area>.go` and are registered through `getXTools()` aggregated by `mcpTools()` in `provider.go`. To add one:

1. **Args struct** — define `XArgs` with `json` + `jsonschema` tags. For ID fields use `minLength=26,maxLength=26` when required, `maxLength=26` (with `,omitempty`) when optional. Tag fields that only make sense for local servers with `access:"local"`.
2. **Resolver** — write `func (p *MattermostToolProvider) toolX(mcpContext *MCPToolContext, args XArgs) (string, error)`. The framework decodes args before the resolver runs, so start at the real logic — do not re-decode and do not nil-check `mcpContext.Client` (it is always set). Conventions:
   - Validate IDs with `requireID` / `optionalID`.
   - On failure return `"", fmt.Errorf(...)` — the error text is what the model sees, so put any guidance there (the first return value is only used on success). On a non-error "nothing found"/disambiguation outcome, return the guidance as `(text, nil)`.
   - For posts, set AI attribution via `p.stampAIGenerated`; for member listings reuse `p.renderMembers`.
   - Format Mattermost entities through the `format/` package, never `fmt.Sprintf` on model types.
3. **Register** — add a literal to the relevant `getXTools()`:
   `{Name: "x", Description: xDescription, Schema: NewJSONSchemaForAccessMode[XArgs](string(p.accessMode)), Resolver: typed("x", p.toolX)}`.
   Put a long description in a package-level `const`. Set `Available: someFunc` to hide the tool from `tools/list` when a dependency is absent (see the automation tools).
4. **Test** — table-driven, calling the resolver directly with an `XArgs` value (helpers in `helpers_test.go`).

## Adding a new optional capability

1. Define the interface in `tools/`, reusing types from their source package.
2. For embedded servers: add the parameter to `NewInMemoryServer`.
3. For external servers: add a plugin HTTP endpoint plus an HTTP client implementation that calls it (reuse `postPluginJSON` for the request plumbing).
