# conversations/AGENTS.md

Scoped instructions for runtime conversation orchestration. Root rules in `/AGENTS.md` still apply.

## Boundary

- This package handles Mattermost DM/channel flows, post hooks, tool approval/resume, regeneration, web-search context, and streaming handoff.
- Stored conversation/turn entities live in `conversation/`.

## Architecture

- Main flows build context, run `toolrunner.Run`, then pass streams to `streaming.Service`.
- Channel tool execution is stricter than DM execution. Automated invokers disable tools unless explicitly allowed by policy.
- `shouldAutoExecuteTool` applies DM/channel policy differences; do not collapse them without checking multiplayer tool-calling docs and tests.
- Approval resume must rehydrate trace context with `telemetry.WithTurnID` and preserve loaded MCP tools from stored turns.
- Use `StreamContinuationToPost` for approval resumes, not normal regeneration streaming.
- Import `"context"` as `stdcontext` in files that also use `*llm.Context`.

## Commands

- Runtime conversation tests: `go test -v ./conversations/...`
- Conversation evals: `make evals-ci` or targeted `GOEVALS=1 go test -v ./conversations -run Eval`

## Gotchas

- Preserve typed approval errors such as stale click, missing conversation ID, wrong requester, and invalid answer.
- Do not add spans in old paths; tool approval spans live in `tool_approval.go`.
- Web-search tool wiring is split across `mmtools/`, `websearch/`, and this package's context extraction.
