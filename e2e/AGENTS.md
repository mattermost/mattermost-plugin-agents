# e2e/AGENTS.md

Scoped instructions for the Playwright specs in `e2e/`. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

- Tool names render in the activity header as well as the tool card; locate them with `toolCard(scope, label)` from `helpers/llmbot-post`. Lint (`e2e/scoped-tool-label`) treats identifiers ending in `Label` as tool names, so keep that naming for tool-name constants.
- Wait on an observable condition (web-first `expect`, `expect.poll`, mock request history), never `waitForTimeout` or `networkidle`.
- A `beforeAll` shares the 60 s per-test timeout; container `start()` methods call `test.slow()` to triple it, and a new container helper must too.
- Check `exitCode` from `container.exec` (lint-enforced); never log or throw its output when the command or its output can contain credentials.
