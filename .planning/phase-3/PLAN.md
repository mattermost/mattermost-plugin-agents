# Phase 3: Core Auto-Approval Logic - Implementation Plan

## Overview

Phase 3 implements the core auto-approval mechanism for READ-only tools from Mattermost-approved MCP servers. When ALL tool calls in a batch come from approved servers and are classified as READ-only, the system skips the "approve execution" step (stage 1) and immediately executes them, then presents the user with the "approve results" step (stage 2) before sharing results in the channel.

**Key design constraints:**
- **All-or-nothing**: If ANY tool in a batch is NOT auto-approvable, fall back to the full two-step approval flow for the ENTIRE batch.
- **Result approval preserved**: Even auto-approved tools still require result approval in channels (data leakage prevention).
- **DM flow unchanged**: DMs already auto-execute all tools. No changes needed for DMs.
- **Existing patterns**: Follow the existing `AutoRunToolsWrapper` and `ShouldAutoRunTools` patterns already in the codebase.

## Data Flow Summary

```
LLM returns EventTypeToolCalls (with ServerOrigin populated by Phase 2)
    |
    v
StreamToPost receives tool calls in EventTypeToolCalls handler
    |
    v
Check: is this a DM? --YES--> Existing DM flow (no change)
    |
    NO
    v
Check: are ALL tool calls auto-approvable? (IsToolAutoApproved for each)
    |
    |--NO--> Existing channel flow: store in KV, redact, set pending_tool_call prop, wait for user
    |
    YES
    v
Store full tool calls in KV (private, same as existing)
Set post prop: "auto_approved_tool_call" = "true"
Set redacted tool calls on post
Update post
Invoke autoExecuteCallback(postID, requesterID) in a goroutine
    |
    v
autoExecuteCallback calls HandleToolCall with ALL tool IDs pre-accepted
    |
    v
HandleToolCall executes tools, stores results in KV
Sets pending_tool_result prop (triggers result approval UI)
    |
    v
User sees result approval UI (stage 2) -- existing flow, no change
```

---

## Step 3.1: Add Auto-Approval Check Infrastructure to Streaming

### File: `streaming/streaming.go`

This is the primary hook point. The `StreamToPost` method handles the `EventTypeToolCalls` case (lines 351-403). Currently, for non-DM channels, it stores tool calls in KV and sets the `pending_tool_call` post prop, which triggers the approval UI on the frontend.

We need to add:
1. A mechanism to check if tool calls are auto-approvable
2. A callback to trigger auto-execution
3. A new post property to signal auto-approved status

#### New Constant

Add to the constants section (around line 37):

```go
const AutoApprovedToolCallProp = "auto_approved_tool_call"
```

#### New Types for Auto-Approval

The `MMPostStreamService` needs access to:
1. An auto-approval checker (to determine if tool calls should be auto-approved)
2. An auto-execute callback (to trigger tool execution after auto-approval)

Add a new interface and callback type:

```go
// ToolAutoApprovalChecker checks whether a tool call should be auto-approved.
type ToolAutoApprovalChecker interface {
    IsToolAutoApproved(serverBaseURL string, toolName string) bool
}

// AutoExecuteCallback is called when all tool calls in a batch are auto-approvable.
// It triggers tool execution without user approval.
// Parameters: postID, requesterID
type AutoExecuteCallback func(postID string, requesterID string)
```

#### Modify MMPostStreamService

Add fields to the `MMPostStreamService` struct:

```go
type MMPostStreamService struct {
    contexts              map[string]postStreamContext
    contextsMutex         sync.Mutex
    mmClient              Client
    i18n                  *i18n.Bundle
    toolAutoApprover      ToolAutoApprovalChecker  // NEW
    autoExecuteCallback   AutoExecuteCallback      // NEW
}
```

#### Modify Constructor

Update `NewMMPostStreamService`:

```go
func NewMMPostStreamService(mmClient Client, i18n *i18n.Bundle) *MMPostStreamService {
    return &MMPostStreamService{
        contexts: make(map[string]postStreamContext),
        mmClient: mmClient,
        i18n:     i18n,
    }
}
```

Add setter methods (to avoid breaking existing constructor callers and to support late initialization, which is common in plugin startup where dependencies are wired incrementally):

