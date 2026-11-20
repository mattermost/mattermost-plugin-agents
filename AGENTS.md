# AGENTS.md

> Canonical agent instructions for this repository. Humans should read `README.md` and `docs/`. `CLAUDE.md` and every nested `<dir>/CLAUDE.md` are thin imports — do not duplicate content there.

## Project

Mattermost server plugin (`mattermost-ai`) that integrates LLM providers. Go backend (`go.mod`) + React/TypeScript webapp (`.nvmrc`).

## Commands

`make help` lists every documented target with a one-line description.

- Pre-PR aggregate (lint + unit tests + e2e shard coverage + i18n/lockfile/nested-go-mod drift; **recommended**): `make check`
- Lint with auto-fix (also re-extracts webapp i18n strings): `make check-style-fix`
- Lint only (includes `webapp` `check-editor-contract` and `nested-modules-lint`): `make check-style`
- All unit tests: `make test`
- Single Go test: `go test -v ./<pkg> -run TestName`
- Build & deploy plugin to a running Mattermost: `make deploy`
- E2E (self-contained, no env setup needed; slow, defer to CI when possible): `make e2e`
- Single e2e spec: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list`
- Prompt evals (non-interactive): `make evals-ci` (includes `./mcpserver`, which boots Mattermost containers and needs Docker)
  Provider: `LLM_PROVIDER=openai|anthropic|azure|mistral|bedrock|cohere|openaicompatible|all make evals-ci`
  Model: `ANTHROPIC_MODEL=claude-opus-5-5 make evals-ci`
- Streaming benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`

When `make check` fails, run the underlying targets individually (`make check-style`, `make test`, `make check-shards`, `make check-i18n`, `make check-locks`, `make check-go-mods`) to isolate which step broke. CI runs the same drift checks; if i18n or a lockfile is out of sync, those targets regenerate the file in place — review and commit.

## Repository layout

Most Go packages live at the **repo root**, not under `server/`.

- `server/` — plugin entrypoint, lifecycle, configuration adapter.
- `api/`, `mmapi/` — HTTP handlers; Mattermost API wrappers.
- `store/` — PostgreSQL persistence and Morph schema migrations (`store/migrations/`).
- `config/` — plugin configuration types and migration.
- `llm/`, `bifrost/` — LLM provider abstractions and Bifrost request pipeline.
- `conversation/` — conversation/turn persistence, content blocks, and history conversion (`conversation.Service`).
- `conversations/` — turn orchestration lifecycle (DMs, mentions, auto-reply, tool approval, regeneration, wakeups).
- `mcp/`, `mcpserver/`, `mmtools/` — MCP client; embedded/HTTP/stdio MCP servers; built-in non-MCP tools (e.g. `create_file`).
- `format/` — formatting of Mattermost entities for LLM consumption (see Conventions).
- `accesscontrol/`, `audit/`, `telemetry/` — ABAC checks, server audit logging, and OpenTelemetry tracing.
- Other top-level feature packages: `bots/`, `channels/`, `threads/`, `meetings/`, `search/`, `embeddings/`, `streaming/`, `toolrunner/`, `websearch/`, `autoreply/`, `skills/`, `customprompts/`, `prompts/`, `files/`.
- `webapp/` — React/TypeScript UI bundle (`webapp/src/`).
- `e2e/` — Playwright + Testcontainers end-to-end tests.
- `evals/`, `cmd/evalviewer/` — prompt evaluation harness and TUI.
- `i18n/` — hand-curated server-side translation strings (`nicksnyder/go-i18n`; not auto-extracted).
- `docs/` — user/admin docs.
- `public/bridgeclient/` — Go package other plugins import (**frozen public API**); part of the root module (no own `go.mod`), not HTTP assets (`HAS_PUBLIC` is intentionally cleared in `Makefile`).

## Conventions

Linters (`golangci-lint`, ESLint, `gofmt`/`goimports`, header check, editorconfig, `build/govet` running `httpclienttimeout` + upstream `mattermost-govet` analyzers, `testifylint` `go-require`, `webapp/eslint-plugin-agents`, and `e2e/eslint-rules`) enforce formatting, imports, error checking, license headers, test-goroutine assertions, timeout-less `http.Client`, and a11y/e2e guards. Suppress a legitimate case on that line with the reason: `//nolint:httpclienttimeout // <reason>` or `// eslint-disable-next-line <rule> -- <reason>`. The rules below are the ones linters cannot enforce.

