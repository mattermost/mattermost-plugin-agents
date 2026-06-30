# llm/AGENTS.md

LLM stack: `llm/` (types, tools, wrappers) → `bifrost/` (the only production provider impl) → `toolrunner/` (execution) → `streaming/` (posts). `llmcontext/` assembles `*llm.Context`. Root `/AGENTS.md` still applies.

## Provider wiring (production path)

- Every real bot LLM is built in `bots/bots.go` `getBaseLLM`: `bifrost.NewFromServiceConfig(service, botCfg, fallbackServices)`. The only exception is `llm.ServiceTypeLoadTestMock` → `loadtest.NewMockLLM`.
- Wrapper stack (outer = applied last): `StructuredOutputFallbackWrapper` → `TokenUsageLoggingWrapper` (optional) → `TruncationWrapper` → base. `TokenUsageLoggingWrapper` streams internally even for `ChatCompletionNoStream`; any wrapper that must intercept `ChatCompletionNoStream` has to sit **outside** it.
- `llm/providers.go` (`openAICompatibleProviders`, `GetOpenAICompatibleProvider`) is **not wired into runtime** post-Bifrost — only tested. Don't extend that registry expecting it to take effect.

## Adding a service type

1. Constant in `llm/service_types.go`; accept it in `llm/configuration.go` `IsValidService`.
2. Map it in `bifrost/config.go` (`MapServiceTypeToProvider`, `IsSupported`); gate native tools via `SupportsNativeTools`.
3. If OpenAI-family, update `llm.ServiceUsesResponsesAPI`.
4. Add to `evals/evals.go` if eval-backed; update admin UI / `docs/admin_guide.md` if user-facing.

## Core types & tools

- `llm.LanguageModel`: `ChatCompletion`, `ChatCompletionNoStream`, `CountTokens`, `InputTokenLimit`, `OutputTokenLimit`. Per-call opts via `LanguageModelOption` (`WithModel`, `WithToolsDisabled`, `WithJSONOutput[T]`).
- `CompletionRequest{Posts, Context, Operation, OperationSubType}`; system prompt = first `PostRoleSystem` post.
- `*llm.Context` is per-turn prompt/tool/runtime state; none of its fields are guaranteed present. Import stdlib `"context"` as `stdcontext` when both appear in one file.
- Tools: `llm.Tool{Name, Description, Schema, Resolver}`, schema via `NewJSONSchemaFromStruct[T]()`. Catalog ≠ execution: `WithToolsDisabled()` blocks execution but leaves tools visible. MCP names: `NamespaceMCPToolName`, `BareMCPToolName`, `LookupTool`.
- Always run provider errors through `SanitizeProviderError` / `SanitizeProviderErrorMessage` before logs, streams, or spans.
- `CountTokens` may return `ErrUnsupportedTokenCount`; `TruncationWrapper` then falls back to `EstimateTokens` and must never drop system posts.

## bifrost/

Wraps `github.com/maximhq/bifrost/core`; also exposes `NewEmbeddingProvider`, `NewTranscriber`. OpenAI-compatible base URLs are normalized (strip trailing `/v1` — Bifrost re-adds it). `ChatCompletionNoStream` = `ChatCompletion` + `ReadAll()` (always SSE underneath). Unmappable fallback services **fail bot setup** rather than being silently dropped. Span `"llm chat completion"` in `bifrost.go`; Bifrost-internal spans via `bifrost/tracer.go`.

## streaming/

Consumes `*llm.TextStreamResult` → posts + websocket events + optional turn persistence; never calls an LLM. One active stream per post (`GetStreamingContext` → `ErrAlreadyStreamingToPost`); always `defer FinishStreaming(post.Id)`. Use `StreamContinuationToPost` for tool-approval resume, **not** for regeneration. `buildContentBlocks()` must return a non-nil slice (webapp crashes on `null`). Span `"stream to post"`.

## llmcontext/

`Builder` assembles `*llm.Context` per turn. Critical option rules:

- `WithLLMContextInteractive()` — **only** when a human is interactively present (DM / channel mention); never for bots, webhooks, bridge API, or evals. It gates `Tool.UserInteraction` tools.
- `WithLLMContextNoTools` / `WithLLMContextConcreteTools` for non-interactive and bridge callers.
- Request `ctx` must be non-nil when MCP tools are needed.

## Testing

- `llm/`, `bifrost/`, `streaming/`: table-driven, real stores/resolvers and `httptest`/fakes — **no mockery in these packages**. Generated mocks under `llm/mocks/` exist only for `search`/`react`/`threads` consumers; don't extend without cause.
- Benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`.
