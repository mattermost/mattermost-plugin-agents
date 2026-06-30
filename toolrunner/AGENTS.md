# toolrunner/AGENTS.md

Scoped instructions for the LLM tool-call loop. Root rules in `/AGENTS.md` and `/llm/AGENTS.md` apply.

## Architecture

- The loop is call -> execute approved tools -> recall, capped by `MaxToolRounds`.
- The penultimate round can force synthesis by disabling tools.
- `Run(ctx, ...)` should receive the upstream agent-run context so tool spans correlate.
- `shouldExecute == false` leaves unresolved tool calls for approval flows.
- `ToolTurns` is populated as the stream is consumed; inspect it after stream completion.
- Bifrost owns LLM chat spans; toolrunner owns runner-level tool resolution/execution spans.

## Commands

- Unit tests: `go test -v ./toolrunner/...`.
- Related approval behavior: `go test -v ./conversations/... -run Tool`.

## Pointers

- Tool definitions and `LanguageModel`: `/llm/AGENTS.md`.
- Tool approval orchestration: `/conversations/AGENTS.md`.
