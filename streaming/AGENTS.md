---
description: Streams llm.TextStreamResult into Mattermost posts, websocket events, and persisted turns.
tags: [streaming, posts, websocket, turns]
---

# streaming/AGENTS.md

Consumes an `llm.TextStreamResult` and drives the Mattermost UX (post updates, websocket broadcasts, turn persistence). It does not call providers directly.

- **Two entry points:** `StreamToPost` for first stream/regeneration; `StreamContinuationToPost` for resuming after tool approval (it clears `post.Message` but keeps tool cards).
- **Websocket updates use `ReliableClusterSend: true`** (payloads exceed the UDP limit) and **redact tool-call arguments**.
- **Turn content blocks must marshal as `[]`, never `null`** (`buildContentBlocks` always returns a non-nil slice).
- A stream with no text **and** no tool calls shows an i18n fallback message; a tool-call-only stream (awaiting approval) is valid — don't treat it as "the model returned nothing."
- `GetStreamingContext` registers a per-post cancel func; a duplicate stream to the same post returns `ErrAlreadyStreamingToPost`. Turn persistence requires the `conversation_id` post prop and `SetTurnStore`.
- OTel span `"stream to post"` (PostID/ChannelID/ThreadRootPostID). Benchmarks: `go test -bench=. -benchmem ./streaming/...`.
