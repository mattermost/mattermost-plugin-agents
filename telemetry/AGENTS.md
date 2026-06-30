# telemetry/AGENTS.md

Scoped instructions for OpenTelemetry helpers. Root tracing rules in `/AGENTS.md` still apply.

## Initialization

- `telemetry.Init` sets the global tracer provider for modes `off`, `logs`, and `otlp`.
- `server/main.go` applies telemetry config on activation and config changes.
- Plugin deactivation must call the returned shutdown function.

## API surface

- Use `Tracer()` for plugin spans.
- Use `DetachContext(ctx)` for background work that must outlive HTTP requests.
- Use `WithTurnID` and `SpanContextForTurn` to correlate runs to conversation turns.
- Reuse constants from `attributes.go`; do not invent attribute names locally.

## Testing

- `integration_test.go` verifies span hierarchy with an in-memory exporter.
- `detach_test.go` documents the streaming/search cancellation bug `DetachContext` prevents.
- Tests use an external package where global provider behavior matters.

## Commands

- Telemetry tests: `go test -v ./telemetry/...`
- Local OTLP stack: `docker compose -f dev/docker-compose.otel.yml up -d`

## Pointers

- Local tracing docs: `/docs/admin_guide.md`.
- API request spans: `/api/AGENTS.md`.
- LLM/provider spans: `/llm/AGENTS.md`.
