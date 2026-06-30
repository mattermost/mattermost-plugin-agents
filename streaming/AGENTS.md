# streaming/AGENTS.md

Scoped instructions for response streaming. Root rules in `/AGENTS.md` apply.

## Architecture

- HTTP/API callers that start async streams must pass a detached context, usually `telemetry.DetachContext(c.Request.Context())`.
- `GetStreamingContext` derives a cancelable context from the input; the input must already be safe to outlive HTTP return when needed.
- `StreamContinuationToPost` demotes the prior anchor; do not reuse it for regeneration unless that behavior is desired.
- Turn finalization runs when streaming ends; `conversation_id` post props are required for stop-button behavior.
- Main span name is `stream to post`.

## Commands

- Unit tests: `go test -v ./streaming/...`.
- Benchmarks: `go test -bench=. -benchmem ./streaming/...`.
- Detached context regression tests: `go test -v ./telemetry/ -run DetachContext`.

## Pointers

- Conversation orchestration: `/conversations/AGENTS.md`.
- OpenTelemetry context rules: `/telemetry/AGENTS.md`.