- File names: `snake_case.go` / `snake_case.ts(x)`.
- A change that users, admins or integrators would need to know about (a feature, setting, default, tool, permission or license gate) updates the docs in `docs/` in the same PR. Follow `docs/AGENTS.md`.
- Go tests must be table-driven when there is more than one case.
- Never introduce a new test/mocking library; prefer to test against real implementations instead.
- Test-only LLM helpers (mock stream generators, logging wrappers) live in `llm/llmtest`; never import `testing` from a production package.
- All formatting of Mattermost entities (posts, users, channels, teams, members) for LLM consumption or tool output must go through the `format/` package. Never `fmt.Sprintf` model types inline; add a formatter to `format/` instead.
- Test for behavior that could break due to a real bug. Before writing a test ask: "If this test fails, does it indicate a real bug in our code?" In particular, do not assert on implementation details like validation order or which error appears first.
- A bug fix includes a test that fails without the fix and runs through the production wiring (route, hook, registration), not only the helper that changed.

## Correctness and security

- Enforce authorization and request scope in code, never in prompt text: when a request is scoped to a channel, thread, or user, bind or validate that parameter on every tool the request can call.
- Fail closed: when a lookup of an agent, bot, user, tool policy, or license misses or errors, return an error or deny; never fall back to a default or first match, or to a permissive default.
- Propagate store and API errors; never turn them into an empty list, "none", or "not found" result.
- Persist references to agents, bots, services, users, and channels by ID, never by name; names are display-only.
- Errors and stream events sent to clients, and span attributes, never carry raw upstream response bodies, request content, or credentials.
- Apply every change to turn behavior or tool policy (license gates, tool filters, round caps, permissions) to each entry point that starts or resumes a turn: DM message, channel @mention, channel auto-reply, loop-in from the mention reminder, regenerate, tool approve/reject, ask-user answers, sharing tool results, search, thread and channel analysis, meeting summarization, and LLM Bridge agent completions.
- Make state transitions that can race (double clicks, retries, HA peers, background sweeps, regenerate during a stream) conditional on the expected prior state; never blind-write.
- Key per-conversation state (KV keys, claims, caches) by conversation or user, never by a provider-generated ID alone; tool call IDs repeat across conversations.
- Goroutines that relay between channels select on `ctx.Done()` for both send and receive; never hold a mutex across network or DB I/O; bound streaming HTTP calls with a context deadline.
- Recompute derived state (active agents, caches, migrated config) on config change and license change, on every HA node, not only the node that made the change.
- JSON returned to the webapp encodes empty slices as `[]`, not `null`, unless the TS type allows `null`; expose state as an explicit field instead of making the client infer it from an empty value.

## Audit logging

Every state-changing operation must emit a server audit record:

- **New state-changing route?** Add an event constant and registry row in `api/audit_events.go` (`api/audit_middleware.go` emits actor, outcome, and trace ID). Auditing is explicit per-route opt-in — never infer it from the HTTP method, and don't audit read-only routes.
- Enrich with object identifiers only via `audit.AddParam(auditRec(c), ...)` or `audit.RecordFromContext(ctx)`, reusing `audit.Key*` constants and clamping unvalidated values with `audit.TruncateID`/`TruncateIDs`.
- **Never put content in a record**: no prompts, tool args/results, message content, credentials, config values, or free-form error text (fail records carry status codes; keep rich errors in server logs). Test with `e.CaptureAuditRecords()`, asserting fields and verifying sentinel request content is absent from the marshaled record.

## OpenTelemetry tracing

