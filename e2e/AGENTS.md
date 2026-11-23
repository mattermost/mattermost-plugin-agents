# e2e/AGENTS.md

Scoped instructions for the Playwright specs in `e2e/`. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

- Tool names render in the activity header as well as the tool card; locate them with `toolCard(scope, label)` from `helpers/llmbot-post`. Lint (`e2e/scoped-tool-label`) treats identifiers ending in `Label` as tool names, so keep that naming for tool-name constants.
- Wait on an observable condition (web-first `expect`, `expect.poll`, mock request history), never `waitForTimeout` or `networkidle` (lint-enforced via `e2e/no-arbitrary-wait`).
- Get containers from a `containerTest(async (own) => ...)` factory in `helpers/container-fixture`, never by starting them in `beforeAll` or a test body (lint-enforced for `Run*`/`start*Stack` calls). Its worker fixture runs off the 60 s test clock and gets a fresh worker per call, so call it once per file, or once per describe/test that needs its own containers. Post-start seeding still belongs in `beforeAll(async ({containers}) => ...)`. Bound any new startup step with `withDeadline` from `helpers/deadline` so a hang fails with the step's name.
- Check `exitCode` from `container.exec` (lint-enforced); never log or throw its output when the command or its output can contain credentials.
- CI shard maintenance: assign every new spec under `e2e/tests/` that should run in CI to the lightest `e2e-shard-*` group (balanced by expected runtime, not alphabetically) in `e2e/scripts/ci-test-groups.mjs`. `make check-shards` validates coverage and runs in `make check`.
- Before running a spec directly with `npx playwright test`, rebuild the plugin bundle with `MM_DEBUG= make dist` (or `make dist-ci` on Linux amd64) so you don't test a stale or missing `dist/*.tar.gz`, and pass `--project=chromium` (CI runs Chromium only; Cloud VMs do not install Firefox). On Cursor Cloud VMs, also set `TESTCONTAINERS_HOST_OVERRIDE=localhost` (see root `AGENTS.md`).
- When changing user-facing labels or modal copy in `webapp/`, grep `e2e/` (`tests/` and `helpers/`) for matching text/role locators before pushing.
- RHS view transitions (`rhs_view_transition.tsx`) slide the outgoing view over the incoming view for 350ms; after navigating back/forward in the RHS, wait for the outgoing view (e.g. the thread reply box) to detach before capturing screenshots or asserting visual state.
- Treat strict-mode duplicate-element failures and timeouts after `waitForStreamingComplete` (which swallows the stop-button wait) as potential product races first; check the trace/DOM snapshot before reaching for `.first()`.
- Default Mattermost `channel_user` roles include channel-bookmark permissions; specs testing a non-manager viewer must explicitly strip those permissions from `channel_user`.