```go
// SetToolAutoApprover sets the tool auto-approval checker for the streaming service.
func (p *MMPostStreamService) SetToolAutoApprover(checker ToolAutoApprovalChecker) {
    p.toolAutoApprover = checker
}

// SetAutoExecuteCallback sets the callback that will be invoked when all tool calls
// in a batch are auto-approvable.
func (p *MMPostStreamService) SetAutoExecuteCallback(callback AutoExecuteCallback) {
    p.autoExecuteCallback = callback
}
```

#### Modify StreamToPost EventTypeToolCalls Handler

Current code at lines 351-403:

```go
case llm.EventTypeToolCalls:
    if toolCalls, ok := event.Value.([]llm.ToolCall); ok {
        for i := range toolCalls {
            toolCalls[i].Status = llm.ToolCallStatusPending
            toolCalls[i].SanitizeArguments()
        }

        channel, err := p.mmClient.GetChannel(post.ChannelId)
        if err != nil {
            p.mmClient.LogError(...)
            return
        }
        isDMWithBot = mmapi.IsDMWith(post.UserId, channel)

        toolCallsForPost := toolCalls
        if !isDMWithBot {
            requesterID, ok := post.GetProp(LLMRequesterUserID).(string)
            if !ok || requesterID == "" {
                p.mmClient.LogError(...)
                return
            }
            kvKey := ToolCallPrivateKVKey(post.Id, requesterID)
            if kvErr := p.mmClient.KVSet(kvKey, toolCalls); kvErr != nil {
                p.mmClient.LogError(...)
                return
            }
            toolCallsForPost = RedactToolCalls(toolCalls)
            post.AddProp(ToolCallRedactedProp, "true")
        }

        toolCallJSON, err := json.Marshal(toolCallsForPost)
        if err != nil {
            p.mmClient.LogError(...)
        } else {
            post.AddProp(ToolCallProp, string(toolCallJSON))
        }

        if err := p.mmClient.UpdatePost(post); err != nil {
            p.mmClient.LogError(...)
        }

        p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{...}, broadcast)
    }
    return
```

**New code** - replace the `if !isDMWithBot {` block (the part between `isDMWithBot = mmapi.IsDMWith(...)` and the tool call JSON marshaling). The key change is inside the `!isDMWithBot` branch:

```go
if !isDMWithBot {
    requesterID, ok := post.GetProp(LLMRequesterUserID).(string)
    if !ok || requesterID == "" {
        p.mmClient.LogError("Missing requester ID for tool call, cannot persist private data", "post_id", post.Id)
        return
    }
    kvKey := ToolCallPrivateKVKey(post.Id, requesterID)
    if kvErr := p.mmClient.KVSet(kvKey, toolCalls); kvErr != nil {
        p.mmClient.LogError("Failed to store tool calls in KV store, cannot continue", "error", kvErr, "post_id", post.Id, "kv_key", kvKey)
        return
    }

    // NEW: Check if all tool calls are auto-approvable
    allAutoApprovable := p.areAllToolCallsAutoApprovable(toolCalls)

    toolCallsForPost = RedactToolCalls(toolCalls)
    post.AddProp(ToolCallRedactedProp, "true")

    if allAutoApprovable {
        post.AddProp(AutoApprovedToolCallProp, "true")
    }
}
```

After the post update and websocket event, add auto-execution trigger:

```go
// After the existing UpdatePost and PublishWebSocketEvent calls...

// NEW: If tools were auto-approved, trigger auto-execution
if !isDMWithBot {
    if autoApproved, _ := post.GetProp(AutoApprovedToolCallProp).(string); autoApproved == "true" {
        if p.autoExecuteCallback != nil {
            requesterID, _ := post.GetProp(LLMRequesterUserID).(string)
            go p.autoExecuteCallback(post.Id, requesterID)
        }
    }
}
```

#### New Helper Method on MMPostStreamService

```go
// areAllToolCallsAutoApprovable checks if all tool calls in the batch
// can be auto-approved. Returns false if any tool is not auto-approvable,
// or if the auto-approval checker is not configured.
func (p *MMPostStreamService) areAllToolCallsAutoApprovable(toolCalls []llm.ToolCall) bool {
    if p.toolAutoApprover == nil {
        return false
    }
    if len(toolCalls) == 0 {
        return false
    }
    for _, tc := range toolCalls {
        if !p.toolAutoApprover.IsToolAutoApproved(tc.ServerOrigin, tc.Name) {
            return false
        }
    }
    return true
}
```

