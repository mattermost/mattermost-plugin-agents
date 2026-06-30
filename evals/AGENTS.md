# evals/AGENTS.md

Prompt-eval harness library. Root `/AGENTS.md` applies. To run the evalviewer CLI/TUI, see `cmd/evalviewer/README.md`.

## Running

- Interactive TUI: `make evals`
- CI gate (exit 1 on failure): `make evals-ci`
- PR comment (always exit 0, writes `comment.md`): `make evals-comment` — **this is what CI runs**
- MCP/agentic evals (separate, needs Docker): `make mcp-evals` (`GOEVALS=1 go test -v ./mcpserver/ -run Eval -timeout 10m`)
- Ad-hoc single eval: `GOEVALS=1 go test -v ./conversations -run TestName`

Default Make targets cover only `./conversations ./threads ./channels ./react`. `./search` and `./mcpserver` are not in them.

## GOEVALS

Eval bodies skip unless `GOEVALS` is a positive integer (`evals.NumEvalsOrSkip` / `evals.Run`); evalviewer sets it automatically. A value >1 repeats each rubric per provider. Results append to repo-root `evals.jsonl` (gitignored — never commit it). `make check` does **not** run evals.

## Providers & models

- `LLM_PROVIDER`: single name, comma-separated list, or `all` (default). `all` = `openai, anthropic, azure, mistral, bedrock, cohere` — it does **not** include `openaicompatible` (set that one explicitly).
- A missing API key skips that provider (logged), not a hard failure.
- Pinned model defaults live in `evals/evals.go` (`DefaultOpenAIModel`, `DefaultAnthropicModel`, …) — bump them there. Override per provider via `OPENAI_MODEL` / `ANTHROPIC_MODEL` / etc.
- The grader LLM is configured separately: `GRADER_LLM_PROVIDER` (default `openai`), `GRADER_LLM_MODEL`.

## Adding an eval

1. A normal `Test*` (commonly in `*_eval_test.go`) whose body is wrapped in `evals.Run(t, "name", func(e *evals.EvalT) { … })`.
2. Use `e.LLM` / `e.Prompts`; grade with `evals.LLMRubricT(e, rubric, output)` (LLM-as-judge: asserts `pass == true` and `score >= 0.6`).
3. Load thread fixtures with `evals.LoadThreadFromJSON(e, path)`.
4. To include it in CI, add its package to the `evals`/`evals-ci`/`evals-comment` lists in the `Makefile`.
