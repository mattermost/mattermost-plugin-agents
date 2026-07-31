# AGENTS.md

> Canonical agent instructions for this repository. Humans should read `README.md` and `docs/`. `CLAUDE.md` and `mcpserver/CLAUDE.md` are thin imports — do not duplicate content there.

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
  Provider: `LLM_PROVIDER=openai|anthropic|azure|openaicompatible|all make evals-ci`
  Model: `ANTHROPIC_MODEL=claude-sonnet-4-5-20250929 make evals-ci`
- Streaming benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`

When `make check` fails, run the underlying targets individually (`make check-style`, `make test`, `make check-shards`, `make check-i18n`, `make check-locks`) to isolate which step broke. CI runs the same drift checks; if i18n or a lockfile is out of sync, those targets regenerate the file in place — review and commit.

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
- `i18n/` — extracted translation strings.
- `docs/` — user/admin docs.
- `public/bridgeclient/` — separate Go module published for other plugins.

## Conventions

Linters (golangci-lint, ESLint, gofmt/goimports, header check, editorconfig) already enforce formatting, imports, error checking, license headers, and indentation. The rules below are the ones a linter cannot enforce.

- File names: `snake_case.go` / `snake_case.ts(x)`.
- TypeScript/React: PascalCase components, strict typing, **always styled-components**, never inline `style={{...}}`.
- New user-facing strings must go through i18n (`make i18n-extract` picks them up).
- Go tests must be table-driven when there is more than one case.
- Never introduce a new test/mocking library; prefer to test against real implementations instead.
- All formatting of Mattermost entities (posts, users, channels, teams, members) for LLM consumption or tool output must go through the `format/` package. Never `fmt.Sprintf` model types inline; add a formatter to `format/` instead.
- E2E shard maintenance: when adding a new spec that should run in CI, assign it in `e2e/scripts/ci-test-groups.mjs` in the same change. `make check-shards` validates coverage and is part of `make check`. Use the lightest `e2e-shard-*` group and balance by expected runtime, not alphabetically.
- Test for behavior that could break due to a real bug. Before writing a test ask: "If this test fails, does it indicate a real bug in our code?" In particular, do not assert on implementation details like validation order or which error appears first.

## OpenTelemetry tracing

The plugin emits OpenTelemetry traces. Agent-relevant rules:

- **Thread `ctx context.Context` as the first parameter** through every entry point → LLM call code path. Don't introduce `context.Background()` shortcuts in production code; the request-scoped context is what makes spans correlate.
- Existing spans live in `bifrost/` (LLM calls), `llm/tools.go`, `conversations/tool_handling.go`, `mcp/`, `search/`, `websearch/`, and `streaming/`. The `otelgin` middleware adds HTTP spans automatically.
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
- `public/bridgeclient/` is a separately published Go module, not HTTP assets; `HAS_PUBLIC` is intentionally cleared in the Makefile.

## Pull requests and commits

- Commit subject: one succinct line. Optional Jira prefix (`MM-12345:`) or short scope (`fix:`, `docs:`, `webapp:`) is fine.
- Do not add `Co-Authored-By` listing the agent.
- Use the GitHub PR template for the PR body.

## Pointers

Read these only when the trigger applies:

- Working inside `mcpserver/` (config-vs-runtime, search service wiring, adding optional capabilities): `mcpserver/AGENTS.md`.
- Configuring providers, agents, or the admin UI: `docs/admin_guide.md`.
- When working on prompt evals or modifying the eval harness: `cmd/evalviewer/README.md`.

## Cursor Cloud specific instructions

These notes apply to the dashboard-managed multi-repo Cloud environment ("Agents Plus Server"). It clones `mattermost`, `enterprise`, and `mattermost-plugin-agents` as siblings under `/agent/repos/`. Go 1.26.4, Node 24.11.1, and Docker are baked into the snapshot; the startup update script only refreshes Go modules and `node_modules` for the three repos, so do not add toolchain installs to it.

- Node version gotcha: the exec-daemon prepends `/exec-daemon` (an older Node, currently v22) to `PATH`, which would shadow nvm. Node 24.11.1 is made effective via symlinks in `/usr/local/cargo/bin` (first in `PATH`). If `node --version` unexpectedly reports v22, recreate them: `ln -sfn ~/.nvm/versions/node/v24.11.1/bin/node /usr/local/cargo/bin/node` (repeat for `npm`, `npx`). The webapp requires Node `^24` but `engine-strict` is off, so a wrong version only warns.
- Docker: the daemon is enabled but may need a nudge per boot — `sudo service docker start`. For non-root access from a shell that predates the group change, run `sudo chmod 666 /var/run/docker.sock` (freshly opened login shells already have the `docker` group).
- Run the whole stack from source (validated end to end): `cd /agent/repos/mattermost/server && ENABLED_DOCKER_SERVICES='postgres redis' make run-server` (details in `mattermost/.cursor/cursor.md`). Local mode is enabled, so seed an admin with `./bin/mmctl --local user create ...` and `team create`. Deploy this plugin against it with `MM_LOCALSOCKETPATH=/var/tmp/mattermost_local.socket ./build/bin/pluginctl deploy mattermost-ai dist/*.tar.gz` (build the bundle first with `make dist-ci`).
- Configure an agent without the UI by writing to `PUT /plugins/mattermost-ai/admin/config` and `POST /plugins/mattermost-ai/agents` using the `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` secrets — full curl recipe in `cursor_cloud_supplement.md`.
- Agent replies stream over websocket and may not render immediately in a browser session opened via automation; the reply is persisted, so reload the page (or read the channel via the REST API) to see it.
