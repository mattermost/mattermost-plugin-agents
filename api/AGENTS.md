# api/AGENTS.md

Scoped instructions for plugin HTTP handlers. Root rules in `/AGENTS.md` apply.

## Architecture

- `api.go` builds the Gin route surface; route additions generally start there.
- Bridge routes use `Mattermost-Plugin-ID` authentication.
- User routes use `Mattermost-User-Id`; admin routes require admin checks.
- Admin config automation should use `GET`/`PUT /plugins/mattermost-ai/admin/config`.
- MCP bridge routes must stay aligned with `mcp/`, `mcpserver/`, and `external/pluginmcp/`.

## Commands

- Unit tests: `go test -v ./api/...`.
- Focus a handler: `go test -v ./api/ -run TestName`.
- Bridge/MCP-related tests: `go test -v ./api/... -run 'Bridge|MCP'`.

## Conventions

- Keep response DTOs near the handler when they are transport-specific.
- Do not format Mattermost entities inline for model/tool output; use `format/`.
- Preserve auth middleware expectations when adding routes.

## Pointers

- Bridge client API package: `/public/bridgeclient/AGENTS.md`.
- MCP client/server behavior: `/mcp/AGENTS.md` and `/mcpserver/AGENTS.md`.
