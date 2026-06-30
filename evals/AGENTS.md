# evals/AGENTS.md

Scoped instructions for prompt evaluations and the evalviewer CLI. Root rules in `/AGENTS.md` still apply.

## When to read this

- Writing or modifying `*_eval_test.go` files.
- Changing graders, provider setup, scoring, recording, or `cmd/evalviewer/`.
- Adding a package to the default eval batch.

## Running

- Interactive TUI: `make evals`
- CI-style check: `make evals-ci`
- PR comment output: `make evals-comment`
- MCP tool evals: `make mcp-evals`
- Ad hoc package: `GOEVALS=1 go test -v ./<pkg> -run Eval`
- Evalviewer module: `make evalviewer`

## Writing evals

1. Use `evals.Run(t, "descriptive name", func(e *evals.EvalT) { ... })`.
2. Assert behavior with rubric helpers such as `LLMRubricT`.
3. Keep rubrics behavioral, not implementation-specific.
4. Load exported thread fixtures with `LoadThreadFromJSON` when needed.
5. Table-drive multiple cases.

## Provider and grader gotchas

- Evals are skipped unless `GOEVALS=1`.
- `LLM_PROVIDER=all` runs the default provider set; `openaicompatible` must be selected explicitly with its API URL and model env vars.
- The grader provider/model are separate from the subject provider/model.
- Missing provider API keys may skip a provider instead of failing.
- Default models live in `evals/evals.go`; update examples when defaults change.

## Artifacts

- `evals.jsonl` is written at the module root and is gitignored.
- `comment.md` is PR comment output and should not be committed.

## cmd/evalviewer

- `cmd/evalviewer/` is a nested Go module and CLI wrapper.
- Keep duplicated log-line schema in sync with `evals/` when fields change.
- Human env examples live in `cmd/evalviewer/README.md`.

## Pointers

- LLM and prompt stack: `/llm/AGENTS.md`.
- MCP evals: `/mcpserver/AGENTS.md`.