**Complete replacement of the EventTypeToolCalls handler:**

Here is exactly what the full `case llm.EventTypeToolCalls:` block should look like after modification:

```go
case llm.EventTypeToolCalls:
    // Handle tool call event
    if toolCalls, ok := event.Value.([]llm.ToolCall); ok {
        // Ensure all tool calls have Pending status and sanitize arguments
        for i := range toolCalls {
            toolCalls[i].Status = llm.ToolCallStatusPending
            toolCalls[i].SanitizeArguments()
        }

        channel, err := p.mmClient.GetChannel(post.ChannelId)
        if err != nil {
            p.mmClient.LogError("Failed to get channel for tool call redaction", "error", err, "post_id", post.Id, "channel_id", post.ChannelId)
            return
        }
        isDMWithBot = mmapi.IsDMWith(post.UserId, channel)

        toolCallsForPost := toolCalls
        var autoApproved bool
        if !isDMWithBot {
            requesterID, ok := post.GetProp(LLMRequesterUserID).(string)
            if !ok || requesterID == "" {
                p.mmClient.LogError("Missing requester ID for tool call, cannot persist private data", "post_id", post.Id)
                return
            }
            kvKey := ToolCallPrivateKVKey(post.Id, requesterID)
            if kvErr := p.mmClient.KVSet(kvKey, toolCalls); kvErr != nil {
                p.mmClient.LogError("Failed to store tool calls in KV store, cannot continue", "error", kvErr, "post_id", post.Id, "kv_key", kvKey)
                return
            }

            // Check if all tool calls are auto-approvable
            autoApproved = p.areAllToolCallsAutoApprovable(toolCalls)

            toolCallsForPost = RedactToolCalls(toolCalls)
            post.AddProp(ToolCallRedactedProp, "true")

            if autoApproved {
                post.AddProp(AutoApprovedToolCallProp, "true")
            }
        }

        // Add the tool call as a prop to the post
        toolCallJSON, err := json.Marshal(toolCallsForPost)
        if err != nil {
            p.mmClient.LogError("Failed to marshal tool call", "error", err)
        } else {
            post.AddProp(ToolCallProp, string(toolCallJSON))
        }

        // Update the post with the tool call and any reasoning that was previously added
        if err := p.mmClient.UpdatePost(post); err != nil {
            p.mmClient.LogError("Failed to update post with tool call", "error", err)
        }

        // Send websocket event with tool call data
        p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
            "post_id":   post.Id,
            "control":   "tool_call",
            "tool_call": string(toolCallJSON),
        }, broadcast)

        // If tools were auto-approved in a channel, trigger auto-execution
        if autoApproved && p.autoExecuteCallback != nil {
            requesterID, _ := post.GetProp(LLMRequesterUserID).(string)
            go p.autoExecuteCallback(post.Id, requesterID)
        }
    }
    return
```

---

## Step 3.2: Implement Auto-Execute Callback in Conversations

### File: `conversations/tool_handling.go`

Add a new public method that serves as the auto-execute callback. This method retrieves the tool calls from KV, calls `HandleToolCall` with all tool IDs pre-accepted.

#### New Method: `AutoExecuteApprovedToolCalls`

```go
// AutoExecuteApprovedToolCalls is the callback invoked by the streaming layer
// when all tool calls in a batch have been auto-approved. It retrieves the
// tool calls from KV store and calls HandleToolCall with all tool IDs pre-accepted.
func (c *Conversations) AutoExecuteApprovedToolCalls(postID string, requesterID string) {
    post, err := c.mmClient.GetPost(postID)
    if err != nil {
        c.mmClient.LogError("Auto-execute: failed to get post", "error", err, "post_id", postID)
        return
    }

    channel, err := c.mmClient.GetChannel(post.ChannelId)
    if err != nil {
        c.mmClient.LogError("Auto-execute: failed to get channel", "error", err, "post_id", postID)
        return
    }

    // Read tool calls from KV store to get the full (unredacted) tool call data
    toolCallKVKey := streaming.ToolCallPrivateKVKey(postID, requesterID)
    var toolCalls []llm.ToolCall
    if kvErr := c.mmClient.KVGet(toolCallKVKey, &toolCalls); kvErr != nil {
        c.mmClient.LogError("Auto-execute: failed to load tool calls from KV store", "error", kvErr, "post_id", postID)
        return
    }

    // Collect all tool IDs for pre-acceptance
    allToolIDs := make([]string, 0, len(toolCalls))
    for _, tc := range toolCalls {
        allToolIDs = append(allToolIDs, tc.ID)
    }

    // Call HandleToolCall with all tools pre-accepted
    if err := c.HandleToolCall(requesterID, post, channel, allToolIDs); err != nil {
        c.mmClient.LogError("Auto-execute: HandleToolCall failed", "error", err, "post_id", postID)
    }
}
```

