# external/pluginmcp/AGENTS.md

Scoped instructions for the external plugin MCP SDK. Root rules in `/AGENTS.md` apply.

## Scope

- This package helps other Mattermost plugins expose MCP tools through the Agents plugin.
- Human API examples live in `README.md`; keep this file to repo maintenance notes.

## Commands

- Unit tests: `go test -v ./external/pluginmcp/...`.

## Integration notes

- Registration uses `POST /mattermost-ai/bridge/v1/mcp/register` and `PluginHTTP`.
- Handlers trust `Mattermost-Plugin-ID: mattermost-ai`; use `GetUserID(ctx)` only inside served requests.
- `ExposeExternal` is plugin-declared, but admin enablement and per-tool policy still apply in Agents.
- Keep README claims aligned with `registration.go`, `server.go`, and admin docs when editing this package.

## Pointers

- MCP client behavior: `/mcp/AGENTS.md`.
- MCP server/tool behavior: `/mcpserver/AGENTS.md`.
