# telemetry/AGENTS.md

Scoped instructions for tracing and telemetry helpers. Root rules in `/AGENTS.md` still apply.

## Architecture

- Supported output modes are `off`, `logs`, and `otlp`.
- `WithTurnID` and `SpanContextForTurn` provide deterministic trace linkage across multi-step agent runs.
- `DetachContext` preserves span context while dropping request cancellation for background streaming/search work.
- Attribute keys live in `attributes.go`; use them instead of ad hoc strings.

## Commands

- Telemetry tests: `go test -v ./telemetry/...`

## Gotchas

- `DetachContext` has regression coverage; use it before introducing custom context wrappers.
- Local Tempo/Grafana setup is documented in `docs/admin_guide.md`.
- Record errors on spans and set error status when failures affect the traced operation.