**Important notes:**
- The method signature is `func(postID string, requesterID string)` which matches the `AutoExecuteCallback` type defined in Step 3.1.
- `HandleToolCall` already handles: tool execution, KV storage of results, setting `pending_tool_result` prop, and post update. It will work correctly because it reads the full tool calls from KV, not from the post props.
- The result approval flow (stage 2) is triggered automatically because `HandleToolCall` sets the `pending_tool_result` prop on successful execution.

### Wiring the Callback

The callback must be wired during plugin initialization. The `MMPostStreamService` and `Conversations` are both created during plugin startup.

#### File: Look for plugin initialization

Search for where `MMPostStreamService` and `Conversations` are created. The callback wiring should be:

```go
// After both streamingService and conversationsService are created:
streamingService.SetAutoExecuteCallback(conversationsService.AutoExecuteApprovedToolCalls)
```

Search pattern to find the right file: `NewMMPostStreamService` usage.

The auto-approval checker also needs wiring:

```go
// configContainer is the *config.Container
streamingService.SetToolAutoApprover(configContainer.ApprovedMCPServers())
```

**Wait - there's a subtlety here.** `configContainer.ApprovedMCPServers()` returns a `*mcp.ApprovedMCPServersConfig` which already has the `IsToolAutoApproved` method. But the config can change dynamically. We need the streaming service to always use the current config, not a snapshot taken at startup.

The solution: instead of passing a static `ApprovedMCPServersConfig`, pass an adapter that calls `configContainer.ApprovedMCPServers()` on each invocation.

#### New Adapter Type

**File: `streaming/streaming.go`** (or a new small file, but keeping it in streaming.go is fine)

```go
// ToolAutoApprovalFunc is a function adapter that implements ToolAutoApprovalChecker.
type ToolAutoApprovalFunc func(serverBaseURL string, toolName string) bool

func (f ToolAutoApprovalFunc) IsToolAutoApproved(serverBaseURL string, toolName string) bool {
    return f(serverBaseURL, toolName)
}
```

#### Wiring in Plugin Initialization

```go
// Create the adapter that always reads the latest config
autoApprovalChecker := streaming.ToolAutoApprovalFunc(func(serverBaseURL string, toolName string) bool {
    return configContainer.ApprovedMCPServers().IsToolAutoApproved(serverBaseURL, toolName)
})
streamingService.SetToolAutoApprover(autoApprovalChecker)
streamingService.SetAutoExecuteCallback(conversationsService.AutoExecuteApprovedToolCalls)
```

---

## Step 3.3: Modify Plugin Initialization to Wire Auto-Approval

### File: `server/main.go`

The `OnActivate()` method creates both `streamingService` (line 157) and `conversationsService` (lines 232-243). The wiring must be added after both are created.

**Exact location**: After line 259 (where `conversationsService.SetMeetingsService(meetingsService)` is called), add:

```go
// Wire auto-approval for approved MCP server tools in channels
streamingService.SetToolAutoApprover(streaming.ToolAutoApprovalFunc(func(serverBaseURL string, toolName string) bool {
    return p.configuration.ApprovedMCPServers().IsToolAutoApproved(serverBaseURL, toolName)
}))
streamingService.SetAutoExecuteCallback(conversationsService.AutoExecuteApprovedToolCalls)
```

This uses `p.configuration` (the `config.Container`) which is the same pattern used throughout the file (see lines 116, 180, 201, 212, etc.).

**Why here and not earlier?**
- `streamingService` is created at line 157
- `conversationsService` is created at lines 232-243
- The wiring needs both to exist
- The location after `SetMeetingsService` (line 259) is a natural place for late-binding wiring, following the existing pattern

---

## Step 3.4: HandleToolCall Compatibility Check

