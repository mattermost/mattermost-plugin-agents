# cmd/evalviewer/AGENTS.md

Scoped instructions for the evalviewer CLI module. Root rules in `/AGENTS.md` still apply.

## Scope

- This is a nested Go module for running, viewing, checking, and commenting on prompt eval results.
- Full eval harness guidance lives in `/evals/AGENTS.md`.

## Gotchas

- Build through `make evalviewer` from the repo root.
- Keep `EvalLogLine` schema in sync with `/evals/` when result fields change.
- This module is not covered by all root Go module drift checks; run module-local tests after dependency changes.

## Commands

- Build: `make evalviewer`
- Module tests: `cd cmd/evalviewer && go test ./...`
- Install locally: `cd cmd/evalviewer && go install`
