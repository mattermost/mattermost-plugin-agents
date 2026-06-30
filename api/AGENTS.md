# api/AGENTS.md

Scoped instructions for plugin HTTP handlers. Root rules in `/AGENTS.md` still apply.

## Router and middleware

- `ServeHTTP` builds a new Gin router per request and registers routes inline.
- Global middleware includes OpenTelemetry, request logging, and metrics.
- Add routes in `api.go` or helpers registered from `ServeHTTP`.

## Authorization tiers

- `/bridge/v1/*`: inter-plugin auth through `Mattermost-Plugin-ID`.
- `/mcp-server/*`: MCP auth middleware when the plugin MCP server is enabled.
- Most plugin routes: Mattermost user auth through `Mattermost-User-Id`.
- `/admin/*`: user auth plus Mattermost admin auth.
- Post/channel/search routes may also require bot, post, or channel authorization middleware.

## Config endpoints

- Admin config URL: `/plugins/mattermost-ai/admin/config`.
- `normalizeAdminConfig` mutates config; GET handlers must clone before normalizing.
- PUT contract: save config, update in-memory config, then publish cluster notification.
- Partial config updates follow the same clone, save, publish, update discipline.

## Agent endpoints

- Agent CRUD lives under `/agents`.
- License and quota checks belong in handlers through `enterprise.LicenseChecker`.
- Mutations publish agent update cluster events after store writes.

## Async work

- Use `telemetry.DetachContext(c.Request.Context())` when background LLM/search work must outlive the HTTP request.
- Do not let request cancellation truncate streaming operations.

## Testing

- Use `TestEnvironment` from `api_test.go` for handler tests.
- Prefer table-driven status/side-effect tests over middleware-order assertions.
- `SetExternalRebuilderForTest` is the supported hook for external MCP rebuild tests.

## Commands

- API tests: `go test -v ./api/...`
- Focused config tests: `go test -v ./api/... -run TestHandleSaveConfig`
- E2E integration for UI-visible API behavior: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list`

## Pointers

- Config types and migrations: `/config/AGENTS.md`.
- Store save semantics: `/store/AGENTS.md`.
- Public bridge client: `/public/AGENTS.md`.
- MCP bridge/server callbacks: `/mcpserver/AGENTS.md`.