`HandleToolCall` in `conversations/tool_handling.go` already handles the case where all tools are accepted. Let's verify it works correctly for auto-approval:

1. **Input**: `acceptedToolIDs` contains ALL tool IDs (from `AutoExecuteApprovedToolCalls`)
2. **KV store read**: On line 168-174, it reads full tool calls from KV (NOT from post props). This works because `AutoExecuteApprovedToolCalls` passes the `post` object that has the tool call prop set.
3. **Tool execution loop**: Lines 195-212, all tools are in `acceptedToolIDs`, so all get executed.
4. **Channel result handling**: Lines 214-268, it stores results in KV with `ToolResultPrivateKVKey`, sets `pending_tool_result` on the post, redacts tool calls. This is exactly the flow we want for auto-approved tools.

**No modifications needed to `HandleToolCall` itself.** The existing logic handles the auto-approved case correctly when called with all tool IDs pre-accepted.

**One potential issue**: The `HandleToolCall` method checks `requesterID != userID` on line 165. In `AutoExecuteApprovedToolCalls`, we pass `requesterID` as the `userID` parameter, so this check passes.

---

## Thread Safety and Concurrency Considerations

1. **Goroutine for auto-execution**: The `go p.autoExecuteCallback(post.Id, requesterID)` call is safe because:
   - The post has already been updated with all necessary props
   - The KV store has the tool calls stored
   - `HandleToolCall` reads fresh data from KV and post

2. **Race between auto-execution and manual approval**: If the frontend were to somehow show an approval UI before the auto-execution goroutine runs, the user could manually approve. This is not a problem because:
   - The frontend will see `auto_approved_tool_call = "true"` and should NOT show the approval UI (Phase 4 will handle this)
   - Even if there's a race, `HandleToolCall` is idempotent with respect to reading from KV - the first call to execute will succeed, subsequent calls would fail gracefully

3. **Config changes during streaming**: The `ToolAutoApprovalFunc` adapter always reads the latest config, so mid-stream config changes are handled correctly.

4. **KV store atomicity**: The existing KV store operations are not transactional, which is consistent with the current codebase. The auto-approval flow uses the same KV patterns as the manual approval flow.

---

## Post Properties Summary

After Phase 3, a channel post with auto-approved tool calls will have these props:

```json
{
    "pending_tool_call": "[{redacted tool calls JSON}]",
    "pending_tool_call_redacted": "true",
    "auto_approved_tool_call": "true",
    "llm_requester_user_id": "user-id",
    "allow_tools_in_channel": "true"
}
```

After auto-execution completes (via `HandleToolCall`), the post will transition to:

```json
{
    "pending_tool_call": "[{redacted tool calls with results}]",
    "pending_tool_call_redacted": "true",
    "auto_approved_tool_call": "true",
    "pending_tool_result": "true",
    "llm_requester_user_id": "user-id",
    "allow_tools_in_channel": "true"
}
```

The frontend (Phase 4) will detect `auto_approved_tool_call = "true"` and skip the call-stage UI, showing only the result-stage UI.

---

## Unit Tests

### File: `streaming/streaming_test.go` (new or extend existing)

Note: `streaming/tool_call_redaction_test.go` already exists for redaction tests. The streaming tests should go in a new file or the existing test file if appropriate.

#### Test: `TestAreAllToolCallsAutoApprovable`

Table-driven test for the `areAllToolCallsAutoApprovable` helper.

