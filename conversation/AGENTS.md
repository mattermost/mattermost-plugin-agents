---
description: Conversation persistence/entity model — turns, content blocks, completion-request assembly. (singular)
tags: [conversation, persistence, turns, blocks]
---

# conversation/AGENTS.md

**Singular `conversation/` = the entity/persistence model.** Not the same package as `conversations/` (plural, the runtime orchestration layer) — see `conversations/AGENTS.md`. Edit here for DB-backed conversations/turns, typed content blocks, and completion-request assembly.

- `Service` (`service.go`) handles conversation/turn CRUD and `BuildCompletionRequest`; set `Operation` to tag the use case (`"conversation"`, `"thread_analysis"`, `"search"`).
- Content blocks (`content_block.go`): `text`, `tool_use`, `tool_result`, … with status constants. `approval_state.go` computes per-post approval state (`call` | `result` | `done`).
- `convert.go` (`BlocksToPost`) renders blocks to a post and **redacts unshared tool results**; format Mattermost entities through `format/`.
- **Rule of thumb:** new DB/turn/block logic → here; new reply paths, hooks, tool UX, streaming → `conversations/`.
