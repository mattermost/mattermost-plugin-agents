---
description: Conversation runtime orchestration — hooks, DM/mention flows, tool approval, streaming. (plural)
tags: [conversations, orchestration, hooks, tools, streaming]
---

# conversations/AGENTS.md

**Plural `conversations/` = the runtime behavior/orchestration layer.** Not the same as `conversation/` (singular, the entity/persistence model — see `conversation/AGENTS.md`). Edit here for message handling, DM/channel mention flows, tool approval/regeneration, and streaming integration.

- Depends on `conversation.Service` (injected via `SetConversationService`) and on `meetings` through a `MeetingsService` interface (circular dep broken with a late setter).
- Key files: `handle_messages.go` (`MessageHasBeenPosted` routing), `conversations.go` (DM flows), `tool_approval.go` (`HandleToolCall`/`HandleToolResult` + OTel spans — **this, not the nonexistent `tool_handling.go`**), `regeneration.go`, `store.go` (direct SQL for title save / soft-delete).
- **`ErrNoResponse` is normal silence, not an error.** Automated invokers (webhook/bot/plugin posts) skip channel tool calling unless `activate_ai` is set and a tool-policy checker is present.
- Gotcha: `MessageHasBeenPosted` starts its OTel span from `context.Background()`, not the plugin hook's request ctx.
- `go test ./conversations/...`.
