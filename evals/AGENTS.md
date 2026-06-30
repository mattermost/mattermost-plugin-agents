# evals/AGENTS.md

Scoped instructions for the prompt eval harness. Root rules in `/AGENTS.md` still apply.

## Scope

- `evals/` owns provider wiring, `evals.Run`, LLM rubric grading, fixtures, and JSONL recording.
- Eval test packages outside this directory import this harness; keep feature-specific rubrics with the feature package.

## Commands

- Harness unit tests: `go test -v ./evals/...`.
- Default non-interactive evals: `make evals-ci`.
- Interactive eval TUI: `make evals`.
- PR comment artifact: `make evals-comment`.
- MCP server evals: `make mcp-evals`.
- Run one package through evalviewer: `LLM_PROVIDER=openai ./bin/evalviewer check -v ./conversations`.

## Writing eval tests

- Gate eval execution with `evals.Run` and `evals.NumEvalsOrSkip`; evalviewer sets `GOEVALS=1`.
- Use `evals.LLMRubricT` or `RecordScore` so results land in `evals.jsonl`.
- Use `LoadThreadFromJSON` for exported thread fixtures.
- Keep rubrics behavior-focused and stable; avoid grading incidental wording.
- `search/search_eval_test.go` is special: it needs `GOEVALS=1`, OpenAI credentials, and local pgvector infrastructure.

## Providers

- `LLM_PROVIDER` accepts `openai`, `anthropic`, `azure`, `openaicompatible`, `mistral`, `bedrock`, `cohere`, `all`, or comma-separated values.
- Provider keys and model override names are defined in `createProvider` in `evals/evals.go`; do not duplicate the full matrix elsewhere.
- Grader overrides use `GRADER_LLM_PROVIDER` and `GRADER_LLM_MODEL`.
- Keep examples aligned with default model constants in `evals/evals.go`.

## Output

- `evals.jsonl` is generated output and should not be hand-edited.
- `cmd/evalviewer/AGENTS.md` covers CLI/TUI subcommands and CI behavior.
