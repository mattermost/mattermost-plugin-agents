---
description: Playwright + Testcontainers end-to-end tests and the CI shard registry.
tags: [e2e, playwright, testcontainers, ci, sharding]
---

# e2e/AGENTS.md

Self-contained Playwright suite driven by Testcontainers (Mattermost EE + pgvector, plus a mock or real LLM). Slow and Docker-dependent — defer full runs to CI.

## Commands

- `make e2e` — builds `dist/*.tar.gz` (`make dist`) then runs the full suite across **chromium and firefox** (double the CI runtime; CI is chromium-only).
- Single spec: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list`; `npm run test:headed|test:debug|test:ui` for interactive runs.
- `make check-shards` (= `node scripts/ci-test-groups.mjs validate`) — validates the shard registry; part of `make check`. No Docker needed.

A prebuilt plugin tarball in `dist/` is mandatory for any container run; helpers throw if missing.

## CI shard registry — `scripts/ci-test-groups.mjs` (source of truth)

Two disjoint universes, validated as a bijection over `tests/**/*.spec.ts`:

- **Mock specs** → exactly one `e2e-shard-1..4` group. Balance by expected runtime, not alphabetically.
- **Real-provider specs** → add the path to the `realAPISpecs` Set **and** exactly one `*-real*` group.

When you add a spec that should run in CI, register it here in the same change, then run `make check-shards`. Shard classification follows this registry, not runtime behavior (e.g. `debug-test.spec.ts` is registered as real but uses mock containers).

## Conventions

- **Helpers are the page objects** (`helpers/*`, imported via the `helpers/*` alias); there is no `fixtures/` or `page-objects/` dir and no `e2e/README`.
- Each spec owns container lifecycle in `beforeAll`/`afterAll`. Pick the right entry point: `RunContainer` / `RunSystemConsoleContainer` / `RunAgentContainer` / `RunToolConfig*Container` (mock) or `RunRealAPIContainer` (real).
- Mock LLM is a `thiht/smocker` container at `http://openai:8080`; mock configs set `useResponsesAPI: false` (Smocker only mocks chat completions).
- Plugin config is applied via the plugin admin HTTP API (`helpers/plugin-http.ts`), not mmctl config patch.
- Retries are `0` (flakiness is surfaced, not hidden). CI disables traces/screenshots to avoid leaking API keys.

## Real-provider tests

Need `ANTHROPIC_API_KEY` and/or `OPENAI_API_KEY`; scope with `E2E_PROVIDER`. They call `checkAPIHealth()` first, use extended timeouts, and attach `logs/server-logs.log` on failure. Default models are pinned in `helpers/api-config.ts` and kept in sync with `evals/evals.go` — bump both together. The mock-shard CI job sets `EXCLUDE_REAL_API_TESTS=true` (see the ignore globs in `playwright.config.ts`).