- **Thread `ctx context.Context` as the first parameter** through every entry point → LLM call path; never introduce `context.Background()` shortcuts in production request paths.
- Existing spans live in `bifrost/`, `llm/tools.go`, `conversations/`, `mcp/`, `mmtools/`, `accesscontrol/`, `websearch/`, and `streaming/` (`otelgin` adds HTTP spans automatically).
- Add spans with `ctx, span := telemetry.Tracer().Start(ctx, "span name", trace.WithAttributes(...))` + `defer span.End()`, reusing keys from `telemetry/attributes.go`, and recording errors via `span.RecordError(err)` + `span.SetStatus(codes.Error, msg)`. When a `*llm.Context` parameter shadows `context`, import `"context"` as `stdcontext`. See `docs/observability.md` for config and local Tempo/Grafana setup.

## Never do

- Never edit `webapp/dist/`, `server/dist/`, or `dist/` — regenerate with the build commands above.
- Never push to `master`; open a PR.

## Gotchas

- `postgres/pgvector_test.go` boots its own pgvector container via `testcontainers-go` (`pgvector/pgvector:pg17`); set `PGVECTOR_TEST_DSN` to reuse an existing instance. Packages like `customprompts/`, `autoreply/`, and `api/` accept `PG_ROOT_DSN` for an external Postgres instance.
- Plugin config is migrated to the plugin DB on activation. For automation, read/write `GET`/`PUT /plugins/mattermost-ai/admin/config` rather than patching the Mattermost server config.
- A fresh checkout does not compile: run `make apply` first to generate `server/manifest.go` (`undefined: manifest` errors otherwise).
- The repo has four Go modules: root, `loadtest/controller/`, `cmd/evalviewer/`, and `build/releasenotes/`. `make check` (`NESTED_GO_MODULES`) tests/lints/checks `loadtest/controller/` and `build/releasenotes/`; run `cmd/evalviewer/` checks separately (`(cd cmd/evalviewer && go test ./...)`). Run Go version bumps, `go mod tidy`, and `go fix` across all four.
- `webapp/node_modules/` contains stray Go files that `./...` matches; for sweeping Go commands use `$(go list ./... | grep -v node_modules)`.
- Bumping the Go version: update all four `go.mod` files and `FIPS_IMAGE` in `build/fips.mk` (see `build/fips.mk` for the Chainguard tag-listing and digest-pinning procedure). If `bin/golangci-lint` fails with an export-data version error, bump `GOLANGCI_LINT_VERSION` in `Makefile` and rerun `make install-go-tools`.

## Cursor Cloud specific instructions

- Run e2e with `TESTCONTAINERS_HOST_OVERRIDE=localhost` (e.g. `cd e2e && TESTCONTAINERS_HOST_OVERRIDE=localhost npx playwright test tests/agents/html-artifact.spec.ts --reporter=list`). On Cloud VMs testcontainers reports the Docker gateway IP (e.g. `172.18.0.1`), which browsers treat as a non-secure context and withhold Fetch Metadata (`Sec-Fetch-*`) headers from — breaking HTML artifact and Clipboard API specs.

## Pull requests and commits

- Commit subject: one succinct line. Optional Jira prefix (`MM-12345:`) or short scope (`fix:`, `docs:`, `webapp:`) is fine.
- Do not add `Co-Authored-By` listing the agent.
- Use the GitHub PR template for the PR body.

## Pointers

Read these only when the trigger applies:

- Working inside `store/` or adding SQL migrations (`store/migrations/`): `store/AGENTS.md`.
- Working inside `mcpserver/` (config-vs-runtime, search service wiring, adding optional capabilities): `mcpserver/AGENTS.md`.
- Changing how a turn runs, replays, stops, or regenerates (`conversations/`, `toolrunner/`, `streaming/`, or their `api/` routes): `conversations/AGENTS.md`.
- Building or changing LLM requests (`bifrost/`, or `llm/` types that feed it): `bifrost/AGENTS.md`.
- Working inside `mcp/` (MCP client, OAuth, tool caches): `mcp/AGENTS.md`.
- Working inside `webapp/`: `webapp/AGENTS.md`.
- Writing or editing Playwright specs in `e2e/`: `e2e/AGENTS.md`.
- Writing or editing the user, admin and developer docs in `docs/`: `docs/AGENTS.md`.
- Configuring providers, agents, or the admin UI: `docs/admin_guide.md` (settings map and links to the task pages).
- When working on prompt evals or modifying the eval harness: `cmd/evalviewer/README.md`.
