# AGENTS.md

> Canonical agent instructions for this repository. Humans should read `README.md` and `docs/`. Every `CLAUDE.md` in this repository is a thin import of the nearest `AGENTS.md`; do not duplicate instructions there.

## Agent instruction convention

- Keep shared, tool-agnostic rules in `AGENTS.md`; keep Claude compatibility in `CLAUDE.md` with only an `@.../AGENTS.md` import.
- Prefer scoped `AGENTS.md` files next to code with local contracts. Root rules still apply unless a nested file is more specific.
- Keep instructions imperative, current, and command-oriented. Update the relevant `AGENTS.md` in the same change that alters a convention.
- Do not add or modify instruction files under `.cursor/`.

## Project

Mattermost server plugin (`mattermost-ai`) that integrates LLM providers. Go 1.26 backend + React/TypeScript webapp (Node 24.11). Most Go packages live at the repo root, not under `server/`.

## Commands

`make help` lists every documented target with a one-line description.

- Pre-PR aggregate, recommended: `make check`
  - Runs `check-style`, `test`, `check-shards`, `check-i18n`, `check-locks`, and `check-go-mods`.
  - If it fails, run the underlying target to isolate the failure. Drift targets may regenerate files; review and commit those changes.
- Lint only: `make check-style`
- Lint with auto-fix and i18n extraction: `make check-style-fix`
- All unit tests: `make test`
- Single Go test: `go test -v ./<pkg> -run TestName`
- Build plugin bundle: `make dist`
- Linux amd64 CI/cloud bundle: `make dist-ci`
- Build and deploy to a running Mattermost: `make deploy`
- E2E full suite, slow and Docker-backed: `make e2e`
- Single e2e spec: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list`
- Prompt evals: `make evals-ci`
  - Providers: `LLM_PROVIDER=openai|anthropic|azure|mistral|bedrock|cohere|openaicompatible|all make evals-ci`
  - `all` excludes `openaicompatible`; that provider needs `OPENAI_COMPATIBLE_API_URL` and `OPENAI_COMPATIBLE_MODEL`.
  - Model example: `ANTHROPIC_MODEL=claude-sonnet-4-6 make evals-ci`
- Streaming benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`

## Repository layout

- `server/` - plugin entrypoint, lifecycle, configuration adapter.
- `api/`, `mmapi/` - HTTP handlers; Mattermost API wrappers.
- `config/`, `store/`, `postgres/` - runtime config, plugin DB schema, pgvector embeddings.
- `llm/`, `bifrost/`, `prompts/`, `llmcontext/` - LLM contracts, provider gateway, prompt templates, context building.
- `conversation/`, `conversations/`, `streaming/` - conversation entities, generation orchestration, Mattermost post streaming. These packages are not interchangeable.
- `mcp/`, `mcpserver/`, `external/pluginmcp/` - MCP client, MCP servers/tools, cross-plugin registration library.
- `format/` - LLM-facing formatting of Mattermost entities.
- `bots/`, `channels/`, `threads/`, `meetings/`, `search/`, `embeddings/`, `indexer/`, `files/`, `websearch/` - feature packages.
- `webapp/` - React/TypeScript plugin UI bundle.
- `e2e/` - Playwright + Testcontainers end-to-end tests.
- `evals/`, `cmd/evalviewer/` - prompt evaluation harness and TUI.
- `loadtest/` - mock LLM and load-test-ng controller module.
- `public/` - public Go API subpaths for other plugins; not HTTP assets.
- `i18n/` - server-side Go i18n catalog. Webapp extracted strings live in `webapp/src/i18n/en.json`.

## Conventions

- Linters enforce formatting, imports, headers, indentation, and many TS/Go rules. The rules here are the ones linters cannot reliably infer.
- File names: `snake_case.go` / `snake_case.ts(x)`.
- TypeScript/React: PascalCase components, strict typing, styled-components for new styling; avoid new inline `style={{...}}`.
- User-facing webapp strings use formatjs at the call site; `make i18n-extract` regenerates `webapp/src/i18n/en.json`.
- Server-side strings in `/i18n/en.json` are hand-curated.
- Go tests are table-driven when there is more than one case.
- Do not introduce a new test or mocking library. Existing mockery mocks are regenerated with `make mock`.
- Format Mattermost entities for LLM consumption or tool output through `format/`; never `fmt.Sprintf` model types inline.
- E2E specs that should run in CI must be assigned in `e2e/scripts/ci-test-groups.mjs` in the same change.
- Test behavior that could break due to a real bug; avoid assertions on implementation details like validation order.

## OpenTelemetry tracing

