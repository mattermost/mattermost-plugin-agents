---
description: Streams LLM output to Mattermost posts over websocket — control events, turn persistence, continuation/regeneration semantics.
tags: [streaming, posts, websocket, turns]
---

# streaming/AGENTS.md

Maps `llm.TextStreamResult` events to Mattermost posts via websocket `postupdate` events, with optional turn persistence. Root `/AGENTS.md` still applies.

## Key files

- `streaming/streaming.go` — `Service`/`MMPostStreamService`, `StreamToPost`, `StreamToNewPost`, `StreamToNewDM`, `StopStreaming`, `GetStreamingContext`, `FinishStreaming`, `turnAccumulator`.
- `streaming/post_modifier.go` — `ModifyPostForBot` and post props (`ConversationIDProp`, `RespondingToProp`, `LLMRequesterUserIDProp`).

## Conventions & gotchas

- **One stream per post:** `GetStreamingContext` returns `ErrAlreadyStreamingToPost` if a post is already streaming.
- **`ReliableClusterSend: true`** on all `postupdate` broadcasts (UDP size limit) — keep it.
- **Tool-call rounds:** resolved vs pre-execution tool calls are distinguished by `isResolvedToolCallsEvent`; a resolved round resets accumulator text.
- **Privacy:** the requester gets full tool-call payloads; the channel gets redacted ones.
- **Turn persistence** is deferred to stream end; `buildContentBlocks` must never marshal `null` (empty → `[]`).
- **Continuation/regeneration** (`StreamContinuationToPost`) demotes the prior anchor turn, sends a `continue` control event, and clears `post.Message`.
- **Background goroutines:** callers commonly `go` the stream + `defer FinishStreaming`, using the request-scoped ctx from `GetStreamingContext`. When the HTTP handler returns first, the upstream caller detaches the span (`telemetry.DetachContext`).

Span `"stream to post"` is created here; see `telemetry/AGENTS.md`. Benchmarks: `go test -bench=. -benchmem ./streaming/...`.
