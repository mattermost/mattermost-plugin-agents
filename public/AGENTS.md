# public/AGENTS.md

Scoped instructions for public Go API subpaths. Root rules in `/AGENTS.md` still apply.

## Scope

- `public/bridgeclient/` is the LLM Bridge client for other Mattermost plugins or server code.
- `public/mcptool/` contains shared MCP before-hook request/response types.
- This is a public import surface within the root module, not plugin HTTP assets.

## Bundle rule

- `HAS_PUBLIC` is intentionally cleared so `public/` is not copied into plugin bundles.
- Do not "fix" bundle detection by restoring public assets.

## Bridge client gotchas

- Plugin ID constant is `mattermost-ai`.
- Agent endpoints support `allowed_tools`; service endpoints reject it.
- Allowed tools may use namespaced MCP names; bare names can be ambiguous.
- Permission checks are opt-in through request user/channel fields.
- Streaming uses event types from the root module; preserve public compatibility.

## MCP hook types

- `public/mcptool` is the JSON contract for before-hook callbacks.
- Keep hook structs in sync with `mcpserver` and plugin registration behavior.

## Commands

- Public package tests: `go test -v ./public/...`
- Bridge client tests: `go test -v ./public/bridgeclient/...`

## Pointers

- Server bridge handlers: `/api/AGENTS.md`.
- MCP hooks/server behavior: `/mcpserver/AGENTS.md`.
- Public bridge README: `bridgeclient/README.md`.
