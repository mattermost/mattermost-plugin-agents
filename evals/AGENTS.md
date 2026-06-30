---
description: Prompt-evaluation harness — GOEVALS gating, LLM-rubric grading, where eval cases live.
tags: [evals, llm, testing, grading]
---

# evals/AGENTS.md

This package is the **harness library**, not where eval cases live. Eval cases are `*_test.go` files (+ JSON fixtures) inside the feature packages: `conversations/`, `threads/`, `channels/`, `react/` (and separately `search/`, `mcpserver/`).

## Running

- `make evals` (TUI), `make evals-ci` (non-interactive, exits 1 on failure), `make evals-comment` (writes `comment.md`, always exits 0). All cover only `./conversations ./threads ./channels ./react`.
- `search/` evals need a local Postgres+pgvector; `mcpserver/` evals run via `make mcp-evals` (Docker). Neither is in `make evals-ci`.
- Activation gate: evals skip unless `GOEVALS=1` (an integer ≥1; `GOEVALS=N` runs each eval N times). The `evalviewer` subcommands set it automatically.
- Results: JSONL at the module root `evals.jsonl` (gitignored); view with `evalviewer view`. See `cmd/evalviewer/`.

## Writing an eval

1. Wrap the test in `evals.Run(t, "name", func(e *evals.EvalT) { … })`. It iterates providers from `LLM_PROVIDER` (default `all` = openai, anthropic, azure, mistral, bedrock, cohere — **not** `openaicompatible`; missing keys skip that provider, they don't fail).
2. Grade output with `evals.LLMRubricT(e, "<natural-language rubric>", output)` (pass threshold is score ≥ 0.6, via a separate grader LLM set by `GRADER_LLM_PROVIDER`, default openai).
3. For thread/channel scenarios, add a JSON fixture beside the test and load it with `evals.LoadThreadFromJSON`.

## Gotchas

- Real LLM + grader calls cost money and are flaky; default models are pinned in `evals.go` (keep in sync with `e2e/helpers/api-config.ts`).
- PR CI uses `make evals-comment` (never fails the job); run `make evals-ci` locally before relying on results.
- `search/search_eval_test.go` uses a manual `skipIfNotEval()` gate, not `evals.Run`.
