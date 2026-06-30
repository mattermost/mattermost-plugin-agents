# telemetry/AGENTS.md

Scoped instructions for OpenTelemetry helpers. Root rules in `/AGENTS.md` apply.

## Context rules

- Thread `ctx context.Context` from entry point to LLM/tool/search work.
- Use `telemetry.DetachContext` for async work started from HTTP handlers that must survive request return.
- Allowed background roots include plugin hook entry spans, background index jobs, admin utility calls, and fire-and-forget title generation.
- Do not introduce new production `context.Background()` shortcuts without documenting why no parent context exists.

## Spans and attributes

- Start spans with `telemetry.Tracer().Start`, defer `span.End()`, record errors, and set error status.
- Reuse attribute keys from `attributes.go`; do not invent near-duplicate keys.
- Turn correlation uses `WithTurnID` and `SpanContextForTurn`.
- Common span owners: `bifrost/`, `llm/tools.go`, `toolrunner/`, `streaming/`, `search/`, `websearch/`, `conversations/`, and `mcp/`.

## Commands

- Unit tests: `go test -v ./telemetry/...`.

## Pointers

- Admin setup for tracing output/endpoints: `docs/admin_guide.md`.
- Async streaming rules: `/streaming/AGENTS.md`.
- Search async behavior: `/search/AGENTS.md`.
