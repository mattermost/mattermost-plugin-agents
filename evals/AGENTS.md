# evals/AGENTS.md

Scoped instructions for prompt evals, the eval harness, and `cmd/evalviewer`. Root rules in `/AGENTS.md` still apply.

## Commands

- Build CLI: `make evalviewer`
- Interactive run with TUI: `make evals`
- Non-interactive local gate: `make evals-ci`
- PR comment generation: `make evals-comment`
- MCP tool evals: `make mcp-evals`
- Single eval package: `GOEVALS=1 go test -v ./conversations -run Eval`
- Inspect existing results: `./bin/evalviewer view`

## Gating and providers

- Eval tests skip unless `GOEVALS` is set to an integer >= 1.
- `evalviewer` injects `GOEVALS=1`.
- `LLM_PROVIDER` supports `openai`, `anthropic`, `azure`, `mistral`, `bedrock`, `cohere`, `openaicompatible`, comma-separated lists, or `all`.
- Missing API keys skip that provider with test logs; they are not always hard failures.
- Grader model env vars are separate: `GRADER_LLM_PROVIDER` and `GRADER_LLM_MODEL`.

## Writing evals

- Wrap eval bodies in `evals.Run(t, "name", func(t *evals.EvalT) { ... })`.
- Use `t.LLM` and `t.Prompts` for production-like behavior.
- Assert quality with `evals.LLMRubricT`; pass means grader pass plus score threshold.
- Use `evals.LoadThreadFromJSON` for exported thread/channel fixtures.
- If a package should run in `make evals*`, add it to `evals`, `evals-ci`, and `evals-comment` Makefile targets.
- MCP eval tests should include `Eval` in the test name so `make mcp-evals` picks them up.

## Outputs and CI

- `evals.jsonl` is generated at the module root and gitignored.
- `comment.md` is generated for PR comment jobs; do not commit it.
- PR CI uses `make evals-comment || true`; failures are summarized in comments rather than failing the workflow.

## Gotchas

- Prompt template changes can affect evals even when Go tests compile.
- `search/` and `mcpserver/` have eval tests outside the default `make evals*` package list.
- `cmd/evalviewer/README.md` is human-facing and can lag code; prefer this file plus source for agent work.