```go
func TestAreAllToolCallsAutoApprovable(t *testing.T) {
    tests := []struct {
        name           string
        toolCalls      []llm.ToolCall
        checker        ToolAutoApprovalChecker
        expected       bool
    }{
        {
            name:      "nil checker returns false",
            toolCalls: []llm.ToolCall{{Name: "test", ServerOrigin: "https://example.com"}},
            checker:   nil,
            expected:  false,
        },
        {
            name:      "empty tool calls returns false",
            toolCalls: []llm.ToolCall{},
            checker:   &mockAutoApprover{approveAll: true},
            expected:  false,
        },
        {
            name: "all tools auto-approvable returns true",
            toolCalls: []llm.ToolCall{
                {Name: "get_issue", ServerOrigin: "https://api.github.com"},
                {Name: "list_repos", ServerOrigin: "https://api.github.com"},
            },
            checker:  &mockAutoApprover{approveAll: true},
            expected: true,
        },
        {
            name: "one tool not auto-approvable returns false",
            toolCalls: []llm.ToolCall{
                {Name: "get_issue", ServerOrigin: "https://api.github.com"},
                {Name: "create_issue", ServerOrigin: "https://api.github.com"},
            },
            checker:  &mockAutoApprover{approved: map[string]bool{"get_issue": true}},
            expected: false,
        },
        {
            name: "tool with empty server origin not auto-approvable",
            toolCalls: []llm.ToolCall{
                {Name: "builtin_tool", ServerOrigin: ""},
            },
            checker:  &mockAutoApprover{approveAll: true},
            expected: false,
        },
        {
            name: "mixed servers - all approved",
            toolCalls: []llm.ToolCall{
                {Name: "get_issue", ServerOrigin: "https://api.github.com"},
                {Name: "getJiraIssue", ServerOrigin: "https://mcp.atlassian.com"},
            },
            checker:  &mockAutoApprover{approveAll: true},
            expected: true,
        },
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            service := &MMPostStreamService{
                toolAutoApprover: tc.checker,
            }
            result := service.areAllToolCallsAutoApprovable(tc.toolCalls)
            require.Equal(t, tc.expected, result)
        })
    }
}
```

Mock for the test:

```go
type mockAutoApprover struct {
    approveAll bool
    approved   map[string]bool
}

func (m *mockAutoApprover) IsToolAutoApproved(serverBaseURL string, toolName string) bool {
    if m == nil {
        return false
    }
    if m.approveAll {
        // Still reject empty server origins (built-in tools)
        return serverBaseURL != ""
    }
    return m.approved[toolName]
}
```

#### Test: `TestStreamToPostAutoApproval`

Integration-style test that verifies the full `StreamToPost` flow with auto-approved tool calls. This test:
1. Creates a stream with tool call events where all tools are auto-approvable
2. Verifies the post gets the `auto_approved_tool_call` prop
3. Verifies the auto-execute callback is invoked

```go
func TestStreamToPostAutoApproval(t *testing.T) {
    tests := []struct {
        name                    string
        toolCalls               []llm.ToolCall
        isDM                    bool
        autoApprover            ToolAutoApprovalChecker
        expectAutoApprovedProp  bool
        expectCallbackInvoked   bool
    }{
        {
            name: "all tools auto-approvable in channel - auto-approved",
            toolCalls: []llm.ToolCall{
                {ID: "tc-1", Name: "get_issue", ServerOrigin: "https://api.github.com"},
            },
            isDM:                   false,
            autoApprover:           &mockAutoApprover{approveAll: true},
            expectAutoApprovedProp: true,
            expectCallbackInvoked:  true,
        },
        {
            name: "not all tools auto-approvable - standard flow",
            toolCalls: []llm.ToolCall{
                {ID: "tc-1", Name: "get_issue", ServerOrigin: "https://api.github.com"},
                {ID: "tc-2", Name: "create_issue", ServerOrigin: "https://api.github.com"},
            },
            isDM:                   false,
            autoApprover:           &mockAutoApprover{approved: map[string]bool{"get_issue": true}},
            expectAutoApprovedProp: false,
            expectCallbackInvoked:  false,
        },
        {
            name: "DM - no auto-approval check needed",
            toolCalls: []llm.ToolCall{
                {ID: "tc-1", Name: "get_issue", ServerOrigin: "https://api.github.com"},
            },
            isDM:                   true,
            autoApprover:           &mockAutoApprover{approveAll: true},
            expectAutoApprovedProp: false,
            expectCallbackInvoked:  false,
        },
        {
            name: "no auto-approver configured - standard flow",
            toolCalls: []llm.ToolCall{
                {ID: "tc-1", Name: "get_issue", ServerOrigin: "https://api.github.com"},
            },
            isDM:                   false,
            autoApprover:           nil,
            expectAutoApprovedProp: false,
            expectCallbackInvoked:  false,
        },
        {
            name: "built-in tool (no server origin) - standard flow",
            toolCalls: []llm.ToolCall{
                {ID: "tc-1", Name: "builtin_tool", ServerOrigin: ""},
            },
            isDM:                   false,
            autoApprover:           &mockAutoApprover{approveAll: true},
            expectAutoApprovedProp: false,
            expectCallbackInvoked:  false,
        },
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            // Test implementation details depend on how the mock client is set up.
            // The implementer should use the existing test patterns from the codebase.
            // Key assertions:
            //   - Check post.GetProp(AutoApprovedToolCallProp)
            //   - Check if callback was invoked
        })
    }
}
```

