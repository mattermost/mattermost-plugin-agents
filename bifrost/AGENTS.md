# bifrost/AGENTS.md

Scoped pointer for the provider gateway. Root rules in `/AGENTS.md` still apply.

- Full LLM/provider guidance lives in `/llm/AGENTS.md`.
- Sanitize provider errors before exposing them to logs or clients.
- Keep Responses API, fallback, native-tool, and tracing behavior aligned with `llm` contracts.
- Tests: `go test -v ./bifrost/...`
