# metrics/AGENTS.md

Scoped instructions for Prometheus metrics. Root rules in `/AGENTS.md` still apply.

## Conventions

- Metric names use the `agents_*` namespace with system, HTTP, API, LLM, and MCP subsystems.
- `NewMetrics(InstanceInfo)` attaches installation and plugin instance labels.
- Use `NewNoopMetrics()` in tests instead of adding new mock libraries.
- `ObserveTokenUsage` intentionally avoids high-cardinality user labels.
- Empty team or bot label values should become `"unknown"`.

## Exposure

- Metrics are served through the plugin metrics handler.
- HTTP request metrics are recorded by API middleware.

## Commands

- Metrics tests: `go test -v ./metrics/...`

## Pointers

- HTTP middleware: `/api/AGENTS.md`.
- Token usage wrapper: `/llm/AGENTS.md`.
