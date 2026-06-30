---
description: Provider-agnostic LLM abstraction — LanguageModel, wrappers, stream contract, truncation, llm.Context.
tags: [llm, providers, streaming, tools, tokens]
---

# llm/AGENTS.md

Provider-agnostic contract plus request/stream types, the tool catalog, truncation, and token accounting. Real provider I/O lives in `bifrost/`; this package defines the interfaces everything else codes against.

## Core invariants

- **`LanguageModel` is the only provider interface** (`language_model.go`): `ChatCompletion` (returns `*TextStreamResult`) and `ChatCompletionNoStream`. Everything streams under the hood — `ChatCompletionNoStream` is implemented via `ChatCompletion` + `ReadAll()`.
- **`llm.Context` (`context.go`) is NOT Go's `context.Context`** — it's per-turn prompt/tool state. In files that need both, import the stdlib as `stdcontext` (root rule). Build it with `llmcontext.Builder`, don't assemble it by hand.
- **Production LLMs are Bifrost-backed then wrapped, and order matters:** Truncation → TokenUsage → StructuredOutputFallback (`bots/bots.go`). Anything intercepting `ChatCompletionNoStream` must sit outside the token wrapper.
- **Stream-event contract (`stream.go`):** consumers must handle all `EventType*` variants. A tool-call turn ends with `EventTypeToolCalls` and does **not** emit `EventTypeEnd` first. `EventTypeUsage` is consumed/logged by the token wrapper and not forwarded downstream.
- **Truncation never drops system posts** (`truncation.go`): heuristic truncate first, then optional provider `CountTokens`; drop oldest non-system posts to fit. Implementations return `ErrUnsupportedTokenCount` when they can't count, and callers fall back to `EstimateTokens`.
- **Set `CompletionRequest.Operation` / `OperationSubType`** (`token_usage_fields.go`) — they drive telemetry and token logs.
- Tools stay in the prompt even when execution is disabled; `WithToolsDisabled()` sets `tool_choice=none` but keeps definitions when history contains tool_use.

## Providers

- OpenAI-compatible providers register in the `openAICompatibleProviders` map (`providers.go`) — no `bots.go`/`api.go` changes needed for a registry entry.
- **But registry membership ≠ Bifrost support.** A new primary service type also needs mapping in `bifrost/config.go` (`MapServiceTypeToProvider`, `IsSupported`). `ServiceTypeScale` is config-valid but not Bifrost-backed — an incomplete integration to be aware of.

## Commands

- `go test ./llm/...`; benchmarks: `go test -bench=. -benchmem ./llm/...`.