### File: `conversations/tool_handling_test.go` (extend existing)

#### Test: `TestAutoExecuteApprovedToolCalls`

This test verifies that `AutoExecuteApprovedToolCalls`:
1. Retrieves tool calls from KV
2. Calls `HandleToolCall` with all tool IDs
3. Results in `pending_tool_result` being set

The test should follow the pattern of the existing `TestHandleToolCallChannelStoresInKVAndRedactsProps` test.

```go
func TestAutoExecuteApprovedToolCalls(t *testing.T) {
    // Setup similar to existing tests:
    // - Create bot, user, channel, post with tool calls
    // - Store tool calls in KV
    // - Set auto_approved_tool_call prop on post
    // - Call AutoExecuteApprovedToolCalls
    // - Verify results stored in KV (ToolResultPrivateKVKey)
    // - Verify post updated with pending_tool_result prop
    // - Verify tool call results contain expected output
}
```

Test cases:
1. **Happy path**: All tool calls execute successfully, results stored in KV, `pending_tool_result` set
2. **Tool execution error**: One tool fails, result still stored with error status
3. **Missing KV data**: Tool calls not in KV - should log error and return
4. **Missing post**: Post not found - should log error and return

---

## Files Changed Summary

| File | Change Type | Description |
|------|-------------|-------------|
| `streaming/streaming.go` | MODIFY | Add `AutoApprovedToolCallProp` constant, `ToolAutoApprovalChecker` interface, `AutoExecuteCallback` type, `ToolAutoApprovalFunc` adapter, `areAllToolCallsAutoApprovable` method, setter methods, modify `EventTypeToolCalls` handler |
| `conversations/tool_handling.go` | MODIFY | Add `AutoExecuteApprovedToolCalls` method |
| `server/main.go` | MODIFY | Wire auto-approval checker and callback after line 259 |
| `streaming/auto_approval_test.go` | NEW | Tests for `areAllToolCallsAutoApprovable` and auto-approval integration |
| `conversations/tool_handling_test.go` | MODIFY | Add `TestAutoExecuteApprovedToolCalls` test |

---

## Imports Needed

### `streaming/streaming.go`
No new imports needed - already has `encoding/json`, `sync`, `github.com/mattermost/mattermost-plugin-ai/llm`, `github.com/mattermost/mattermost-plugin-ai/mmapi`, `github.com/mattermost/mattermost/server/public/model`.

### `conversations/tool_handling.go`
No new imports needed - already has `github.com/mattermost/mattermost-plugin-ai/llm`, `github.com/mattermost/mattermost-plugin-ai/streaming`.

### Test files
Will need `github.com/stretchr/testify/require` (already used throughout).

---

## What Does NOT Change

1. **DM flow**: The `isDMWithBot` check happens before the auto-approval check. DMs continue to work exactly as before.
2. **`HandleToolCall`**: No modifications needed. It already works correctly when all tool IDs are pre-accepted.
3. **`HandleToolResult`**: No modifications needed. It already handles the result approval flow correctly.
4. **Frontend**: This is Phase 4. The frontend will continue to work (showing the standard approval flow) until Phase 4 adds awareness of the `auto_approved_tool_call` prop. Without Phase 4, auto-approved tools will appear to "instantly complete" the call stage and jump to the result stage.
5. **API endpoints**: `handleToolCall` and `handleToolResult` in `api/api_post.go` remain unchanged.
6. **Phase 1 & 2 code**: `mcp/approved_servers.go`, `mcp/approved_servers_builtin.go`, `llm/tools.go`, `mcp/user_clients.go` remain unchanged.

---

## Edge Cases

1. **Empty tool call batch**: If the LLM returns an empty tool call list, `areAllToolCallsAutoApprovable` returns `false` (vacuous truth avoided), falling back to standard flow.

2. **Built-in tools mixed with MCP tools**: Built-in tools have empty `ServerOrigin`, so `IsToolAutoApproved("", "tool_name")` returns `false` (no URL patterns match empty string). This correctly falls back to standard flow.

