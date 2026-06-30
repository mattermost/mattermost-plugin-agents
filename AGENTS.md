# AGENTS.md

> Canonical agent instructions for this repository. Humans should read `README.md` and `docs/`.
>
> **`AGENTS.md` is the single source of truth.** Every `CLAUDE.md` in this repo is a thin `@AGENTS.md` import — never put content in a `CLAUDE.md`. When you add or edit an `AGENTS.md`, create/keep a sibling `CLAUDE.md` containing only `@AGENTS.md`.
>
> Guidance is **cumulative and nearest-wins**: this root file applies everywhere; a nested `AGENTS.md` adds local deltas for its subtree. Read the nested file for the area you're editing (see [Nested AGENTS.md files](#nested-agentsmd-files)).

## Project

Mattermost server plugin (`mattermost-ai`) that integrates LLM providers. Go 1.26 backend + React/TypeScript webapp (Node 24.11).

## Commands

`make help` lists every documented target with a one-line description.

- Pre-PR aggregate (lint + unit tests + e2e shard coverage + i18n/lockfile drift; **recommended**): `make check`
- Lint with auto-fix (also re-extracts i18n strings): `make check-style-fix`
- Lint only: `make check-style`
- All unit tests: `make test`
- Single Go test: `go test -v ./<pkg> -run TestName`
- Build & deploy plugin to a running Mattermost: `make deploy`
- E2E (self-contained, no env setup needed; slow, defer to CI when possible): `make e2e`
- Single e2e spec: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list`
- Prompt evals (non-interactive): `make evals-ci`
  Provider: `LLM_PROVIDER=openai|anthropic|azure|openaicompatible|all make evals-ci` (note: `all` runs openai/anthropic/azure/mistral/bedrock/cohere — it does **not** include `openaicompatible`, which must be requested explicitly)
  Model: `ANTHROPIC_MODEL=claude-sonnet-4-5-20250929 make evals-ci`
- Streaming benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`

When `make check` fails, run the underlying targets individually (`make check-style`, `make test`, `make check-shards`, `make check-i18n`, `make check-locks`, `make check-go-mods`) to isolate which step broke. CI runs the same drift checks; if i18n, a lockfile, or a nested `go.mod` is out of sync, those targets regenerate/tidy the file in place — review and commit. Note `make check-style-fix` is not a strict superset of `check-style` (it skips the webapp ESLint pass, the license-header vet, and the loadtest-controller lint).

## Repository layout

Most Go packages live at the **repo root**, not under `server/`.

- `server/` — plugin entrypoint, lifecycle, configuration adapter.
- `api/`, `mmapi/` — HTTP handlers; Mattermost API wrappers.
- `llm/` — LLM provider abstractions and provider implementations.
- `mcp/`, `mcpserver/` — MCP client; embedded/HTTP/stdio MCP servers and tools.
- `format/` — formatting of Mattermost entities for LLM consumption (see Conventions).
- Other top-level feature packages exist (e.g. `bots/`, `channels/`, `threads/`, `meetings/`, `search/`, `embeddings/`, `streaming/`, `toolrunner/`, `websearch/`, …). Read the package name and skim the package source before assuming purpose — note in particular that both `conversation/` and `conversations/` exist and are not the same.
- `config/` — plugin configuration types and migration.
- `webapp/` — React/TypeScript UI bundle (`webapp/src/`).
- `e2e/` — Playwright + Testcontainers end-to-end tests.
- `evals/`, `cmd/evalviewer/` — prompt evaluation harness and TUI.
- `i18n/` — server-side, hand-curated translation strings (webapp strings live in `webapp/src/i18n/` and are auto-extracted).
- `docs/` — user/admin docs.
- `public/bridgeclient/` — Go client published for other plugins. It is a subpath of the **root** module (no separate `go.mod`), but is intentionally excluded from the plugin tarball (`HAS_PUBLIC` cleared).

## Conventions

