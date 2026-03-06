# CLAUDE.md

## Build/Lint/Test Commands
- Build & Deploy plugin: `make deploy`
- Lint code and fix some errors, will edit files if fixes needed: `make check-style-fix`
- Run all tests: `make test`
- Run specific Go test: `go test -v ./server/path/to/package -run TestName`
- Run e2e tests: `make e2e`
- Run specific e2e test file: `cd e2e && npx playwright test filename.spec.ts --reporter=list`
- Run prompt evaluations (CI mode, non-interactive): `make evals-ci`
- Run evals with specific provider: `LLM_PROVIDER=openai make evals-ci` (options: openai, anthropic, azure, openaicompatible, all)
- Run evals with specific model: `ANTHROPIC_MODEL=claude-3-opus-20240229 make evals-ci`
- Run evals with multiple providers: `LLM_PROVIDER=openai,anthropic make evals-ci`
- Run evals with OpenAI compatible API (e.g., local LLMs): `LLM_PROVIDER=openaicompatible OPENAI_COMPATIBLE_API_URL=http://localhost:8080/v1 OPENAI_COMPATIBLE_MODEL=llama-3 make evals-ci`
- Run streaming benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`
- Run telemetry tests: `go test -v ./telemetry/...`

## OpenTelemetry / Tracing

The plugin uses OpenTelemetry for distributed tracing. Key architecture points:

- **Telemetry package** (`telemetry/`): Owns OTel initialization, attribute constants, and helpers. Use `telemetry.Tracer()` to get a tracer and `telemetry.SpanFromContext(ctx)` to get the current span.
- **context.Context threading**: All functions in the request pipeline accept `ctx context.Context` as the first parameter. Always propagate ctx from entry points (HTTP handlers, plugin hooks) through to LLM calls and external services.
- **Span instrumentation**: Spans are created in `bifrost/` (LLM calls), `llm/tools.go` (tool resolution), `conversations/tool_handling.go` (tool call handling), `mcp/` (MCP tool calls), `search/` (semantic search), `websearch/` (Brave/Google), and `streaming/` (post streaming). The `otelgin` middleware auto-creates HTTP spans.
- **Adding new spans**: Use `ctx, span := telemetry.Tracer().Start(ctx, "span name", trace.WithAttributes(...))` and `defer span.End()`. Record errors with `span.RecordError(err)` and `span.SetStatus(codes.Error, msg)`. Use attribute keys from `telemetry/attributes.go`.
- **Config**: `EnableOpenTelemetry` (bool) and `OpenTelemetryEndpoint` (string, e.g. `localhost:4317`) in plugin settings.
- **Local testing**: `docker compose -f dev/docker-compose.otel.yml up -d` starts Jaeger at `http://localhost:16686`.
- **Context aliasing**: In files where a `context *llm.Context` parameter shadows the `context` package, use `stdcontext` as the import alias for `"context"`.

## Code Style Guidelines
- Go: Follow Go standard formatting conventions according to goimports
- TypeScript/React: Use 4-space indentation, PascalCase for components, strict typing, always use styled-components, never use style properties
- Error handling: Check all errors explicitly in production code
- File naming: Use snake_case for file names
- Documentation: Include license header in all files
- Use descriptive variable and function names
- Use small, focused functions
- Write go unit tests whenever possible
- Never use mocking or introduce new testing libraries
- Document all public APIs
- Always add i18n for new text
- Write go unit tests as table driven tests whenever possible
