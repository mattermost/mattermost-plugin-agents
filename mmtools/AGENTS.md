# mmtools/AGENTS.md

Scoped instructions for built-in Mattermost tools exposed to LLMs. Root rules in `/AGENTS.md` still apply.

## Architecture

- Tool resolvers should use `format/` for Mattermost entity output.
- Web search tool behavior combines provider calls, source fetching, annotations, and citation markers.
- User-interaction tools are answered by users in the UI; do not auto-execute them in policy code.
- Hidden/bound params should use the `llm` tool metadata helpers instead of prompt-visible arguments.

## Commands

- Built-in tool tests: `go test -v ./mmtools/...`

## Gotchas

- Tool authorization and auto-run policy belong in callers such as `conversations/`, not in provider adapters.
- Keep tool names and argument schemas stable for MCP/eval consumers.