Linters (golangci-lint, ESLint, gofmt/goimports, header check, editorconfig) already enforce formatting, imports, error checking, license headers, and indentation. The rules below are the ones a linter cannot enforce.

- File names: `snake_case.go` / `snake_case.ts(x)`.
- TypeScript/React: PascalCase components, strict typing, **always styled-components**, never inline `style={{...}}`.
- New user-facing strings must go through i18n (`make i18n-extract` picks them up).
- Go tests must be table-driven when there is more than one case.
- Never introduce a new test/mocking library; prefer to test against real implementations instead.
- All formatting of Mattermost entities (posts, users, channels, teams, members) for LLM consumption or tool output must go through the `format/` package. Never `fmt.Sprintf` model types inline; add a formatter to `format/` instead.
- E2E shard maintenance: when adding a new spec that should run in CI, assign it in `e2e/scripts/ci-test-groups.mjs` in the same change. `make check-shards` validates coverage and is part of `make check`. Mock/non-real-api tests go in the lightest `e2e-shard-*` group; provider-backed tests go in the matching `*-real*` group; balance by expected runtime, not alphabetically.
- Test for behavior that could break due to a real bug. Before writing a test ask: "If this test fails, does it indicate a real bug in our code?" In particular, do not assert on implementation details like validation order or which error appears first.

## OpenTelemetry tracing

The plugin emits OpenTelemetry traces. Agent-relevant rules:

- **Thread `ctx context.Context` as the first parameter** through every entry point → LLM call code path. Don't introduce `context.Background()` shortcuts in production code; the request-scoped context is what makes spans correlate.
- Existing spans live in `bifrost/` (LLM calls), `llm/tools.go`, `toolrunner/`, `conversations/tool_approval.go`, `mcp/` (`"mcp call tool"`), `search/`, `websearch/`, and `streaming/`. The `otelgin` middleware adds HTTP spans automatically.
- To add a span: `ctx, span := telemetry.Tracer().Start(ctx, "span name", trace.WithAttributes(...))`, then `defer span.End()`. Record errors with `span.RecordError(err)` and `span.SetStatus(codes.Error, msg)`. Reuse attribute keys from `telemetry/attributes.go` instead of inventing new ones.
- When a `*llm.Context` parameter would shadow the `context` package in the same file, import `"context"` as `stdcontext`.
- Config fields (`TelemetryOutput`, `OpenTelemetryEndpoint`) and local Tempo/Grafana setup live in `docs/admin_guide.md`.

## Never do

- Never edit `webapp/dist/`, `server/dist/`, or `dist/` — regenerate with the build commands above.
- Never hand-edit `webapp/src/i18n/en.json` — `make check-style-fix` re-extracts it from webapp source. Add the user-facing string at the call site instead. (Server-side `i18n/en.json` is hand-curated; mmgotool extraction doesn't apply to this repo's `nicksnyder/go-i18n` setup.)
- Never push to `master`; open a PR.

## Gotchas

- If `make install-go-tools` fails to build `mattermost-govet`, the pinned commit is incompatible with the active Go toolchain. The Makefile prints the exact fix: bump `MATTERMOST_GOVET_VERSION` in the Makefile to a newer commit. This is a real problem to fix, not a warning to ignore.
- `postgres/pgvector_test.go` boots its own pgvector container via `testcontainers-go` (`pgvector/pgvector:pg17`); `go test ./postgres/...` works on a fresh checkout as long as Docker is available. To run against an existing pgvector instance for fast iteration, set `PGVECTOR_TEST_DSN`.
- Plugin config is migrated to the plugin DB on activation. For automation, read/write `GET`/`PUT /plugins/mattermost-ai/admin/config` rather than patching the Mattermost server config.
- The embedded MCP server requires `SiteURL` to be set on the Mattermost server, and uses in-memory transport (no HTTP). On tool name collisions across MCP servers, first-registered wins; later duplicates are skipped with a warning.
- `public/bridgeclient/` is a Go client published for other plugins (a subpath of the root module, **not** a separate `go.mod`), not HTTP assets; `HAS_PUBLIC` is intentionally cleared in the Makefile so `public/` is not bundled. Its streaming API exposes root `llm` types — breaking them breaks external consumers.
- `conversation/` (singular) is the persistence/entity model (turns, content blocks, completion-request assembly); `conversations/` (plural) is the runtime orchestration layer (hooks, DM/mention flows, tool approval, streaming). Edit the right one — see their nested `AGENTS.md`.

