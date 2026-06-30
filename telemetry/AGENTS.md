---
description: OpenTelemetry setup — init modes, attribute-key catalog, span-creation recipe, turn-ID trace roots, and the repo span inventory.
tags: [telemetry, otel, tracing, attributes, spans]
---

# telemetry/AGENTS.md

Global OpenTelemetry wiring for the plugin. Root `/AGENTS.md` carries the cross-cutting ctx rules; the catalog and recipe live here.

## Key files

- `telemetry/telemetry.go` — `Init`, `Tracer`, `SpanFromContext`, `DetachContext`, output modes (`OutputModeOff|Logs|OTLP`).
- `telemetry/attributes.go` — every `LLM*`, `Agent*`, `Tool*`, `MCP*`, entity attribute key + `WithLLMAttributes`.
- `telemetry/turn_id.go` — `WithTurnID`, `NewTurnIDGenerator` (deterministic trace roots per turn).
- `telemetry/log_processor.go` — span → log line processor.

## Conventions

- **Span recipe:** `ctx, span := telemetry.Tracer().Start(ctx, "span name", trace.WithAttributes(...))`; `defer span.End()`. On error: `span.RecordError(err)` + `span.SetStatus(codes.Error, msg)`.
- **Always reuse keys from `attributes.go`** — never invent `agents.*` strings ad hoc. Add new keys here if needed.
- **Init modes:** `OutputModeLogs` needs a `LogService`; `OutputModeOTLP` needs an endpoint (gRPC, insecure). Sampler is `AlwaysSample()`.
- **Turn IDs:** `telemetry.WithTurnID(ctx, turnID)` + `trace.WithNewRoot()` yields a deterministic trace ID per agent turn.
- **`DetachContext`:** keeps the active span but drops cancellation — for background post streaming that outlives the HTTP request.

## Span inventory (where spans are created)

- `bifrost/bifrost.go` — `"llm chat completion"` (+ dynamic Bifrost spans in `tracer.go`).
- `llm/tools.go`, `toolrunner/toolrunner.go` — `"resolve tool"`.
- `conversations/tool_approval.go` — `"handle tool call"`, `"handle tool result"`, `"tool followup completion"`.
- `conversations/handle_messages.go` — `"message has been posted"`, `"agent run"`; `conversations.go` — `"process dm request"`; `regeneration.go` — `"handle regenerate"`.
- `mcp/client.go` — `"mcp call tool"`. `search/search.go` — `"run search"`, `"search query"`. `websearch/*.go` — `"google web search"`, `"brave web search"`. `streaming/streaming.go` — `"stream to post"`.
- HTTP spans: `otelgin` middleware in `api/api.go`.

## Tests

In-memory OTel exporter (`integration_test.go`, `turn_id_test.go`, `detach_test.go`). `go test -v ./telemetry -run TestName`.
