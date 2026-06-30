# AGENTS.md

> Canonical agent instructions. Humans should read `README.md`, `docs/`, and `.github/CONTRIBUTING.md`.
> `CLAUDE.md` files are compatibility stubs only; keep them to a single `@./AGENTS.md` import.
> Source-of-truth versions: `go.mod`, `.nvmrc`, and `plugin.json`. Plugin ID: `mattermost-ai`.

## Project

Mattermost server plugin that integrates LLM providers. Backend code is Go; the webapp is React/TypeScript.

## Commands

`make help` lists every documented target.

- Pre-PR aggregate: `make check` (lint, unit tests, e2e shard coverage, i18n/lockfile/module drift).
- Lint with auto-fix: `make check-style-fix`.
- Lint only: `make check-style`.
- All unit tests: `make test`.
- Single Go test: `go test -v ./<pkg> -run TestName`.
- Build and deploy to a running Mattermost: `make deploy`.
- E2E: `make e2e` (slow; details in `e2e/AGENTS.md`).
- Single e2e spec: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list --project=chromium`.
- Prompt evals: `make evals-ci` (provider details in `evals/AGENTS.md`).
- MCP server evals: `make mcp-evals`.
- Standalone MCP dev binary: `make mcp-server`.
- Streaming/LLM benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`.

When `make check` fails, isolate with `make check-style`, `make test`, `make check-shards`, `make check-i18n`, `make check-locks`, and `make check-go-mods`.

## Repository layout

Most Go packages live at the repo root, not under `server/`.

- `server/` — plugin binary, activation, config migration, service wiring.
- `api/`, `mmapi/` — plugin HTTP handlers and Mattermost API/database wrappers.
- `llm/`, `bifrost/` — LLM abstractions and production gateway.
- `conversation/`, `conversations/` — persisted conversation entity layer vs Mattermost orchestration.
- `mcp/`, `mcpserver/`, `external/pluginmcp/` — MCP client, server/tooling, and external plugin SDK.
- `search/`, `embeddings/`, `postgres/`, `indexer/` — semantic search/RAG stack.
- `streaming/`, `toolrunner/`, `websearch/` — response streaming, tool loop, web search providers.
- `format/` — formatting of Mattermost entities for LLM consumption and tool output.
- `webapp/`, `e2e/`, `evals/`, `cmd/evalviewer/` — UI, Playwright, prompt eval harness, eval CLI/TUI.
- `config/`, `store/`, `i18n/` — plugin config schema, plugin DB persistence, server catalogs.
- `loadtest/controller/` — nested Go module for load-test controller code.
- `public/bridgeclient/`, `public/mcptool/` — exported integration packages, not HTTP assets.

## Conventions

- File names: `snake_case.go` / `snake_case.ts(x)`.
- Go tests are table-driven when there is more than one case.
- Do not add test/mocking libraries; prefer real implementations and existing helpers.
- Test behavior that would indicate a real bug if it fails, not validation order or incidental implementation detail.
- Format Mattermost posts, users, channels, teams, and members through `format/`; never inline `fmt.Sprintf` model formatting.
- New e2e specs must be assigned in `e2e/scripts/ci-test-groups.mjs` in the same change; run `make check-shards`.
- For OpenTelemetry, thread request `ctx context.Context` through entry points and reuse keys from `telemetry/attributes.go`; scoped details and exceptions live in `telemetry/AGENTS.md`.

## Never do

- Never edit `webapp/dist/`, `server/dist/`, `dist/`, `mcpserver/bin/`, `build/bin/`, or `evals.jsonl`; regenerate them.
- Never hand-edit `webapp/src/i18n/en.json`; add strings at call sites and run extraction.
- Never use stale `webapp/i18n/en.json`; canonical webapp catalog is `webapp/src/i18n/en.json`.
- Never push to `master`; open a PR.

## Gotchas

- If `make install-go-tools` fails to build `mattermost-govet`, bump `MATTERMOST_GOVET_VERSION` as the Makefile instructs.
- Plugin config is migrated to the plugin DB on activation; automation should use `GET`/`PUT /plugins/mattermost-ai/admin/config`.
- The embedded MCP server requires Mattermost `SiteURL` and uses in-memory transport.
- Tool name collisions across MCP servers are resolved by first registration in the plugin aggregate.
- `public/bridgeclient/` is a published import path from this module; `HAS_PUBLIC` is intentionally cleared in the Makefile.

## Pull requests and commits

- Before opening a PR, run `make check` or the closest scoped checks for your change.
- Commit subject: one succinct line; optional Jira prefix or short scope is fine.
- Do not add `Co-Authored-By` for agents.
- Use the GitHub PR template for the PR body.

## Pointers

Read only when the trigger applies:

- Plugin lifecycle/config wiring: `server/AGENTS.md`.
- HTTP handlers and bridge routes: `api/AGENTS.md`.
- Mattermost API/database wrappers: `mmapi/AGENTS.md`.
- Config schema and legacy migrations: `config/AGENTS.md`.
- Plugin DB store and migrations: `store/AGENTS.md`.
- LLM abstractions/providers: `llm/AGENTS.md`.
- Bifrost gateway/fallbacks: `bifrost/AGENTS.md`.
- Streaming responses: `streaming/AGENTS.md`.
- Tool-call loop: `toolrunner/AGENTS.md`.
- Conversation entity layer: `conversation/AGENTS.md`.
- Agent orchestration, approval, regeneration: `conversations/AGENTS.md`.
- Semantic search/RAG/pgvector: `search/AGENTS.md`.
- Embedding pipeline: `embeddings/AGENTS.md`.
- pgvector test storage: `postgres/AGENTS.md`.
- Background indexing: `indexer/AGENTS.md`.
- OpenTelemetry: `telemetry/AGENTS.md`.
- MCP client/OAuth/dynamic tools: `mcp/AGENTS.md`.
- MCP server/tools/transports: `mcpserver/AGENTS.md`.
- External plugin MCP SDK: `external/pluginmcp/AGENTS.md`.
- Bridge client: `public/bridgeclient/AGENTS.md`.
- Webapp UI: `webapp/AGENTS.md`.
- E2E specs/shards: `e2e/AGENTS.md`.
- Prompt eval harness: `evals/AGENTS.md`.
- Evalviewer CLI/TUI: `cmd/evalviewer/AGENTS.md`.
- Server/webapp i18n split: `i18n/AGENTS.md`.
- Load testing: `docs/load-testing.md` and `loadtest/controller/AGENTS.md`.
- Provider/admin config: `docs/admin_guide.md`.
