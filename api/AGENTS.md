# api/AGENTS.md

Scoped instructions for plugin HTTP handlers. Root rules in `/AGENTS.md` still apply.

## Architecture

- `ServeHTTP` rebuilds the Gin router per request with middleware, metrics, and OTel.
- Keep auth tiers explicit:
  - `/bridge/v1/*` uses inter-plugin auth headers.
  - Most routes use the Mattermost user header.
  - `/admin/*` requires system admin.
  - Post/channel/search routes combine bot selection and resource authorization.
- Admin config is DB-backed. `GET`/`PUT /admin/config` must use the plugin store, not Mattermost server config.
- Clone config before normalization on read paths; normalization mutates defaults.
- For background LLM or streaming work after the HTTP handler returns, use `telemetry.DetachContext(c.Request.Context())`.

## Commands

- API tests: `go test -v ./api/...`

## Gotchas

- Route order matters when static paths share prefixes with `/:id` routes.
- Preserve sentinel errors from `conversations/` so handlers return the correct status codes.
- `/search/raw` and `/files/content` are MCP callback endpoints, not UI endpoints.
