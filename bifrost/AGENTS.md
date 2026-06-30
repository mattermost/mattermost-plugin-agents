---
description: Bifrost gateway implementing llm.LanguageModel — provider mapping, Responses vs Chat routing, fallback chains, and LLM-call tracing.
tags: [bifrost, llm, providers, fallback, responses-api, otel]
---

# bifrost/AGENTS.md

Single implementation of `llm.LanguageModel` via [maximhq/bifrost](https://github.com/maximhq/bifrost). **This is where "add a provider" work actually happens.** Root `/AGENTS.md` still applies.

## Key files

- `bifrost/bifrost.go` — `LLM`, `New`, `ChatCompletion`, `streamChat`/`streamResponses`, `convertToBifrostRequest`, `shouldUseResponsesAPI`.
- `bifrost/config.go` — `NewFromServiceConfig`, `MapServiceTypeToProvider`, `IsSupported`, `SupportsNativeTools`, `normalizeOpenAIBaseURL`.
- `bifrost/models.go` — model listing. `bifrost/embeddings.go`, `bifrost/transcription.go` — other Bifrost-backed services.
- `bifrost/tracer.go`, `bifrost/errors.go`, `bifrost/logger.go` — OTel bridge, error recording, API-key-redacting logger.

## Conventions & gotchas

- **Add a provider:** map the `ServiceType` in `MapServiceTypeToProvider` AND add it to `IsSupported`; provider-specific behavior (Responses vs chat, reasoning, native tools, message conversion) goes in `bifrost.go`. A `ServiceType` that is valid in `llm` but missing from `IsSupported` fails at `NewFromServiceConfig` (see `ServiceTypeScale`).
- **Responses vs Chat** (`shouldUseResponsesAPI`): OpenAI always uses Responses; also triggered by native tools or `NativeWebSearchAllowed`. `ChatOnly` fallbacks downgrade Responses → chat.
- **Fallback chains** from `llm.ResolveFallbackChain`; colliding providers get custom names `{base}::{serviceID}`. Misconfigured fallbacks fail at construction, not silently.
- **Anthropic:** unsigned reasoning isn't replayed; consecutive same-role messages merge; thinking budget must be `< max_tokens`.
- **Base URL:** strip a trailing `/v1` for OpenAI-type providers (`normalizeOpenAIBaseURL`).
- **CountTokens** strips native server tools and maps `unsupported_operation` → `llm.ErrUnsupportedTokenCount`.

## Tracing (LLM calls)

The outer plugin span `"llm chat completion"` is created here (`telemetry.WithLLMAttributes`); inner Bifrost spans come from the `otelTracer` registered in the Bifrost client. Errors via `recordBifrostError`; composition/usage attributes via `telemetry/attributes.go`. See `telemetry/AGENTS.md` for the catalog.

## Tests

`bifrost_test.go` is an `httptest`-backed integration suite; span assertions in `tracer_test.go`/`composition_span_test.go`. `go test -v ./bifrost -run TestName`.
