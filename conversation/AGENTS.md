# conversation/AGENTS.md

Scoped instructions for stored conversation entities. Root rules in `/AGENTS.md` still apply.

## Boundary

- This package owns conversation and turn persistence, completion-request reconstruction, title generation, and tool-turn writes.
- Runtime Mattermost flows, post hooks, streaming, and tool approval live in `conversations/`.

## Architecture

- `ContentBlock` is the persisted JSON schema for text, thinking, tool use, and tool result content.
- `BuildCompletionRequest` reconstructs LLM input from stored turns; changes affect every conversation-backed feature.
- `DeriveLoadedMCPTools` restores dynamically loaded MCP tools from turn history.
- Keep the service store interface narrow; extend `store.Store` first, then widen this package only when needed.
- Empty turn content should marshal as `[]`, not `null`.

## Commands

- Conversation entity tests: `go test -v ./conversation/...`

## Gotchas

- `CreateConversationParams.Operation` feeds telemetry and token usage; keep values aligned with LLM operation constants.
- Tool approval statuses and timestamps are UI/security-visible. Treat schema changes as compatibility changes.