3. **Tool name collision**: If a built-in tool and an MCP tool have the same name, the tool store already handles this (first wins in `ToolStore.AddTools`). The `ServerOrigin` from Phase 2 will be correct for whichever tool was actually registered.

4. **Config changes during execution**: The `ToolAutoApprovalFunc` adapter reads config on each invocation, but the auto-approval check happens once per tool call batch. A config change between the check and execution is not a security issue because:
   - If a previously-approved tool becomes non-approved: the execution was already committed
   - If a previously-non-approved tool becomes approved: it will be auto-approved in the next batch

5. **Concurrent auto-execution and user action**: The goroutine for auto-execution may race with a user clicking the approval UI (if Phase 4 hasn't been deployed yet or has a bug). `HandleToolCall` is safe to call in this scenario because it reads from KV atomically and the first successful call updates the post. The second call would fail gracefully because the tool call data may have been modified.

6. **Plugin restart during auto-execution**: If the plugin restarts between setting the `auto_approved_tool_call` prop and the goroutine executing, the tools will remain in the "pending" state. The user will see the approval UI and can manually approve. This is acceptable - auto-approval is a best-effort optimization.

---

## Implementation Summary

**Completed by: implementer-3**

### Changes Made

#### `streaming/streaming.go`
- Added `AutoApprovedToolCallProp` constant (`"auto_approved_tool_call"`)
- Added `ToolAutoApprovalChecker` interface with `IsToolAutoApproved(serverBaseURL, toolName)` method
- Added `AutoExecuteCallback` function type for triggering auto-execution
- Added `ToolAutoApprovalFunc` adapter type (function-to-interface adapter pattern)
- Added `toolAutoApprover` and `autoExecuteCallback` fields to `MMPostStreamService`
- Added `SetToolAutoApprover()` and `SetAutoExecuteCallback()` setter methods
- Added `areAllToolCallsAutoApprovable()` helper method (all-or-nothing batch check)
- Modified `EventTypeToolCalls` handler in `StreamToPost`:
  - After KV store write, checks if all tool calls are auto-approvable
  - Sets `auto_approved_tool_call = "true"` post prop when auto-approved
  - After post update and websocket event, launches goroutine to invoke `autoExecuteCallback`

#### `conversations/tool_handling.go`
- Added `AutoExecuteApprovedToolCalls(postID, requesterID)` method
  - Retrieves post and channel
  - Reads full tool calls from KV store
  - Collects all tool IDs and calls `HandleToolCall` with all tools pre-accepted
  - Signature matches `AutoExecuteCallback` type for direct wiring

#### `server/main.go`
- Added wiring after `SetMeetingsService`:
  - `SetToolAutoApprover` with `ToolAutoApprovalFunc` adapter that reads current config via `p.configuration.ApprovedMCPServers().IsToolAutoApproved()`
  - `SetAutoExecuteCallback` wired to `conversationsService.AutoExecuteApprovedToolCalls`

#### `streaming/auto_approval_test.go` (NEW)
- `TestAreAllToolCallsAutoApprovable`: 7 table-driven test cases covering nil checker, empty tools, all approved, partial approval, empty server origin, mixed servers
- `TestToolAutoApprovalFunc`: Verifies the function adapter pattern
- `TestStreamToPostAutoApproval`: 6 integration test cases covering auto-approved channel, multiple tools, partial approval fallback, DM bypass, nil approver, built-in tool fallback

#### `conversations/tool_handling_test.go` (EXTENDED)
- Enhanced `fakeMMClient` with `posts` and `channels` maps for `GetPost`/`GetChannel` support
- `TestAutoExecuteApprovedToolCalls`: 4 subtests - happy path with successful execution, tool error handling, missing post, missing KV data

### Key Design Decisions
1. **Setter methods** instead of constructor params to avoid breaking existing callers and support late initialization
2. **ToolAutoApprovalFunc adapter** for dynamic config reads (avoids stale config snapshots)
3. **No changes to HandleToolCall** - existing logic works correctly when all tool IDs are pre-accepted
4. **Goroutine for auto-execution** - non-blocking, safe because post and KV data are already persisted

### All Tests Passing
- `go test -v ./streaming/...` - 22 tests pass
- `go test -v ./conversations/...` - 9 tests pass (2 evals skipped)
- `go build -o /dev/null ./server/...` - compiles successfully
