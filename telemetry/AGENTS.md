---
description: OpenTelemetry setup and the shared span attribute-key registry.
tags: [telemetry, otel, tracing, attributes]
---

# telemetry/AGENTS.md

OpenTelemetry plumbing for the plugin. See the root `## OpenTelemetry tracing` section for the cross-cutting rules; this covers package specifics.

- Start spans with `telemetry.Tracer().Start(ctx, …)`. **Reuse attribute keys from `attributes.go`** (namespace `agents.*`) instead of inventing new ones; `WithLLMAttributes()` is the helper for LLM spans.
- Output modes: off / logs / OTLP (`Init`, configured via `TelemetryOutput` / `OpenTelemetryEndpoint`).
- Use `DetachContext()` to carry trace context into background goroutines without inheriting cancellation. `WithTurnID()` correlates a trace to a conversation turn.
- Note: the tracer name is `…/mattermost-plugin-ai` (historical), distinct from the plugin id `mattermost-ai`.