- Thread `ctx context.Context` as the first parameter through every entry point to LLM calls. Do not use `context.Background()` shortcuts in production LLM paths.
- Existing spans live in `bifrost/`, `llm/tools.go`, `conversations/tool_approval.go`, `mcp/`, `search/`, `websearch/`, and `streaming/`. HTTP spans come from `otelgin`.
- To add a span: `ctx, span := telemetry.Tracer().Start(ctx, "span name", trace.WithAttributes(...))`, then `defer span.End()`.
- Record errors with `span.RecordError(err)` and `span.SetStatus(codes.Error, msg)`.
- Reuse keys from `telemetry/attributes.go`; do not invent new attribute names.
- When `*llm.Context` would shadow the `context` package, import `"context"` as `stdcontext`.
- Config fields and local Tempo/Grafana setup live in `docs/admin_guide.md`.

## Never do

- Never edit `webapp/dist/`, `server/dist/`, or `dist/`; regenerate with `make dist`, `make dist-ci`, or `make deploy`.
- Never hand-edit `webapp/src/i18n/en.json`; add strings at the call site and run extraction.
- Never treat `/i18n/` and `webapp/src/i18n/` as the same localization system.
- Never push to `master`; open a PR.

## Gotchas

- If `make install-go-tools` fails to build `mattermost-govet`, the pinned commit is incompatible with the active Go toolchain. The Makefile prints the exact fix; apply it.
- `postgres/pgvector_test.go` boots `pgvector/pgvector:pg17` with testcontainers. CI plugin tests use pgvector pg15 separately.
- Plugin config is migrated to the plugin DB on activation. For automation, use `GET`/`PUT /plugins/mattermost-ai/admin/config` instead of patching Mattermost server config.
- The embedded MCP server requires `SiteURL`, uses in-memory transport, and skips later duplicate tool names with a warning.
- `public/` is intentionally excluded from plugin bundles by clearing `HAS_PUBLIC`.
- `loadtest/controller/` is a nested Go module; use the loadtest Make targets and `make check-go-mods` for drift.

## Pull requests and commits

- Commit subject: one succinct line. Optional Jira prefix (`MM-12345:`) or short scope (`fix:`, `docs:`, `webapp:`) is fine.
- Do not add `Co-Authored-By` listing the agent.
- Use `.github/PULL_REQUEST_TEMPLATE.md` for PR bodies, including the `release-note` block or `NONE`.

## Pointers

Read the scoped file when the trigger applies:

- Webapp UI bundle: `webapp/AGENTS.md`.
- Playwright e2e tests: `e2e/AGENTS.md`.
- Plugin lifecycle and cluster events: `server/AGENTS.md`.
- HTTP handlers and auth tiers: `api/AGENTS.md`.
- Config types and legacy migrations: `config/AGENTS.md`.
- Plugin DB schema and Morph migrations: `store/AGENTS.md`.
- Mattermost API and DB wrappers: `mmapi/AGENTS.md`.
- pgvector embeddings: `postgres/AGENTS.md`.
- Telemetry helpers and attributes: `telemetry/AGENTS.md`.
- Prometheus metrics: `metrics/AGENTS.md`.
- LLM provider stack, prompts, context building: `llm/AGENTS.md`.
- Generation orchestration, conversation entities, streaming: `conversations/AGENTS.md`.
- Conversation entity, streaming, prompt/provider subpackages have local pointer files when editing directly in those directories.
- MCP client and dynamic tools: `mcp/AGENTS.md`.
- MCP server/tools: `mcpserver/AGENTS.md`.
- Built-in non-MCP tools: `mmtools/AGENTS.md`.
- Generic LLM tool loop: `toolrunner/AGENTS.md`.
- Cross-plugin MCP library: `external/pluginmcp/AGENTS.md`.
- LLM-facing Mattermost entity formatting: `format/AGENTS.md`.
- Semantic search, embeddings, indexing, pgvector pipeline: `search/AGENTS.md`.
- Embeddings, indexer, chunking, and subtitle subpackages have local pointer files when editing directly in those directories.
- Bot lifecycle and agent registry: `bots/AGENTS.md`.
- Meeting transcription and summarization: `meetings/AGENTS.md`.
- File content reads for tools/prompts: `files/AGENTS.md`.
- Channel/thread analysis: `channels/AGENTS.md`.
- External web search providers: `websearch/AGENTS.md`.
- Prompt evals and evalviewer: `evals/AGENTS.md`; human env examples remain in `cmd/evalviewer/README.md`.
- Load testing and mock LLM: `loadtest/AGENTS.md`; operator docs live in `docs/load-testing.md`.
- Public bridge client and hook types: `public/AGENTS.md`.
- Human documentation: `docs/AGENTS.md`.
- Build, manifest, deploy tooling: `build/AGENTS.md`.
- Server-side i18n: `i18n/AGENTS.md`.
