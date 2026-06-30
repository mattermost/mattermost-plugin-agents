# AGENTS.md

> README for agents. Humans read `README.md` and `docs/`. This file is the canonical, hand-maintained agent guidance — keep it accurate and lean. Package-specific rules live in nested `AGENTS.md` files (see Index); prefer adding guidance next to the code it governs.
>
> Every `CLAUDE.md` in this repo is a one-line `@AGENTS.md` import. Edit the sibling `AGENTS.md`; never put content in a `CLAUDE.md`.

## Project

Mattermost server plugin (id `mattermost-ai`, module `github.com/mattermost/mattermost-plugin-agents`) that integrates LLM providers. Go 1.26 backend + React/TypeScript webapp (Node 24.11). Postgres-only.

## Commands

`make help` lists every documented target.

- Pre-PR aggregate (**recommended**): `make check` → `check-style test check-shards check-i18n check-locks check-go-mods` (skips the slow `make e2e`).
- Lint + type-check: `make check-style`; with auto-fix (also re-extracts webapp i18n): `make check-style-fix`.
- Unit tests (Go + webapp): `make test`. Single Go test: `go test -v ./<pkg> -run TestName`.
- Build & deploy to a running Mattermost: `make deploy`.
- E2E (self-contained, needs Docker; slow, defer to CI): `make e2e`. Single spec: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list`.
- Prompt evals (non-interactive): `make evals-ci` (see `evals/AGENTS.md`).
- Streaming benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`.

When `make check` fails, run the failing sub-target alone to isolate it. CI splits these across jobs; `check-i18n`/`check-locks` regenerate files in place on drift — review and commit the result.

## Repository layout

Most Go packages live at the **repo root**, not under `server/`.

- `server/` — plugin entrypoint, lifecycle, service wiring, config migration.
- `api/`, `mmapi/` — Gin HTTP handlers; Mattermost API + Postgres wrappers.
- `llm/` — provider-agnostic LLM types, tool catalog, prompt renderer, decorator wrappers. **Provider implementations live in `bifrost/`**, not here.
- `bifrost/` — the `llm.LanguageModel` gateway implementation (multi-provider, fallback chains).
- `mcp/`, `mcpserver/` — MCP client; embedded/HTTP/stdio MCP servers and native tools.
- `format/` — formatting of Mattermost entities for LLM consumption (see Conventions).
- `conversation/` (entity service) and `conversations/` (runtime orchestration) are **different packages** — read each before assuming purpose. Other feature packages include `bots/`, `channels/`, `threads/`, `meetings/`, `search/`, `embeddings/`, `streaming/`, `toolrunner/`, `websearch/`, `indexer/`.
- `config/`, `store/` — config types/migration; Postgres persistence.
- `webapp/` — React/TS UI bundle. `e2e/` — Playwright + Testcontainers. `evals/`, `cmd/evalviewer/` — prompt eval harness/TUI.
- `i18n/` — **hand-curated** server strings (`nicksnyder/go-i18n`); not auto-extracted. Webapp strings in `webapp/src/i18n/en.json` ARE auto-extracted.
- `public/bridgeclient/` — Go client library for the LLM Bridge API; a subpackage of the root module (importable by path), not a separate module.
- Nested Go modules (own `go.mod`): `cmd/evalviewer/`, `loadtest/controller/`.

## Conventions

Linters (golangci-lint, ESLint, gofmt/goimports, license-header check, editorconfig) already enforce formatting, imports, error checking, headers, and indentation. The rules below are what a linter cannot.

- File names: `snake_case.go` / `snake_case.ts(x)`.
- TypeScript/React: PascalCase components, strict typing, styled-components (avoid inline `style={{...}}`). See `webapp/AGENTS.md`.
- New user-facing strings go through i18n. Never hand-edit `webapp/src/i18n/en.json` (regenerate via `make check-style-fix`); the server `i18n/en.json` is hand-curated.
- Go tests must be table-driven when there is more than one case.
- Never introduce a new test/mocking library; test against real implementations where possible (existing mockery-generated mocks are fine).
- All formatting of Mattermost entities (posts, users, channels, teams, members, files) for LLM/tool output goes through `format/`. Never `fmt.Sprintf` model types inline; add a `Write*`/entry to `format/`.
- E2E: every spec under `e2e/tests/` must be assigned to a shard (`e2e-shard-1`…`e2e-shard-4`) in `e2e/scripts/ci-test-groups.mjs`; `make check-shards` enforces exact coverage. Balance by expected runtime. See `e2e/AGENTS.md`.
- Test for behavior that could break from a real bug. Don't assert implementation details like validation order or which error appears first.

