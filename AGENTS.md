# AGENTS.md

> Canonical agent instructions for this repository. Humans should read `README.md` and `docs/`. Every `CLAUDE.md` is a thin `@AGENTS.md` import — don't duplicate content into it. This root file holds repo-wide rules; subsystem detail lives in nested `AGENTS.md` files (see Nested guides).

## Project

Mattermost server plugin (plugin id `mattermost-ai`; Go module `github.com/mattermost/mattermost-plugin-agents`) that integrates LLM providers. Go 1.26 backend + React/TypeScript webapp (Node 24.11). Most Go packages live at the **repo root**, not under `server/`.

## Commands

`make help` lists every documented target.

- Pre-PR aggregate (lint + unit tests + e2e shard coverage + i18n/lockfile/Go-module drift; **recommended**): `make check`
- Lint with auto-fix (also re-extracts webapp i18n): `make check-style-fix`
- Lint only: `make check-style`
- All unit tests (Go + webapp jest + loadtest-controller module): `make test`
- Single Go test: `go test -v ./<pkg> -run TestName`
- Build & deploy to a running Mattermost: `make deploy`
- E2E (Playwright + Testcontainers; needs Docker; slow, defer to CI): `make e2e` — see `e2e/AGENTS.md`
- Prompt evals: `make evals-ci` (CI itself runs `make evals-comment`) — see `evals/AGENTS.md`
- Streaming benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`

When `make check` fails, run the underlying targets to isolate the break: `make check-style`, `make test`, `make check-shards`, `make check-i18n`, `make check-locks`, `make check-go-mods`. CI runs the same drift checks; drift targets regenerate the file in place — review and commit.

## Conventions

Linters (golangci-lint incl. gofmt/goimports formatters, ESLint, mattermost-govet license check) enforce formatting, imports, error checking, and license headers. `.editorconfig` is advisory (IDE only). The rules below are the ones a linter does **not** enforce:

- File names: `snake_case.go` / `snake_case.ts(x)`.
- TypeScript/React: PascalCase components, strict typing, styled-components only, never inline `style={{…}}` (convention, not lint-enforced).
- User-facing strings go through i18n. For webapp, add the string at the call site in `src/components/**` and run `make i18n-extract`; only ever edit `en.json` (never other language catalogs).
- Go tests are table-driven when there is more than one case.
- Don't add new test/mocking libraries. Interface mocks are generated with **mockery** (`make mock`, `.mockery.yml`) under `llm/mocks/`, `mmapi/mocks/`, `embeddings/mocks/`; prefer real implementations otherwise.
- All formatting of Mattermost entities for LLM/tool consumption goes through `format/` — never `fmt.Sprintf` model types inline. See `format/AGENTS.md`.
- E2E shard maintenance: assign new CI specs in `e2e/scripts/ci-test-groups.mjs` in the same change (`make check-shards` validates). See `e2e/AGENTS.md`.
- Test behavior that could break from a real bug; don't assert implementation details like validation order.

## OpenTelemetry tracing

- Thread `ctx context.Context` as the first parameter through every entry point → LLM call path. Don't introduce `context.Background()` shortcuts in production code.
- Add spans with `ctx, span := telemetry.Tracer().Start(ctx, "name", trace.WithAttributes(...))` then `defer span.End()`; record errors with `span.RecordError(err)` / `span.SetStatus(codes.Error, msg)`. Reuse attribute keys from `telemetry/attributes.go`.
- Existing spans live in `bifrost/`, `llm/tools.go`, `conversations/` (`tool_approval.go`, `handle_messages.go`, `regeneration.go`), `mcp/`, `toolrunner/`, `search/`, `websearch/`, and `streaming/`. The `otelgin` middleware adds HTTP spans automatically.
- When a `*llm.Context` parameter would shadow the `context` package in a file, import `"context"` as `stdcontext`.
- Config (`TelemetryOutput`, `OpenTelemetryEndpoint`) and local Tempo/Grafana setup live in `docs/admin_guide.md`.

## Never do

- Never edit generated/build output: `webapp/dist/`, `server/dist/`, `dist/`, `webapp/src/manifest.ts`.
- Never hand-edit `webapp/src/i18n/en.json` (re-extracted by `make check-style-fix`). The server-side `i18n/en.json` is hand-curated (this repo uses `nicksnyder/go-i18n`, not mmgotool extraction).
- Never push to `master`; open a PR.

## Gotchas

- If `make install-go-tools` fails to build `mattermost-govet`, the pinned commit is incompatible with the active Go toolchain. Bump `MATTERMOST_GOVET_VERSION` in the Makefile (the target prints the fix) — a real fix, not a warning to ignore.
- Plugin config is migrated to the plugin DB on activation; the Mattermost server config is **not** the live source afterward. For automation, read/write `GET`/`PUT /plugins/mattermost-ai/admin/config`. See `config/AGENTS.md`.
- The plugin won't activate if Mattermost `SiteURL` is unset (`OnActivate` errors). The embedded MCP server also needs it. See `server/AGENTS.md`.

## Pull requests and commits

- Commit subject: one succinct line. Optional Jira prefix (`MM-12345:`) or short scope (`fix:`, `docs:`, `webapp:`).
- Don't add `Co-Authored-By` listing the agent.
- Use the GitHub PR template (`.github/PULL_REQUEST_TEMPLATE.md`) for the PR body.

## Nested guides

Read the nearest `AGENTS.md` to the code you're editing; the closest file wins. Subsystem guides (read only when the trigger applies):

- `webapp/AGENTS.md` — React/TS bundle: commands, host integration, generated manifest, i18n scope.
- `e2e/AGENTS.md` — Playwright/Testcontainers harness, container runners, mock LLM, CI shards.
- `llm/AGENTS.md` — LLM types/tools/wrappers + `bifrost/`, `streaming/`, `llmcontext/`; adding a provider.
- `search/AGENTS.md` — semantic search / embeddings / pgvector / chunking / indexer cluster.
- `mcp/AGENTS.md` — MCP client + `toolrunner/` + `mmtools/`; dynamic tool loading.
- `mcpserver/AGENTS.md` — embedded/HTTP/stdio MCP servers and tools (config-vs-runtime, capabilities).
- `conversation/AGENTS.md` — `conversation/` vs `conversations/` (key footgun) + `threads/`, `channels/`, `meetings/`.
- `bots/AGENTS.md` — bot/agent lifecycle, LLM construction, access control, licensing.
- `config/AGENTS.md` — config types, DB-backed live config, legacy migrations.
- `server/AGENTS.md` — activation/lifecycle wiring + `mmapi/`, `store/`, `telemetry/`, `metrics/`.
- `api/AGENTS.md` — Gin HTTP routing, auth middleware layering, adding endpoints.
- `evals/AGENTS.md` — eval harness (`GOEVALS`, providers, grading); CLI in `cmd/evalviewer/README.md`.
- `prompts/AGENTS.md` — `text/template` prompts, `go generate` for `prompts_vars.go`.
- `loadtest/AGENTS.md` — mock LLM + the nested `loadtest/controller` Go module.
- `format/AGENTS.md` — entity formatter API and conventions.
- `public/AGENTS.md` — `bridgeclient`/`mcptool` import subpackages (backward-compat contract).
- `external/pluginmcp/README.md` — external plugin MCP integration.
- `docs/admin_guide.md` — configuring providers, agents, the admin UI, telemetry.
