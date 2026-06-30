# e2e/AGENTS.md

Scoped instructions for `e2e/`. Root rules in `/AGENTS.md` still apply.

## Prerequisites

- Docker is required; tests use Testcontainers for Mattermost, PostgreSQL/pgvector, and Smocker.
- `make e2e` builds `dist/*.tar.gz` before Playwright runs.
- Node version comes from repo `.nvmrc`.

## Commands

- Validate shard coverage: `make check-shards`.
- Direct shard validation: `cd e2e && node scripts/ci-test-groups.mjs validate`.
- List shard groups: `cd e2e && node scripts/ci-test-groups.mjs groups`.
- Full e2e suite: `make e2e`.
- Single spec: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list --project=chromium`.
- Headed/debug/UI modes: `cd e2e && npm run test:headed`, `npm run test:debug`, or `npm run test:ui`.

## CI shard maintenance

Every `e2e/tests/**/*.spec.ts` file must be represented in `e2e/scripts/ci-test-groups.mjs`.

- Mock or Smocker specs go in the lightest `e2e-shard-*` group by expected runtime.
- Real-provider specs go in `realAPISpecs` and in the matching `*-real*` group.
- `make check-shards` fails on missing files, duplicates, stale paths, or real/mock partition drift.
- `tests/seed.spec.ts` is a generator seed and still belongs to a shard.

## Local environment

- `EXCLUDE_REAL_API_TESTS=true` mirrors mock CI shards.
- `MM_IMAGE` overrides the Mattermost container image.
- `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` are used by real-API suites.
- `E2E_PROVIDER=openai|anthropic` selects provider-specific real-API runs.

## Architecture

- `helpers/plugincontainer.ts` starts the mock LLM plugin environment.
- `helpers/openai-mock.ts` configures Smocker-backed tests.
- `helpers/real-api-container.ts` starts live-provider runs; use its timeout constants for real API `beforeAll`.
- CI uses Chromium with one worker; do not assume parallel isolation.
- CI disables screenshots/traces to avoid leaking provider request data.

## Never do

- Do not add a `.spec.ts` file without updating `ci-test-groups.mjs`.
- Do not classify a real-provider test in only one of `realAPISpecs` or a `*-real*` group.
