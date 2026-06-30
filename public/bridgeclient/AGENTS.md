# public/bridgeclient/AGENTS.md

Scoped instructions for the public bridge client package. Root rules in `/AGENTS.md` apply.

## Scope

- This package is an exported Go client for the Agents bridge API.
- It is imported from the main module path; it is not a bundled HTTP asset.
- Human API examples live in `README.md`.

## Commands

- Unit tests: `go test -v ./public/bridgeclient/...`.

## Conventions

- Keep request/response structs aligned with `/bridge/v1` handlers in `api/`.
- Agent endpoint `allowed_tools` supports MCP/embedded tools only; do not imply arbitrary Mattermost permissions.
- Preserve compatibility for external plugin consumers.

## Pointers

- Bridge route handlers: `/api/AGENTS.md`.
- External plugin MCP SDK: `/external/pluginmcp/AGENTS.md`.
