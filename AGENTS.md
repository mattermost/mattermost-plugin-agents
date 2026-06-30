# AGENTS.md

> Canonical coding-agent instructions for this repository. Keep this root file lean and put subsystem details in the nearest nested `AGENTS.md`.

## Instruction files

- `AGENTS.md` is the source of truth. Every `CLAUDE.md` in this repo must contain only `@AGENTS.md`.
- Add or update a nested `AGENTS.md` when guidance applies mainly to one subtree.
- Do not duplicate linter-enforced rules or README prose. Use imperative, checkable instructions and exact commands.
- Do not edit instruction files under `.cursor/`; those are Cloud Agent runtime rules.

## Project

Mattermost Agents plugin (`mattermost-ai`): Go 1.26 server plugin, React/TypeScript webapp (Node 24.11), Playwright/Testcontainers e2e, and prompt eval tooling.

Most Go packages live at the repository root, not under `server/`.

## Commands

Run `make help` for documented targets.

- Pre-PR aggregate: `make check`
- Lint only: `make check-style`
- Lint with safe fixes + i18n extraction: `make check-style-fix`
- Unit tests: `make test`
- Single Go test: `go test -v ./<pkg> -run TestName`
- Build/deploy to a running Mattermost: `make deploy`
- Full e2e: `make e2e`
- Single e2e spec: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list`
- Prompt evals (need API keys; not in `make check`):
  - Interactive: `make evals`
  - Non-interactive gate: `make evals-ci`
  - PR comment generation: `make evals-comment`
  - MCP evals: `make mcp-evals`
  - Provider: `LLM_PROVIDER=openai|anthropic|azure|mistral|bedrock|cohere|openaicompatible|all`
- MCP standalone binary: `make mcp-server`
- Load-test controller module: `make loadtest-controller-build loadtest-controller-lint loadtest-controller-test loadtest-controller-mod-check`
- Streaming benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`

When `make check` fails, run its underlying targets individually: `make check-style`, `make test`, `make check-shards`, `make check-i18n`, `make check-locks`, `make check-go-mods`.

## Repository map

- `server/` — plugin lifecycle and composition root.
- `api/`, `mmapi/` — HTTP handlers and Mattermost API wrappers.
- `config/`, `store/` — DB-backed plugin config and persistence/migrations.
- `llm/`, `bifrost/`, `llmcontext/` — LLM request contracts, provider gateway, prompt/tool context assembly.
- `bots/` — bot and DB-backed agent runtime management.
- `conversation/` — stored conversation/turn entity service.
- `conversations/` — runtime DM/channel orchestration, tool approval, regeneration, streaming handoff.
- `channels/`, `threads/`, `meetings/` — feature-specific analysis and transcription flows.
- `mcp/`, `mcpserver/`, `external/pluginmcp/` — MCP client, Mattermost MCP server/tools, and helper library for other plugins.
- `mmtools/` — built-in Mattermost tools exposed to LLMs.
- `format/` — LLM-facing Mattermost entity formatting. Add formatters here instead of formatting model objects inline.
- `search/`, `embeddings/`, `postgres/`, `indexer/` — semantic search/RAG and pgvector indexing.
- `streaming/`, `toolrunner/`, `websearch/` — post streaming, tool-call loop, web-search providers.
- `webapp/` — React/TypeScript plugin bundle.
- `e2e/` — Playwright + Testcontainers e2e tests.
- `evals/`, `cmd/evalviewer/` — prompt eval harness and CLI/TUI.
- `docs/` — user/admin/operator docs.
- `public/bridgeclient/` — published Go library path in the root module; not plugin HTTP assets.
- `loadtest/controller/` — true nested Go module for mattermost-load-test-ng.

## Repo-wide conventions

