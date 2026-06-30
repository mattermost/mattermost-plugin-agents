# external/pluginmcp/AGENTS.md

Scoped instructions for the helper library used by other plugins to expose MCP servers. Root rules in `/AGENTS.md` still apply unless overridden here.

## Boundary

- This is a published import path in the root Go module, not code imported by this plugin server at runtime.
- Integration with this plugin is over the bridge registration HTTP contract in `api/api_bridge_mcp.go`.

## Commands

- Library tests: `go test -v ./external/pluginmcp/... -race`
- Handler sync tests: `go test -v ./api/... -run MCP`
- Root checks: `make test` and `make check-style`

## Contract

- Keep registration paths and JSON tags synchronized with `api/api_bridge_mcp.go`.
- `AddTool` prefixes sanitized tool names with `{pluginID}__`; plugin IDs must not contain `__`.
- In `ServeHTTP`, trust `Mattermost-Plugin-ID: mattermost-ai` and use `GetUserID(ctx)` only inside that path.
- Registration is async with bounded retries; call `Unregister()` from the consuming plugin's deactivation path.
- `ExposeExternal` advertises eligibility; admin config controls whether a plugin MCP server is enabled.

## Never do

- Never import root plugin server packages into this library.
- Never add heavy dependencies beyond stdlib and the MCP SDK without strong justification.
- Never change naming/auth/registration behavior without updating README examples and bridge handler tests.
