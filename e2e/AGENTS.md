# e2e/AGENTS.md

Scoped instructions for the Playwright specs in `e2e/`. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

- Tool names render in the activity header as well as the tool card; scope tool-name locators to the tool card or approval element.
- Wait on an observable condition (web-first `expect`, `expect.poll`, mock request history), never `waitForTimeout` or `networkidle`.
- A `beforeAll` that starts containers calls `test.setTimeout(...)` first; otherwise it shares the 60 s per-test timeout.
- Check `exitCode` from `container.exec`; never log or throw its output when the command or its output can contain credentials.