## OpenTelemetry tracing

Cross-cutting rules only (attribute catalog, span recipe, and span inventory live in `telemetry/AGENTS.md`):

- **Thread `ctx context.Context` as the first parameter** through every entry point → LLM call path. Don't add `context.Background()` shortcuts in production code (plugin hook entrypoints are the documented exception).
- When a `*llm.Context` parameter would shadow the `context` package, import `"context"` as `stdcontext`.
- Reuse attribute keys from `telemetry/attributes.go`; never invent ad-hoc keys.
- Config (`TelemetryOutput`, `OpenTelemetryEndpoint`) and local Tempo/Grafana setup: `docs/admin_guide.md`.

## Never do

- Never edit generated output: `webapp/dist/`, `server/dist/`, `dist/`, `webapp/src/manifest.ts`, `prompts/prompts_vars.go` — regenerate via the build/codegen commands.
- Never hand-edit `webapp/src/i18n/en.json`.
- Never push to `master`; open a PR.

## Gotchas

- If `make install-go-tools` fails to build `mattermost-govet`, the pinned commit is incompatible with the active Go toolchain — bump `MATTERMOST_GOVET_VERSION` in the Makefile (the target prints the fix). A real problem to fix, not a warning to ignore.
- Plugin config migrates to the plugin DB once on activation; afterward read/write via `GET`/`PUT /plugins/mattermost-ai/admin/config`, not the Mattermost `config.json`.
- Activation requires `SiteURL` (embedded MCP + OAuth callbacks).

## Pull requests and commits

- Commit subject: one succinct line. Optional Jira (`MM-12345:`) or scope (`fix:`, `webapp:`) prefix.
- Do not add `Co-Authored-By` for the agent.
- Fill in the GitHub PR template (`#### Summary`, `#### Ticket Link`, `#### Screenshots`, `#### Release Note` — use `NONE` if not needed).

## Index of nested AGENTS.md

Read the matching file before working in that area. Guidance is cumulative — nested files add to, not replace, this one.

- `webapp/AGENTS.md` — React/TS bundle: registry integration, formatjs i18n, redux, tests.
- `api/AGENTS.md` — Gin routes, auth tiers, agent CRUD.
- `server/AGENTS.md` — activation/wiring order, migrations, cluster events.
- `config/AGENTS.md` — `Config`/`Container`, update listeners, legacy migrations.
- `store/AGENTS.md` — Postgres schema, migrations, turn sequencing.
- `llm/AGENTS.md` — `LanguageModel` contract, wrapper chain, tool/MCP naming.
- `bifrost/AGENTS.md` — provider gateway; where "add a provider" actually happens.
- `streaming/AGENTS.md` — streaming to posts, turn persistence, control events.
- `telemetry/AGENTS.md` — OTel init, attribute catalog, span recipe + inventory.
- `prompts/AGENTS.md` — embedded templates + codegen + renderer split.
- `format/AGENTS.md` — entity formatter pattern.
- `conversation/AGENTS.md` — conversation entity / content-block model.
- `conversations/AGENTS.md` — runtime orchestration, tool approval, invoker rules.
- `mcp/AGENTS.md` — MCP client: connections, namespacing, OAuth, dynamic tools.
- `mcpserver/AGENTS.md` — MCP server: transports, service injection, native tools.
- `search/AGENTS.md` — RAG pipeline (search/embeddings/postgres/chunking).
- `indexer/AGENTS.md` — incremental + reindex jobs, KV/cluster locks.
- `bots/AGENTS.md` — bot/agent merge, license caps, `EnsureBots`.
- `e2e/AGENTS.md` — Playwright/Testcontainers, mock layers, sharding.
- `evals/AGENTS.md` — prompt eval harness, providers, `GOEVALS`.

## Pointers

- Providers, agents, admin UI: `docs/admin_guide.md`.
- Prompt eval CLI/TUI: `cmd/evalviewer/README.md`. Load testing: `docs/load-testing.md`.
- LLM Bridge client for other plugins: `public/bridgeclient/README.md`.
