---
description: Conversation runtime ORCHESTRATION (plural) — message hooks, DM/mention routing, tool approval flows, streaming, regeneration.
tags: [conversations, orchestration, tool-approval, dm, mentions]
---

# conversations/AGENTS.md

Runtime **orchestration** (plural). Hook-driven AI behavior that delegates entity work to `conversation.Service` (singular — see `conversation/AGENTS.md`). Root `/AGENTS.md` still applies.

## Key files

- `conversations/handle_messages.go` — `MessageHasBeenPosted`, mention/DM routing, automated-invoker rules.
- `conversations/conversations.go` — `ProcessDMRequest`, `CreateOrGetDMConversation`.
- `conversations/tool_approval.go` — `HandleToolCall`, `HandleToolResult`, tool follow-up streaming.
- `conversations/regeneration.go`, `loop_in_agent.go` — user-initiated flows.
- `conversations/bot_channel_tool_filter.go` — channel follow-up MCP tool constraints.
- `conversations/store.go` — direct `LLM_Conversations` title/delete SQL via `mmapi.DBClient` (parallel to the `store` package).

## Conventions & gotchas

- **`convService` (the `conversation.Service`) must be set** before DM/mention paths work — it's injected via a setter during wiring in `server/main.go`.
- **Automated invokers** (bot/webhook/plugin/oauth posts) disable channel tool calling unless `activate_ai` is set and a tool-policy checker is present.
- **`MessageHasBeenPosted` is a hook entrypoint and uses `context.Background()`** — the documented exception to the root "no `context.Background()`" rule. Thread the resulting ctx onward.
- New conversational behavior usually belongs **here**; new entity/turn/content-block logic belongs in `conversation/`.
- Tool approval maps stale clicks to `ErrStaleToolClick`; surface that to the API, don't swallow it.
