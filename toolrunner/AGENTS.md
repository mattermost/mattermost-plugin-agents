# toolrunner/AGENTS.md

Scoped instructions for the generic LLM tool loop. Root rules in `/AGENTS.md` still apply.

## Scope

- Executes model tool calls, appends tool results, and recalls the model until completion or max rounds.
- MCP-specific unloaded-tool behavior coordinates with `/mcp/AGENTS.md`.

## Gotchas

- Preserve max-round behavior from bot effective tool-turn settings.
- `UnloadedMCPToolUserHint` is user-facing guidance for dynamic MCP loading; keep wording aligned with MCP behavior.
- Tool execution telemetry should identify dynamic search/load behavior without leaking sensitive arguments.

## Commands

- Tool runner tests: `go test -v ./toolrunner/...`
- Unloaded MCP focus: `go test -v ./toolrunner/... -run Unloaded`

## Pointers

- MCP dynamic loading: `/mcp/AGENTS.md`.
- Conversation tool approval: `/conversations/AGENTS.md`.
