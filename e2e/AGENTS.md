---
description: Playwright + Testcontainers end-to-end tests — container boot model, LLM mock layers, and the CI shard map.
tags: [e2e, playwright, testcontainers, sharding, mocks]
---

# e2e/AGENTS.md

Playwright E2E that boots Mattermost + pgvector Postgres via Testcontainers, installs the built plugin, and exercises the UI/HTTP APIs against deterministic LLM mocks. Root `/AGENTS.md` still applies.

## Commands

- Full suite (builds `dist` first, chromium + firefox): root `make e2e`.
- From `e2e/` (assumes `dist/*.tar.gz` exists): `npm test`, `--headed`, `--debug`, `--ui`.
- Single spec: `npx playwright test tests/path/spec.ts --reporter=list`.
- Shard validation (part of `make check`): `make check-shards` (= `node scripts/ci-test-groups.mjs validate`).
- Local aimock smoke (not in CI shards): `npx playwright test --config=playwright.aimock-smoke.config.ts`.

Needs Docker and exactly one tarball in repo `dist/`.

## Conventions & gotchas

- **Container model:** specs start their own stack in `beforeAll` (global setup only creates artifact dirs). `mmcontainer.ts` runs `pgvector/pgvector:pg15` + the MM enterprise image (`MM_IMAGE` override); the plugin is installed via `mmctl --local` + the plugin admin HTTP API. Import helpers as `helpers/foo` (tsconfig paths), not relative.
- **Three mock layers — don't conflate:** aimock sidecar (`AIMockContainer`, network alias `openai`; preferred for new LLM tests); Smocker (`OpenAIMockContainer`, used by `tests/tool-config/mock-api/*`); and the default `RunContainer` (embedded MCP + mock embeddings).
- **`real-api/` is not live providers.** Those suites use aimock; no test calls a real OpenAI/Anthropic key.
- **Sharding:** four groups `e2e-shard-1`…`e2e-shard-4` in `scripts/ci-test-groups.mjs`. `validate` requires an exact bijection between `tests/**/*.spec.ts` and the union of shard lists — every new spec must be added to exactly one group, balanced by runtime. `smoke/` is intentionally excluded.
- **CI vs local:** CI runs chromium only, 1 worker, 0 retries, traces/screenshots disabled (secret-leak prevention); the default local config also runs firefox with up to 4 workers.
- `MM_SERVICESETTINGS_ALLOWEDUNTRUSTEDINTERNALCONNECTIONS` must include `openai` (and `websearch` when mocking web search). The aimock keep-alive preload guards against stale-connection flakes — don't remove it casually.