- File names: `snake_case.go` and `snake_case.ts(x)`.
- Go tests are table-driven when there is more than one case.
- Test behavior that would represent a real bug if broken; do not assert on incidental implementation order.
- Do not add a new test/mocking library.
- Thread `ctx context.Context` as the first parameter through production entry points to LLM calls. Use `telemetry.DetachContext` for background work that must outlive an HTTP request.
- Reuse OpenTelemetry attribute keys from `telemetry/attributes.go`; do not invent local string keys.
- When `*llm.Context` would shadow the `context` package, import `"context"` as `stdcontext`.
- User-facing webapp strings go through FormatJS extraction. Never hand-edit `webapp/src/i18n/en.json`.
- Server-side `i18n/en.json` is hand-curated; update only US English translation files.

## Never do

- Never edit `webapp/dist/`, `server/dist/`, `dist/`, Playwright reports, or generated test artifacts.
- Never treat `public/` as plugin static assets; `HAS_PUBLIC` is intentionally cleared.
- Never add root-plugin imports to `loadtest/controller/`.
- Never change `public/bridgeclient/` or `external/pluginmcp/` wire formats without updating matching `api/` handlers and tests.
- Never patch Mattermost server config for plugin settings; use `GET`/`PUT /plugins/mattermost-ai/admin/config`.
- Never push to `master`; open a PR.

## Gotchas

- If `make install-go-tools` fails to build `mattermost-govet`, follow the Makefile's fix and bump `MATTERMOST_GOVET_VERSION`.
- `postgres/pgvector_test.go` starts `pgvector/pgvector:pg17` with testcontainers. Set `PGVECTOR_TEST_DSN` to reuse an existing pgvector instance.
- Embedded MCP requires Mattermost `SiteURL`.
- MCP tool-name collision rules differ by layer; read `mcp/AGENTS.md` and `mcpserver/AGENTS.md` before changing registration or proxying.

## Pull requests and commits

- Commit subject: one succinct line. Optional Jira prefix (`MM-12345:`) or short scope (`fix:`, `docs:`, `webapp:`) is fine.
- Do not add `Co-Authored-By` listing the agent.
- Use `.github/PULL_REQUEST_TEMPLATE.md` for the PR body.

## Pointers

Read these when the trigger applies:

- Plugin lifecycle/wiring: `server/AGENTS.md`
- HTTP handlers/auth/admin config: `api/AGENTS.md`
- Mattermost API wrappers and DB helpers: `mmapi/AGENTS.md`
- Config migrations and DB-backed settings: `config/AGENTS.md`
- LLM abstractions/providers/tools: `llm/AGENTS.md`
- Bot/agent runtime management: `bots/AGENTS.md`
- Conversation entities: `conversation/AGENTS.md`
- Runtime conversation flows/tool approval: `conversations/AGENTS.md`
- Channel analysis: `channels/AGENTS.md`
- Thread analysis: `threads/AGENTS.md`
- Meeting transcription/summaries: `meetings/AGENTS.md`
- Semantic search/embeddings/pgvector: `search/AGENTS.md`
- Post streaming: `streaming/AGENTS.md`
- Tool-call loop: `toolrunner/AGENTS.md`
- LLM-facing Mattermost entity formatting: `format/AGENTS.md`
- Tracing helpers and detached contexts: `telemetry/AGENTS.md`
- Built-in Mattermost tools: `mmtools/AGENTS.md`
- Web search providers: `websearch/AGENTS.md`
- MCP client/runtime policy: `mcp/AGENTS.md`
- MCP server/tools: `mcpserver/AGENTS.md`
- Webapp work: `webapp/AGENTS.md`
- Playwright e2e: `e2e/AGENTS.md`
- Prompt evals: `evals/AGENTS.md`
- Evalviewer CLI/TUI: `cmd/evalviewer/AGENTS.md`
- User/admin docs: `docs/AGENTS.md`
- Bridge client library: `public/bridgeclient/AGENTS.md`
- Plugin MCP helper library: `external/pluginmcp/AGENTS.md`
- Load-test controller: `loadtest/controller/AGENTS.md`
- Provider/admin concepts: `docs/admin_guide.md`