## Pull requests and commits

- Commit subject: one succinct line. Optional Jira prefix (`MM-12345:`) or short scope (`fix:`, `docs:`, `webapp:`) is fine.
- Do not add `Co-Authored-By` listing the agent.
- Use the GitHub PR template for the PR body.

## Nested AGENTS.md files

Read the nearest one when you work in that subtree (progressive disclosure — each holds only local deltas). Each has a sibling `CLAUDE.md` that just imports it.

| Path | Scope |
| --- | --- |
| `webapp/AGENTS.md` | React/TS plugin bundle: webpack externals, plugin registry, Redux namespace, FormatJS scope, `make apply` manifest. |
| `e2e/AGENTS.md` | Playwright + Testcontainers; the CI shard registry and new-spec checklist. |
| `evals/AGENTS.md` | Prompt-eval harness (`GOEVALS`, `LLMRubricT`, where eval cases live). `cmd/evalviewer/AGENTS.md` covers the TUI runner. |
| `llm/AGENTS.md` | Provider abstraction: `LanguageModel`, wrapper order, stream-event contract, truncation, `llm.Context` vs `context.Context`. |
| `bifrost/AGENTS.md` | Bifrost gateway adapter: Responses-vs-Chat routing, fallbacks, key redaction. |
| `streaming/AGENTS.md` | Streaming `TextStreamResult` to Mattermost posts/websockets/turns. |
| `prompts/AGENTS.md` | Embedded `*.tmpl` system prompts + `go generate` constants. |
| `llmcontext/AGENTS.md` | Building `*llm.Context`; MCP tool filtering and interactive gating. |
| `mcp/AGENTS.md` | MCP client: transports, tool namespacing, caching, OAuth, dynamic loading. |
| `mcpserver/AGENTS.md` | MCP servers + tools: server types, config-vs-runtime, AccessMode, adding tools. |
| `mmtools/AGENTS.md`, `toolrunner/AGENTS.md` | Built-in (non-MCP) tools; the tool-call execution loop. |
| `external/pluginmcp/AGENTS.md` | Helper for other plugins to register MCP tools (see its README). |
| `format/AGENTS.md` | The mandatory formatters for Mattermost entities. |
| `postgres/AGENTS.md`, `store/AGENTS.md` | pgvector schema-as-code; plugin SQL + Morph migrations. |
| `embeddings/AGENTS.md`, `search/AGENTS.md`, `indexer/AGENTS.md`, `chunking/AGENTS.md` | The RAG pipeline (embed → store → index → search). |
| `server/AGENTS.md`, `api/AGENTS.md`, `config/AGENTS.md` | Plugin composition root; Gin HTTP surface; config types + migrations. |
| `bots/AGENTS.md` | Agent/bot registry and `EnsureBots` invariants. |
| `conversation/AGENTS.md`, `conversations/AGENTS.md` | Entity model vs runtime orchestration (the singular/plural split). |
| `meetings/AGENTS.md` | Call transcription/summarization (ffmpeg-gated). |
| `telemetry/AGENTS.md` | Tracer setup + the shared attribute-key registry. |
| `public/bridgeclient/AGENTS.md` | The external LLM Bridge API client contract. |
| `loadtest/AGENTS.md` | In-process mock LLM + the nested load-test controller module. |
| `enterprise/AGENTS.md` | Runtime license gating (no build tags). |

Other docs to read when the trigger applies:

- Configuring providers, agents, or the admin UI: `docs/admin_guide.md`.
- Prompt evals / eval harness internals: `cmd/evalviewer/README.md`.
