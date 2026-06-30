# conversations/AGENTS.md

Scoped instructions for Mattermost agent orchestration. Root rules in `/AGENTS.md` apply.

## Scope

- `conversation/` is the singular entity/service layer for persisted conversations, turns, and `CompletionRequest` construction.
- `conversations/` is the plural orchestration layer for Mattermost hooks, DMs, tool approval, streaming, regeneration, and web search decoration.

## Architecture

- Main entry points include `handle_messages.go`, `tool_approval.go`, `regeneration.go`, and `conversations.go`.
- Plugin post hooks intentionally start a root span because Mattermost does not provide a request context there.
- The normal path is build request -> `toolrunner.Run` -> optional web-search annotation -> `streaming.Service`.
- Automated invokers and bot activation paths affect tool approval; preserve policy checker behavior.
- Regeneration and continuation paths have distinct streaming semantics.

## Commands

- Unit tests: `go test -v ./conversations/... ./conversation/...`.
- Prompt evals for this package: `make evals-ci` or `./bin/evalviewer check -v ./conversations`.

## Pointers

- Tool loop: `/toolrunner/AGENTS.md`.
- Streaming: `/streaming/AGENTS.md`.
- Eval authoring: `/evals/AGENTS.md`.
