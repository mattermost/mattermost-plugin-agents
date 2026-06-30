---
description: Provider-agnostic LLM domain layer — LanguageModel contract, wrapper chain, tool/MCP naming, config and fallback semantics.
tags: [llm, language-model, tools, mcp, streaming, prompts]
---

# llm/AGENTS.md

Provider-agnostic LLM types and helpers. **Provider implementations live in `bifrost/`** — `llm/` is the contract, not the gateway. Root `/AGENTS.md` still applies.

## Key files / interfaces

- `llm/language_model.go` — `LanguageModel` interface, `LanguageModelConfig`, options (`WithModel`, `WithMaxGeneratedTokens`, `WithJSONOutput`, `WithToolsDisabled`, `WithNativeWebSearchAllowed`, `WithReasoningDisabled`).
- `llm/completion_request.go` — `CompletionRequest`, `Post`, `Truncate`.
- `llm/context.go` — `Context` (the prompt/tool runtime context).
- `llm/configuration.go` — `ServiceConfig`, `BotConfig`, `IsValidService`, `ResolveFallbackChain`, `DefaultMaxToolTurns`.
- `llm/service_types.go` — `ServiceType*` constants.
- `llm/tools.go` — `Tool`, `ToolStore`, `ResolveTool`, MCP naming (`NamespaceMCPToolName`, `BareMCPToolName`).
- `llm/stream.go` — `TextStreamResult`/`TextStreamEvent`, `ReadAll`.
- `llm/prompts.go` — the prompt **renderer** (`NewPrompts(fs.FS)`, `Format`); templates live in `prompts/`.
- Wrappers: `truncation.go`, `token_tracking.go`, `structured_output_fallback.go`.

## Conventions & gotchas

- **Adding a provider** is mostly a `bifrost/` task. The minimal path: constant in `service_types.go` → validation in `configuration.go` `IsValidService` → mapping in `bifrost/config.go` (`MapServiceTypeToProvider`, `IsSupported`) → behavior in `bifrost/bifrost.go`. For generic OpenAI-compatible endpoints use the existing `ServiceTypeOpenAICompatible` + `APIURL` — no registry entry. (`llm/providers.go` is a registry referenced only by its own test; don't assume it wires runtime behavior.)
- **Contract:** every method takes `context.Context` first; streaming is canonical (`ChatCompletion` → `*TextStreamResult`). `CountTokens` may return `ErrUnsupportedTokenCount` → fall back to `EstimateTokens`. `InputTokenLimit() == 0` disables client-side truncation.
- **Wrapper order** (applied in `bots/bots.go`): `TokenUsageLoggingWrapper` converts non-stream calls to streaming internally, so wrappers that must intercept `ChatCompletionNoStream` go outside it.
- **Tools:** MCP tools are namespaced `{serverSlug}__{bareName}`; consumers of `ReadAll` must handle all `EventType*` values.
- **Prompts:** `llm/prompts.go` renders from a caller-supplied `fs.FS` (the embedded `prompts.PromptsFolder`); it does not embed templates itself.
- `stdcontext` alias is not needed inside `llm/` itself; it's for packages that take a `*llm.Context`.

## Tests

Table-driven across `*_test.go`; prompt tests use `fstest.MapFS`. Benchmarks: `go test -bench=. -benchmem ./llm/...`.
