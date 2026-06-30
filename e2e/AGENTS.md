# e2e/AGENTS.md

Scoped instructions for Playwright end-to-end tests. Root rules in `/AGENTS.md` still apply.

## Overview

- The suite boots Mattermost, pgvector, and the plugin with Testcontainers.
- Mock-backed tests use Smocker for OpenAI-compatible chat completions.
- Real API tests require provider API keys and run in separate CI jobs.
- `global-setup.ts` creates directories only; specs own container lifecycle.

## Commands

- Validate shard coverage: `make check-shards`
- Full suite: `make e2e`
- Single spec: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list`
- List shard files: `cd e2e && node scripts/ci-test-groups.mjs list e2e-shard-1`
- Mock-only local run: `EXCLUDE_REAL_API_TESTS=1 make e2e`

## Test architecture

- Do not add shared Playwright fixtures unless refactoring the suite intentionally; current specs use per-file `beforeAll` / `afterAll`.
- Choose the right container helper: default plugin, System Console, tool config, agents, or real API.
- Install/configure the plugin through helper APIs that call the plugin admin config endpoint.
- Stop mock containers before Mattermost containers.
- Prefer page objects and helpers in `helpers/` over ad hoc selectors.

## Mock vs real API

- Mock LLM services must set `useResponsesAPI: false`; Smocker handles chat completions, not Responses API.
- Register specific Smocker body matchers before catch-all matchers.
- Real API specs must gate on env config, extend hook/test timeouts, and attach server log context on failures.
- `EXCLUDE_REAL_API_TESTS` is a convenience, not a complete source of truth; prefer explicit spec paths for local runs.

## CI shards

- Every new `tests/**/*.spec.ts` must be assigned in `scripts/ci-test-groups.mjs`.
- Mock/non-real specs go in exactly one `e2e-shard-*` group.
- Real API specs go in `realAPISpecs` and the matching `*-real*` group.
- Balance shards by expected runtime, not alphabetically.

## Never do

- Never assign a spec to multiple shard groups.
- Never add a real-API spec without updating both registries.
- Never commit `playwright-report/`, `test-results/`, or `logs/`.
- Never use `mmctl config patch` for plugin config in new tests.
