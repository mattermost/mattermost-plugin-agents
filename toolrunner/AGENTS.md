# toolrunner/AGENTS.md

Scoped instructions for the tool-call loop. Root rules in `/AGENTS.md` still apply.

## Architecture

- `Run(ctx, request, shouldExecute, onRound)` owns the call-execute-recall loop.
- Callers own approval policy; `toolrunner` only asks `shouldExecute` per batch.
- If any tool in a batch needs approval, the whole batch is returned unresolved.
- `ToolRunResult.Stream` is live; `ToolTurns` and final text are reliable only after the stream is fully drained.
- Consecutive tool failures and synthesis behavior are coordinated with `llm/tool_retry.go`.

## Commands

- Toolrunner tests: `go test -v ./toolrunner/...`

## Gotchas

- `toolrunner.MaxToolRounds` re-exports `limits.MaxToolRounds`; default LLM max tool turns may differ.
- Do not add caller-specific authorization or UI decisions here.
- Unknown tools should produce tool results without calling `shouldExecute`.
