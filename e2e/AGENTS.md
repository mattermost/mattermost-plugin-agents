# e2e/AGENTS.md

Scoped instructions for Playwright + Testcontainers end-to-end tests. Root rules in `/AGENTS.md` still apply.

## Scope

- Specs boot Mattermost Enterprise with pgvector, install the plugin bundle, and drive the UI and plugin APIs.
- Shared helpers live in `helpers/`, not `tests/support/`.
- `tests/seed.spec.ts` is a generator placeholder, not runtime coverage.

## Commands

- Full suite, slow: `make e2e`
- Single spec: `cd e2e && npx playwright test tests/path/spec.ts --reporter=list`
- Chromium only, CI-like: `cd e2e && npx playwright test --project=chromium tests/path/spec.ts --reporter=list`
- Headed/debug/UI modes: `cd e2e && npm run test:headed`, `npm run test:debug`, or `npm run test:ui`
- Validate shard coverage: `make check-shards`
- List a CI group: `cd e2e && node scripts/ci-test-groups.mjs list e2e-shard-1`
- Install browsers if needed: `cd e2e && npx playwright install --with-deps`

## Test architecture

- Use container runners from `helpers/*-container.ts`; do not duplicate Mattermost setup.
- Plugin config changes go through `PUT /plugins/mattermost-ai/admin/config` helpers, not mmctl config patches.
- Mock LLM tests use Smocker via `helpers/openai-mock.ts`; register mocks before each LLM interaction.
- Real API tests use `getAPIConfig()`, provider-specific env vars, and explicit `test.skip` when keys are missing.
- Use page/domain helpers such as `MattermostPage`, `AIPlugin`, `AgentPageHelper`, and `SystemConsoleHelper` instead of raw selectors.

## CI shard rules

- Every new `tests/**/*.spec.ts` must be listed in `scripts/ci-test-groups.mjs`.
- Mock/non-real specs go in exactly one `e2e-shard-*` group.
- Real API specs go in `realAPISpecs` and exactly one matching `*-real*` group.
- Balance by expected runtime, not alphabetically.
- `make check-shards` validates missing, duplicate, and stale assignments.

## Environment

- `EXCLUDE_REAL_API_TESTS=true` skips real API specs for mock shards.
- `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` enable real API specs.
- `ANTHROPIC_MODEL` / `OPENAI_MODEL` override defaults.
- `E2E_PROVIDER` and `E2E_LIVE_PROVIDER` select provider-specific real flows.
- `MM_IMAGE` overrides the Mattermost Enterprise test image.

## Gotchas

- Local `make e2e` may run real API specs if keys are present; CI mock shards set `EXCLUDE_REAL_API_TESTS=true`.
- CI runs Chromium only with one worker; local config may use more projects/workers.
- Retries are zero; fix flaky behavior instead of masking it.
- CI disables Playwright traces/screenshots to avoid leaking API keys.
- Server logs are written to `e2e/logs/server-logs.log`.
- Do not commit `test.only`.
