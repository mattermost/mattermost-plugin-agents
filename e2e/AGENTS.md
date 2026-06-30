# e2e/AGENTS.md

Playwright + Testcontainers end-to-end tests. Root `/AGENTS.md` still applies. Always run Playwright from `e2e/` (CI sets `working-directory: ./e2e`; tarball paths assume cwd is `e2e/`).

## Commands

- Full suite (slow; chromium + firefox, builds the plugin first): `make e2e`
- Single spec (match CI by pinning chromium): `cd e2e && npx playwright test tests/<path>/spec.ts --project=chromium --reporter=list`
- Skip real-API paths: `EXCLUDE_REAL_API_TESTS=1 npx playwright test --project=chromium`
- Real-API spec locally: `ANTHROPIC_API_KEY=… npx playwright test --project=chromium tests/llmbot-post-component/<spec>.spec.ts`
- Validate shard coverage: `make check-shards` (`node scripts/ci-test-groups.mjs validate`)
- First local run needs browsers: `npx playwright install --with-deps`
- Requires Docker. Failures: `e2e/playwright-report/` (HTML) and `e2e/logs/server-logs.log`.

## Harness

Each spec owns its stack in `beforeAll`/`afterAll`. Reuse a container runner from `helpers/` — do not inline a new Mattermost config unless the scenario is genuinely unique:

- `RunContainer` — default RHS/bot tests (mock bots, mock embeddings)
- `RunSystemConsoleContainer` — System Console UI
- `RunAgentContainer` — Agents UI/API (`tests/agents/`)
- `RunToolConfigContainer[WithPolicies]` — tool-config UI + embedded MCP tools
- `RunRealAPIContainer` / `RunToolConfigRealAPIContainer` — real LLM calls

Plugin config is applied via `PUT /plugins/mattermost-ai/admin/config` (`helpers/plugin-http.ts`), not mmctl config patch.

**Mock LLM** uses Smocker (`helpers/openai-mock.ts`): start `RunOpenAIMocks(mattermost.network)` after Mattermost is up; register specific body matchers before catch-all rules; set `useResponsesAPI: false` on mock services (Smocker only handles `/v1/chat/completions`); `resetMocks()` in `beforeEach` when one container serves many tests.

Reuse UI helpers, don't relocate them: `MattermostPage` (`helpers/mm.ts`), `AIPlugin` (`helpers/ai-plugin.ts`), `AgentPageHelper`/`AgentAPIHelper`, `SystemConsoleHelper`, `LLMBotPostHelper`, `ToolConfigUIHelper`.

## Adding a spec — CI checklist (same PR)

1. Place under `e2e/tests/<feature>/…spec.ts` (snake_case); import helpers as `helpers/foo` (tsconfig paths, not relative `../helpers`).
2. **Mock/non-real** → add to exactly one `e2e-shard-1`…`e2e-shard-4` group in `scripts/ci-test-groups.mjs`.
3. **Real-API** → add the path to the `realAPISpecs` Set **and** the matching `*-real*` group (`llmbot-real-*`, `tool-config-real`, `channel-analysis-real`, `system-console-real`, `tool-calling-real`). A real spec must be in both, never in an `e2e-shard-*` group.
4. Run `make check-shards`. Balance shards by expected runtime, not alphabetically.

## Real-API conventions

- Gate with `getAPIConfig()` + `test.skip(!config.shouldRunTests, …)`; models default via `helpers/api-config.ts` (`ANTHROPIC_MODEL`/`OPENAI_MODEL`, `E2E_PROVIDER`).
- Use long `beforeAll`/test timeouts; `attachAPIErrorContext(testInfo)` in `afterEach`.
- LLM non-determinism: prefer `test.skip(true, 'LLM did not invoke…')` over false-green assertions.

## Never

- Don't enable Playwright traces/screenshots in CI for real-API specs (API-key leakage); don't add `retries` to mask flakes (config sets `retries: 0`).
- `playwright-report/`, `test-results/`, `logs/` are artifacts — never commit.
