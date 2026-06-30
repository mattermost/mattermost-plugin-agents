# public/bridgeclient/AGENTS.md

Scoped instructions for the published LLM Bridge client library. Root rules in `/AGENTS.md` still apply unless overridden here.

## Boundary

- This is a published import path in the root Go module, not plugin HTTP assets and not a nested module.
- It is consumed by other plugins and Mattermost server code.
- Server handlers for this API live in `api/api_llm_bridge.go` and related bridge tests.

## Commands

- Client-only tests: `go test -v ./public/bridgeclient/... -race`
- Bridge handler tests: `go test -v ./api/... -run Bridge`
- Root checks: `make test` and `make check-style`

## Conventions

- Treat exported types, methods, and JSON field names as public API.
- Keep request/response shapes synchronized with `api/` bridge handlers.
- Keep dependencies minimal. The existing `llm` streaming dependency is deliberate; avoid pulling in more root packages.
- Extend `PluginAPI` / `AppAPI` interfaces only when the client needs a new Mattermost capability.

## Never do

- Never re-enable `public/` bundling in the Makefile.
- Never change bridge wire formats without updating server handlers, tests, and README examples.
- Never apply webapp/i18n/e2e conventions to this library unless the change also touches those areas.
