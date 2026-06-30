---
description: Prompt-eval harness — GOEVALS semantics, provider matrix, LLM-as-judge graders, and where eval tests live.
tags: [evals, llm, graders, providers]
---

# evals/AGENTS.md

Go library wrapping `testing.T` with real LLM providers (via Bifrost), LLM-as-judge rubrics, and JSONL recording. The eval **tests** live in feature packages (`conversations/`, `threads/`, `channels/`, `react/`, …), not under `evals/`. Root `/AGENTS.md` still applies.

## Commands

- Non-interactive (CI gate): `make evals-ci`. Interactive TUI: `make evals`. PR comment: `make evals-comment` (always exit 0).
- Single package/test: `GOEVALS=1 go test -v ./conversations -run TestName`.
- MCP server evals: `make mcp-evals`.
- Provider/model selection: `LLM_PROVIDER=openai make evals-ci`, `ANTHROPIC_MODEL=claude-sonnet-4-6 make evals-ci`.

## Key files

- `evals/evals.go` — `Eval`/`EvalT`, `Run`, provider selection, `NumEvalsOrSkip`.
- `evals/graders.go` — `LLMRubric`/`LLMRubricT` (pass threshold score ≥ 0.6).
- `evals/record.go` — appends to repo-root `evals.jsonl`. `cmd/evalviewer/` consumes it (see its README).

## Conventions & gotchas

- **`GOEVALS` is a repeat count, not a boolean:** evals skip entirely unless it parses to an integer ≥ 1; `GOEVALS=3` repeats each eval 3 times per provider.
- **`LLM_PROVIDER=all` = `openai, anthropic, azure, mistral, bedrock, cohere`** — it does **not** include `openaicompatible` (that one only works when named explicitly). Default is `all`.
- **Missing API key = soft skip** (logged), not a failure.
- **Grader is a separate LLM:** `GRADER_LLM_PROVIDER` (default `openai`), `GRADER_LLM_MODEL`.
- Default models live in code (`DefaultOpenAIModel`, `DefaultAnthropicModel`, …), overridable via `OPENAI_MODEL`, `ANTHROPIC_MODEL`, etc.
- `make evals-ci` only runs `./conversations ./threads ./channels ./react`; `search/*_eval_test.go` and `mcpserver/tools_eval_test.go` run manually or via `make mcp-evals`.
