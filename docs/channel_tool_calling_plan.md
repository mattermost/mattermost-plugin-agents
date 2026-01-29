<!--
Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
-->

# Channel Tool Calling Plan (Short-Term, Option A)

## Goal and Problems We are Solving

We want to enable tool calling for channel mentions without leaking sensitive
data. The short-term solution must:

- Use the mentioner's OAuth identity for tool execution.
- Keep tool call approvals visible in the thread so other users can see the
  agent is working.
- Prevent tool arguments and tool results from leaking to the channel.
- Require two approvals:
  - Tool CALL approval (before execution).
  - Tool RESULT approval (before sharing in channel).
- Reuse existing UI components (ToolApprovalSet / ToolCard) as much as possible.

This plan implements a minimal, safe flow using private KV storage for tool
arguments and results, while redacting any channel-visible data.

## Implementation Plan (Detailed)

### 1) Allow tools in channel mentions (but only for @mentions)

**Problem:** `ProcessUserRequestWithContext()` disables tools for non-DM, which
blocks channel tool usage.

**Change:**
- Add a boolean flag or a separate method to allow tools in channel mentions:
  - Example: `ProcessUserRequestWithContext(..., allowToolsInChannel bool)`
- `handleMentions()` passes `allowToolsInChannel=true`.
- `handleDMs()` stays default.

**Guardrails:**
- Only set `DisabledToolsInfo` when tools are actually disabled.
- Do not tell the model "tools are DM-only" when tools are enabled.

### 2) Redact tool calls at the source (streaming layer)

**Problem:** tool arguments are currently stored in `pending_tool_call` post
props and sent over websockets.

**Change (non-DM channels only):**
1. Store full tool calls (with args) in KV (private).
2. Build a redacted tool call list (no args, no results).
3. Write only redacted tool calls to post props and websocket payloads.

**KV key:**
- `tool_call_private:<postID>:<requesterID>`

**Thread UX:**
- Tool call cards appear with tool names and statuses only.
- Arguments are hidden for all channel participants.

**Backend detail:**
- Extend `streaming.Client` to support `KVSet` (and optionally `KVDelete`).
- `mmapi.Client` already has KV operations.

### 3) Tool CALL approval (requester only)

**Problem:** tool execution needs full args, which are now private.

**Change:**
- In `HandleToolCall()`, load full tool calls from KV when in a channel.
- Execute tools using those args.
- Do NOT write results to post props.
- Store results in KV.
- Update the post with redacted tool calls and statuses only.
- Set a lightweight prop such as `pending_tool_result=true`.

**KV key:**
- `tool_result_private:<postID>:<requesterID>`

**Important:**
- Do NOT continue the LLM flow yet.
- Result approval must gate the continuation.

### 4) Tool RESULT approval (requester-only, reuse ToolApproval UI)

**New endpoints:**

1) Fetch private tool calls (requester-only):
```
GET /post/:postid/tool_call_private
```
Returns full tool calls (args included).

2) Fetch private tool results (requester-only):
```
GET /post/:postid/tool_result_private
```
Returns tool calls with results.

3) Submit result approval (requester-only):
```
POST /post/:postid/tool_result
{
  "accepted_tool_ids": ["..."]
}
```
On approval, continue the LLM flow. On rejection, stop and update status.

### 5) LLM continuation after result approval

**Problem:** LLM needs tool results, but results must not leak into channel props.

**Change:**
- Build LLM posts using private tool results in memory, without persisting them.
- Add a helper to inject private tool results for the tool-call post only.

**Flow:**
1. Load thread posts.
2. Replace the tool-call post data with private tool results in memory.
3. Call LLM and stream final answer to the channel.
4. If new tool calls appear, loop back to call approval.

### 6) Frontend changes (reuse existing UI)

#### A) Hide args for non-requesters
- Render tool cards in thread for visibility.
- Show tool name and status only.
- Hide arguments and results for non-requesters.

#### B) Requester sees full call approval
- If `pending_tool_call_redacted` (or equivalent) is set:
  - Requester fetches `GET /tool_call_private`.
  - Render ToolApprovalSet with full args.

#### C) Result approval uses same UI
- If `pending_tool_result` is set:
  - Requester fetches `GET /tool_result_private`.
  - Render ToolApprovalSet in `result` mode:
    - Show results.
    - Hide args.
    - Accept / Reject buttons.

#### Minimal component changes
- Add a prop for approval stage:
  - `approvalStage: "call" | "result"`
- In result stage:
  - Show results (read-only).
  - Hide args.
  - Keep accept / reject buttons.

### 7) Data leak audit checkpoints

| Leak Surface | Mitigation |
| --- | --- |
| Post props `pending_tool_call` | Redact args |
| Websocket tool_call payload | Redact args |
| Post props after execution | Do not store results |
| Tool results storage | KV, requester scoped |
| Result UI | Requester-only fetch |
| LLM trace logs | Consider redaction if enabled |

## Files Likely to Change

### Backend
- `conversations/conversations.go`
- `conversations/tool_handling.go`
- `streaming/streaming.go`
- `api/api_post.go`
- `mmapi/client.go` (streaming interface update)
- Optional: `conversations/private_tool_data.go`

### Frontend
- `webapp/src/components/llmbot_post/llmbot_post.tsx`
- `webapp/src/components/tool_approval_set.tsx`
- `webapp/src/components/tool_card.tsx`
- `webapp/src/client.tsx`
- `webapp/src/i18n/*.json` (if new strings are added)
