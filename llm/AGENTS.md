# llm/AGENTS.md

Scoped instructions for LLM contracts, request composition, and tools. Root rules in `/AGENTS.md` still apply.

## Architecture

- Feature packages depend on `LanguageModel`; production wiring uses `bifrost/`, not direct provider SDK calls.
- New OpenAI-compatible providers belong in `openAICompatibleProviders`; update Bifrost config mapping when needed.
- Keep `context.Context` separate from `*llm.Context`. The former carries cancellation/tracing; the latter carries prompt, tool, and user context.
- Build `*llm.Context` through `llmcontext/` for runtime flows instead of ad hoc construction.
- Set `CompletionRequest.Operation` for token usage and telemetry.
- Use `EscapePromptContent` for user-generated template variables.
- Tool runtime metadata must not leak into prompt-visible catalog context.

## Commands

- LLM tests: `go test -v ./llm/... ./bifrost/... ./llmcontext/...`
- Streaming/LLM benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`

## Gotchas

- `ToolCatalogContext` and `ToolRuntimeContext` serve different audiences.
- Tool retry/failure policy lives in `llm/tool_retry.go`; tool-call orchestration lives in `toolrunner/`.
- When changing prompt templates, check `evals/AGENTS.md` for eval expectations.
