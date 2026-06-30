# conversation/AGENTS.md

Canonical guide for the conversation layer and the analysis features built on it: `conversation/`, `conversations/`, `threads/`, `channels/`, `meetings/`. Root `/AGENTS.md` applies.

## `conversation/` vs `conversations/` (read first — the #1 footgun)

Two distinct packages with near-identical names:

- **`conversation/`** (singular) = entity + persistence + LLM-request assembly. Core type `conversation.Service`. Owns `store.Conversation`/`store.Turn` CRUD, the `ContentBlock` schema, turn→`llm.CompletionRequest` assembly (`BuildCompletionRequest`, `BuildChannelMentionRequest`), approval state (`ComputePostApprovalState`), and MCP reload (`RestoreLoadedMCPToolsFromTurns`). Do **not** add post-hook or HTTP-handler logic here.
- **`conversations/`** (plural) = Mattermost runtime orchestration. Type `conversations.Conversations`. Owns the `MessageHasBeenPosted` hook, DM and channel-mention flows, tool approval/resume (`tool_approval.go`), and regeneration (`regeneration.go`). Always delegates turn/request building to `conversation.Service`; never duplicate the turn JSON schema.

Import paths differ only by the trailing `s`: `…/conversation` vs `…/conversations`.

## Persistence

- Tables `LLM_Conversations` / `LLM_Turns`; soft-delete via `DeleteAt`.
- Threaded lookup is **per-user**: unique key `(RootPostID, BotID, UserID)`. `GetOrCreateConversation` handles the race (`store.ErrConversationConflict`).
- Owner-only analysis conversations (threads/channels) intentionally omit `ChannelID`/`RootPostID`.

## Turn assembly invariants

- Default to **redacting** unshared tool content (`BuildOptions.AllowUnsharedToolContent` false); only opt in for requester-scoped responses (DM follow-up). DM tool results are always shared.
- `ExcludeAfterPostID` truncates back to the originating **user** turn — Bifrost rejects an assistant-ended prefill.
- When replaying turns, merge an assistant turn with its following `tool_result`, and strip stored `Reasoning`/`ReasoningSignature` (not provider-safe to replay).
- Regeneration: build the request **before** `DeleteResponseTurns`.

## Orchestration (`conversations/`)

- Entry `MessageHasBeenPosted` filters (`ErrNoResponse`, `ActivateAIProp`, `FromWebhookProp`, …) then routes to DM or mention handling.
- Auto-execute policy: DM allows `auto_run_in_dm` + `auto_run_everywhere`; channels allow **`auto_run_everywhere` only**. Interaction tools never auto-execute. Automated invokers (webhook/bot/plugin without `activate_ai`) run with tools disabled.
- Follow-up uses `streamToolFollowUp` + `StreamContinuationToPost` (not `StreamToPost`).
- Spans live in `handle_messages.go` (`"message has been posted"`, `"agent run"`), `conversations.go` (`"process dm request"`), `tool_approval.go` (`"handle tool call"`, `"handle tool result"`, `"tool followup completion"`), and `regeneration.go` (`"handle regenerate"`). Use `telemetry.WithTurnID` and span links for resume/regen tracing.

## Analysis features (all use `conversation.Service`)

- **`threads/`**: `Summarize` / `FindActionItems` / `FindOpenQuestions` → `Analyze()` (one-shot, tools disabled). Regen via post props `ThreadIDProp`, `AnalysisTypeProp`.
- **`channels/`**: `AnalyzeChannel` scopes tools with `WithBoundParams({"channel_id": …})` and auto-approves them; `Interval` fetches posts with no tools.
- **`meetings/`**: `SummarizeTranscription` has **no** conversation entity yet — direct `CompletionRequest`; uses `chunking.SplitPlaintextOnSentences` when over the input-token budget; needs `ffmpeg` + `bots.GetTranscribe()` (Whisper via Bifrost); posts tagged `streaming.NoRegen`.
- **`subtitles/`**: parses VTT / Zoom chat → `FormatForLLM()` for meetings; don't format inline.

## Testing

Entity/approval tests in `conversation/`; orchestration (tool approval, DM, channel mention, loaded MCP) in `conversations/`. Analysis packages use in-package JSON fixtures, not e2e. No new mocking libraries.
