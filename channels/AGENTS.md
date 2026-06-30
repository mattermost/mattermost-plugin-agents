# channels/AGENTS.md

Scoped instructions for channel and thread analysis. Root rules in `/AGENTS.md` still apply.

## Scope

- Applies to `channels/` and the adjacent `threads/` package.
- Both build Mattermost context, format it, prompt the LLM, and store owner-scoped analysis conversations.

## Shared pattern

- Fetch thread/channel context.
- Format with `format.ThreadData` and related helpers.
- Build prompts through the LLM prompt stack.
- Persist through `conversation.Service`.

## Channels

- Channel analysis binds MCP tools with `channel_id`; the LLM must not override it.
- Missing embedded MCP tools are a hard error for tool-backed analysis.
- Interval filtering removes deleted and system posts before formatting.
- Tool turns must be persisted through the standard conversation flow.

## Threads

- Thread analysis uses `mmapi.GetThreadData`.
- Tools are disabled for thread analysis.
- Prompt names for action items/open questions are resolved explicitly.

## Commands

- Analysis tests: `go test -v ./channels/... ./threads/...`
- Channel focus: `go test -v ./channels/... -run Test`
- Thread focus: `go test -v ./threads/... -run Test`

## Pointers

- Conversation entity/orchestration split: `/conversations/AGENTS.md`.
- Formatting: `/format/AGENTS.md`.
- MCP client/server behavior: `/mcp/AGENTS.md` and `/mcpserver/AGENTS.md`.
