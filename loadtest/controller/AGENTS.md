# loadtest/controller/AGENTS.md

Scoped instructions for the load-test controller nested Go module. Root rules in `/AGENTS.md` apply.

## Scope

- This directory is a separate Go module so load-test dependencies stay out of the production plugin module.
- Root-module commands such as `go test ./...` do not cover this module unless the Makefile target calls it.

## Commands

- Unit tests: `make loadtest-controller-test`.
- Lint/vet: `make loadtest-controller-lint`.
- Module drift: `make loadtest-controller-mod-check`.
- Compile smoke: `make loadtest-controller-build`.
- Aggregate drift gate: `make check-go-mods`.

## Conventions

- Run commands from repo root unless explicitly working inside this module.
- Commit `go.mod`/`go.sum` drift only when it follows from intentional dependency changes.
