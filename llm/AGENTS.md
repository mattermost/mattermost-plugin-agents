# llm/AGENTS.md

Scoped instructions for LLM abstractions. Root rules in `/AGENTS.md` apply.

## Architecture

- `LanguageModel` is the boundary used by conversation, search, eval, and tool flows.
- All chat paths take `context.Context` first and must preserve it into implementations and wrappers.
- When `*llm.Context` would shadow the stdlib package, import `"context"` as `stdcontext`.
- `CompletionRequest.Operation` uses constants from `token_usage_fields.go`; these feed telemetry and usage accounting.
- `ToolStore.ResolveTool` owns a tool-resolution span; `toolrunner` adds runner-level spans around execution.
- Token usage wrappers must continue to wrap new `LanguageModel` features.
- Add OpenAI-compatible provider entries in `providers.go`; do not wire each provider through `bots.go` or `api.go`.

## Commands

- Unit tests: `go test -v ./llm/...`.
- Benchmarks: `go test -bench=. -benchmem ./llm/...`.
- Gateway tests: `go test -v ./bifrost/...`.

## Pointers

- Production gateway and fallback behavior: `/bifrost/AGENTS.md`.
- Tool execution loop: `/toolrunner/AGENTS.md`.
- Eval provider wiring: `/evals/AGENTS.md`.
