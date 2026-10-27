# e2e/AGENTS.md

Scoped instructions for the Playwright specs in `e2e/`. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

- Tool names render in the activity header as well as the tool card; locate them with `toolCard(scope, label)` from `helpers/llmbot-post`. Lint (`e2e/scoped-tool-label`) treats identifiers ending in `Label` as tool names, so keep that naming for tool-name constants.
- Wait on an observable condition (web-first `expect`, `expect.poll`, mock request history), never `waitForTimeout` or `networkidle`.
- Get containers from a `containerTest(async (own) => ...)` factory in `helpers/container-fixture`, never by starting them in `beforeAll` or a test body (lint-enforced for `Run*`/`start*Stack` calls). Its worker fixture runs off the 60 s test clock and gets a fresh worker per call, so call it once per file, or once per describe/test that needs its own containers. Post-start seeding still belongs in `beforeAll(async ({containers}) => ...)`. Bound any new startup step with `withDeadline` from `helpers/deadline` so a hang fails with the step's name.
- Check `exitCode` from `container.exec` (lint-enforced); never log or throw its output when the command or its output can contain credentials.
