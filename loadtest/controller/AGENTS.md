# loadtest/controller/AGENTS.md

Scoped pointer for the nested load-test-ng controller module. Root rules in `/AGENTS.md` still apply.

- Full loadtest guidance lives in `/loadtest/AGENTS.md`.
- Run commands from the repo root Makefile unless iterating locally inside this module.
- Tests: `cd loadtest/controller && go test ./... -race`
- Module drift: `make loadtest-controller-mod-check`
