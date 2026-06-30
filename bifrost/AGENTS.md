---
description: Bifrost gateway adapter implementing llm.LanguageModel — Responses-vs-Chat routing, fallbacks, key redaction.
tags: [bifrost, llm, providers, otel]
---

# bifrost/AGENTS.md

Adapter that wraps the external `github.com/maximhq/bifrost/core` gateway to implement `llm.LanguageModel`. Not a fork and not an in-repo provider SDK — it translates the plugin's `CompletionRequest`/stream types to/from Bifrost.

- **Entry point:** `NewFromServiceConfig` (called from `bots.getBaseLLM`). Supported service types are listed in `config.go` (`MapServiceTypeToProvider` / `IsSupported`); update both when adding a provider family.
- **Dual API routing:** `shouldUseResponsesAPI` picks `streamResponses` vs `streamChat` and sets the `LLMPath` span attribute. OpenAI always uses Responses; OpenAI-compatible/Azure honor the per-service toggle.
- **Fallbacks are Bifrost-native and validated at bot init** — a misconfigured fallback fails at construction, not silently at runtime. Fallbacks sharing a base provider name get `provider::serviceID` custom slots.
- **Strip a trailing `/v1`** from OpenAI base URLs before handing them to Bifrost (it appends `/v1/...` itself).
- **Redact everywhere:** primary and fallback API keys go through the sanitizing logger and `llm.SanitizeProviderError`; the `otelTracer` bridge also sanitizes keys before setting span error status.
- **`CountTokens` preflight** strips native tools/reasoning/format (keeps only function tools), 30s timeout. Unsupported operations are detected at call time via the `unsupported_operation` error code, not a static capability matrix.
- Annotation positions use **UTF-16 code units** (to match the JS frontend's string slicing) — see `llm/string_indices.go`.
- Embeddings, transcription, and model listing are separate Bifrost clients in sibling files (`embeddings.go`, `transcription.go`, `models.go`).

Tests: `go test ./bifrost/...`.
