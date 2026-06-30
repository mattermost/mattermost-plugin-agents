# loadtest/controller/AGENTS.md

Scoped instructions for the mattermost-load-test-ng SimulController module. Root rules in `/AGENTS.md` still apply unless overridden here.

## Boundary

- This directory is a true nested Go module with its own `go.mod`.
- It must stay isolated from production plugin dependencies.
- Plugin-side mock LLM/load-test code lives in root-module `loadtest/`; operator docs live in `docs/load-testing.md`.

## Commands

- Fast tests: `cd loadtest/controller && go test ./... -race`
- CI-equivalent tests: `make loadtest-controller-test`
- Lint/vet/license: `make loadtest-controller-lint`
- Module drift: `make loadtest-controller-mod-check`
- Compile smoke: `make loadtest-controller-build`

## Contract

- Do not import from `github.com/mattermost/mattermost-plugin-agents` root packages.
- The plugin ID is `mattermost-ai`.
- Keep action names aligned with `docs/load-testing.md` and mattermost-load-test-ng expectations.
- Config is loaded from `MM_AGENTS_LOADTEST_CONFIG` or `./config/mattermost-ai-loadtest.json`.
- Do not commit temporary `replace` directives.

## Gotchas

- Root `go test ./...` does not cover this module directly; use the Makefile targets.
- `check-style-fix` does not run the loadtest controller linter.
- Cross-repo behavior may require coordinated changes in mattermost-load-test-ng.
