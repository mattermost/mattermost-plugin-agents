# loadtest/AGENTS.md

Scoped instructions for load testing and the mock LLM. Root rules in `/AGENTS.md` still apply.

## Layout

- `loadtest/*.go` is in the root module and implements the in-process `loadtest_mock` LLM.
- `loadtest/controller/` is a nested Go module for mattermost-load-test-ng controller registration.
- Operator docs live in `/docs/load-testing.md`.

## Mock LLM

- `loadtest_mock` bypasses Bifrost; it is not an external HTTP mock.
- Profile JSON rejects unknown fields.
- Tool argument generation must stay compatible with LLM tool schemas.
- Look for `Initialized load-test mock LLM` logs when debugging config activation.

## Controller

- The load-test-ng integration blank-imports this controller package.
- Registered action names are stable API; do not rename them without updating docs and downstream config.
- MCP tools used in load tests usually need `auto_run_everywhere` or simulations stall on approval.
- Do not commit temporary `replace` directives used for local load-test-ng development.

## Commands

- Root mock tests: `go test ./loadtest/... -race`
- Nested controller tests: `make loadtest-controller-test`
- Nested controller lint: `make loadtest-controller-lint`
- Nested module drift: `make loadtest-controller-mod-check`
- Nested build smoke: `make loadtest-controller-build`
- Full pre-PR drift check includes: `make check-go-mods`

## Pointers

- Operator setup: `/docs/load-testing.md`.
- LLM provider contracts: `/llm/AGENTS.md`.
