# conversations/AGENTS.md

Scoped instructions for generation orchestration, conversation entities, and streaming. Root rules in `/AGENTS.md` still apply.

## Package roles

- `conversation/` is the entity service: conversations, turns, content blocks, request building, tool turns, attachments, and redaction.
- `conversations/` is orchestration: post hooks, DMs, channel mentions, tool approval/resume, regeneration, and loop-in flows.
- `streaming/` consumes `llm.TextStreamResult`, updates Mattermost posts, sends websocket events, and persists assistant turns.

## Tool loop

- Generation paths use `toolrunner.New(lm, toolrunner.WithMaxRounds(bot.EffectiveMaxToolTurns()))`.
- `onToolTurns` must persist intermediate assistant/tool rounds through `convService.WriteToolTurns`.
- DM auto-run allows `auto_run` and `auto_run_everywhere`; channels allow only `auto_run_everywhere`.
- MCP meta-tools always auto-run. User-interaction tools never auto-run.

## Privacy and redaction

- `BuildCompletionRequest` defaults to redacting unshared tool content.
- Use `AllowUnsharedToolContent: true` only for requester-scoped output such as DM follow-up.
- Channel and group-DM flows must keep shared-channel redaction defaults.

## Streaming gotchas

- `buildContentBlocks()` must return an empty slice, not `nil`.
- Use continuation streaming only when continuing an existing anchor turn; do not use it for regeneration.
- Websocket broadcasts for streaming updates must be reliable cluster sends.
- DM detection uses `mmapi.IsDMWith`; group DMs follow channel-style sharing.
- The post prop for conversation linkage is `streaming.ConversationIDProp`.

## Telemetry

- Agent runs set turn IDs with `telemetry.WithTurnID`.
- Tool approval resume rehydrates the originating run trace.
- Tool spans live in `tool_approval.go`, `handle_messages.go`, `regeneration.go`, `toolrunner/`, and `llm/tools.go`.

## Commands

- Orchestration tests: `go test -v ./conversations/...`
- Conversation entity tests: `go test -v ./conversation/...`
- Streaming tests: `go test -v ./streaming/...`
- Tool runner tests: `go test -v ./toolrunner/...`
- Streaming benchmarks: `go test -bench=. -benchmem ./streaming/...`
- Evals: `make evals-ci`

## Pointers

- LLM provider and prompt stack: `/llm/AGENTS.md`.
- Format Mattermost entities through `/format/AGENTS.md`.
- MCP client/tool loading: `/mcp/AGENTS.md`.
