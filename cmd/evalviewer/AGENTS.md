# cmd/evalviewer/AGENTS.md

Scoped instructions for the evalviewer CLI/TUI. Root rules in `/AGENTS.md` and harness rules in `/evals/AGENTS.md` still apply.

## Commands

- Build: `make evalviewer`.
- Install while iterating: `cd cmd/evalviewer && go install`.
- Interactive run: `make evals` or `./bin/evalviewer run -v ./conversations`.
- CI check: `make evals-ci` or `./bin/evalviewer check -v ./conversations`.
- PR comment artifact: `make evals-comment` or `./bin/evalviewer comment -v ./conversations`.
- View existing results: `./bin/evalviewer view -f evals.jsonl`.
- View failures only: `./bin/evalviewer view -failures-only`.

## Behavior

- `run`, `check`, and `comment` set `GOEVALS=1` and forward package args to `go test`.
- `check` exits non-zero when evals fail.
- `comment` always exits zero and writes `comment.md` for CI aggregation.
- `view` reads an existing JSONL file and does not run tests.

## Pointers

- Eval authoring, providers, and grading rules: `/evals/AGENTS.md`.
- Human CLI overview: `README.md`.
