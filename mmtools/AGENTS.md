# mmtools/AGENTS.md

Scoped instructions for built-in non-MCP LLM tools. Root rules in `/AGENTS.md` still apply.

## Scope

- This package implements `llm.ToolProvider` tools such as web search and asking the user a question.
- MCP tools live in `/mcpserver/tools/`; do not mix the catalogs.

## Gotchas

- `AskUserQuestion` is cataloged only when an interactive user is present.
- Built-in web search is omitted when the bot has native web search.
- Tool execution flows through conversation approval, not direct resolver calls from UI handlers.

## Commands

- Built-in tool tests: `go test -v ./mmtools/...`

## Pointers

- Web search providers: `/websearch/AGENTS.md`.
- Tool approval: `/conversations/AGENTS.md`.
