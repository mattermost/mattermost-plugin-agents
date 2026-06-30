# llmcontext/AGENTS.md

Scoped pointer for request context building. Root rules in `/AGENTS.md` still apply.

- Full context and tool-catalog guidance lives in `/llm/AGENTS.md`.
- Apply catalog flags before adding tools.
- Use interactive context only for human-present flows.
- Tests: `go test -v ./llmcontext/...`
