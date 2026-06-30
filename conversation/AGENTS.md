---
description: Conversation ENTITY service (singular) — DB-backed conversations/turns, content-block model, completion-request building. Not orchestration.
tags: [conversation, entity, turns, content-blocks]
---

# conversation/AGENTS.md

The conversation **entity** service (singular). It owns the DB-backed conversation/turn model and building `llm.CompletionRequest`s. Runtime orchestration (hooks, streaming) lives in **`conversations/`** (plural). Root `/AGENTS.md` still applies.

## Key files

- `conversation/service.go` — `Service`, `CreateConversation`, `GetOrCreateConversation`, `BuildCompletionRequest`, `BuildChannelMentionRequest`, turn writes.
- `conversation/content_block.go` — typed blocks (`text`, `tool_use`, `tool_result`, …) and status constants. **Mirrors `webapp/src/types/conversation.ts`** — keep both in sync.
- `conversation/convert.go` — blocks ↔ LLM posts; inline-file limits and redaction.
- `conversation/approval_state.go` — `ComputePostApprovalState` for webapp stages.
- `conversation/derive_loaded_tools.go` — derive loaded MCP tools from turn history.

## Conventions & gotchas

- This package does **not** handle post hooks or streaming — don't add orchestration logic here; put it in `conversations/`.
- Use `CreateTurnAutoSequence` (via `store`) for atomic turn ordering; don't compute sequence numbers manually.
- Persistence goes through the narrow `Store` interface; formatting through `format/`.
- Content-block JSON must round-trip with the webapp and never serialize `null` arrays.
