# Implementation Plan: Mattermost Approved MCP Servers

## Problem Statement

Currently, every MCP tool call in the Mattermost Agents plugin requires explicit user approval before execution. In multiplayer (channel) scenarios, there is an additional approval step for sharing the tool result. This two-step approval is important for security but creates friction, especially for READ-only operations that don't modify any external data.

MCP (Model Context Protocol) has no standard mechanism to classify tools as READ vs WRITE. We want to introduce a **Mattermost Approved MCP Server** concept where we pre-classify tools from known MCP servers, automatically allowing READ-only tools to execute without user approval.

## Design Goals

1. **Auto-approve READ-only tools** from Mattermost-approved MCP servers (skip tool-call approval)
2. **Preserve result-sharing approval** in multiplayer channels (data leakage prevention)
3. **Ship with three built-in approved servers**: Atlassian, GitHub, Figma
4. **Enable end-user extensibility** via a JSON-based configuration format
5. **Minimize blast radius** of changes to the existing approval flow

---

## Architecture Overview

### New Concept: `ApprovedMCPServer`

An `ApprovedMCPServer` is a configuration object that declares which tools on a specific MCP server are classified as READ-only and can be auto-executed without user approval.

```json
{
  "approved_mcp_servers": [
    {
      "name": "GitHub",
      "url_patterns": ["api.githubcopilot.com"],
      "auto_approve_tools": [
        "get_me",
        "get_file_contents",
        "list_branches",
        "issue_read",
        "pull_request_read"
      ],
      "enabled": true
    }
  ]
}
```

### Flow Change Summary

#### DM Flow (Current: No approval needed → No change)
DM tool calls already execute immediately. No changes needed.

#### Channel Flow - Non-Approved Tool (Current: Two-step approval → No change)
```
LLM requests tool → User approves execution → Tool runs → User approves sharing result → Bot continues
```

#### Channel Flow - Approved READ Tool (NEW: Skip step 1, keep step 2)
```
LLM requests tool → Tool auto-executes → User approves sharing result → Bot continues
```

The key insight: we skip the "approve execution" step but **keep the "approve sharing result" step** in channels, because even READ data from external systems could contain sensitive information that shouldn't be leaked to a public channel without the requester's consent.

---

## Detailed Implementation Plan

### Phase 1: Data Model & Configuration

#### Step 1.1: Define `ApprovedMCPServer` Configuration Type

**File: `mcp/approved_servers.go` (new)**

Create the data model for approved MCP server configurations:

```go
package mcp

// ToolAccessLevel defines the access level of a tool
type ToolAccessLevel string

const (
    ToolAccessRead   ToolAccessLevel = "read"
    ToolAccessWrite  ToolAccessLevel = "write"
    ToolAccessDelete ToolAccessLevel = "delete"
    ToolAccessMixed  ToolAccessLevel = "mixed"
)

// ApprovedMCPServer defines a pre-approved MCP server with classified tools
type ApprovedMCPServer struct {
    // Name is a human-readable identifier for this approved server
    Name string `json:"name"`

    // URLPatterns are URL substrings used to match MCP server BaseURLs.
    // A configured MCP server matches if its BaseURL contains any of these patterns.
    URLPatterns []string `json:"url_patterns"`

    // AutoApproveTools lists tool names that are READ-only and can be
    // auto-executed without user approval in channel contexts.
    AutoApproveTools []string `json:"auto_approve_tools"`

    // Enabled controls whether this approved server config is active
    Enabled bool `json:"enabled"`
}

// ApprovedMCPServersConfig holds the list of all approved server configurations
type ApprovedMCPServersConfig struct {
    Servers []ApprovedMCPServer `json:"servers"`
}

// IsToolAutoApproved checks if a given tool from a given MCP server URL
// is pre-approved for auto-execution (i.e., it's a READ-only tool).
func (c *ApprovedMCPServersConfig) IsToolAutoApproved(serverBaseURL string, toolName string) bool {
    for _, server := range c.Servers {
        if !server.Enabled {
            continue
        }
        if !matchesURLPattern(serverBaseURL, server.URLPatterns) {
            continue
        }
        for _, approvedTool := range server.AutoApproveTools {
            if approvedTool == toolName {
                return true
            }
        }
    }
    return false
}

// matchesURLPattern checks if a base URL matches any of the given patterns
func matchesURLPattern(baseURL string, patterns []string) bool {
    for _, pattern := range patterns {
        if strings.Contains(baseURL, pattern) {
            return true
        }
    }
    return false
}
```

#### Step 1.2: Add Built-in Approved Server Definitions

**File: `mcp/approved_servers_builtin.go` (new)**

Embed the three Mattermost-approved server definitions as compile-time defaults. These are the baseline; user config can override or add to them.

```go
package mcp

// BuiltinApprovedServers returns the Mattermost-curated list of approved MCP servers.
// These are compiled into the plugin and represent Mattermost's assessment of which
// tools are READ-only on well-known MCP servers.
func BuiltinApprovedServers() []ApprovedMCPServer {
    return []ApprovedMCPServer{
        atlassianApprovedServer(),
        githubApprovedServer(),
        figmaApprovedServer(),
    }
}
```

This file would contain `atlassianApprovedServer()`, `githubApprovedServer()`, and `figmaApprovedServer()` functions each returning the full `ApprovedMCPServer` with all READ tool names from the companion docs (ATLASSIAN.md, GITHUB.md, FIGMA.md).

#### Step 1.3: Extend Plugin Configuration

**File: `mcp/mcp.go`** - Extend the existing `Config` struct:

```go
type Config struct {
    Enabled            bool                    `json:"enabled"`
    EnablePluginServer bool                    `json:"enablePluginServer"`
    Servers            []ServerConfig          `json:"servers"`
    EmbeddedServer     EmbeddedServerConfig    `json:"embeddedServer"`
    IdleTimeoutMinutes int                     `json:"idleTimeoutMinutes"`
    // NEW: User-defined approved MCP servers (merged with built-in list)
    ApprovedServers    []ApprovedMCPServer     `json:"approvedServers,omitempty"`
}
```

**File: `config/config.go`** - Add accessor to the `Container`:

```go
func (c *Container) ApprovedMCPServers() *mcp.ApprovedMCPServersConfig {
    cfg := c.cfg.Load()
    if cfg == nil {
        return &mcp.ApprovedMCPServersConfig{}
    }
    // Merge built-in + user-defined approved servers
    return mcp.MergeApprovedServers(
        mcp.BuiltinApprovedServers(),
        cfg.MCP.ApprovedServers,
    )
}
```

#### Step 1.4: Merge Logic for Built-in + User-Defined Approved Servers

**File: `mcp/approved_servers.go`** - Add merge function:

```go
// MergeApprovedServers combines built-in and user-defined approved server configs.
// User-defined configs with the same name as built-in ones override the built-in version.
// User-defined configs with new names are appended.
func MergeApprovedServers(builtin []ApprovedMCPServer, userDefined []ApprovedMCPServer) *ApprovedMCPServersConfig {
    merged := make(map[string]ApprovedMCPServer)

    // Start with built-in servers
    for _, s := range builtin {
        merged[s.Name] = s
    }

    // Override/extend with user-defined servers
    for _, s := range userDefined {
        merged[s.Name] = s
    }

    servers := make([]ApprovedMCPServer, 0, len(merged))
    for _, s := range merged {
        servers = append(servers, s)
    }

    return &ApprovedMCPServersConfig{Servers: servers}
}
```

---

### Phase 2: Tool Metadata Propagation

The key challenge is that when a tool call arrives for approval, we need to know **which MCP server** it came from to check if it's auto-approvable. Currently, `llm.ToolCall` only has `Name` - it doesn't carry server origin information.

#### Step 2.1: Add Server Origin to Tool Registration

**File: `llm/tools.go`** - Extend the `Tool` struct:

```go
type Tool struct {
    Name        string
    Description string
    Schema      any
    Resolver    ToolResolver
    // ServerOrigin identifies the MCP server this tool came from.
    // Empty for built-in tools.
    ServerOrigin string
}
```

**File: `mcp/user_clients.go`** - When building `llm.Tool` from MCP tools, populate `ServerOrigin` with the server's BaseURL:

```go
// In GetTools() method, around line 169
tools = append(tools, llm.Tool{
    Name:         toolName,
    Description:  tool.Description,
    Schema:       tool.InputSchema,
    Resolver:     c.createToolResolver(client, toolName),
    ServerOrigin: client.config.BaseURL, // NEW: propagate server origin
})
```

#### Step 2.2: Add Server Origin to ToolCall

**File: `llm/tools.go`** - Extend `ToolCall`:

```go
type ToolCall struct {
    ID          string          `json:"id"`
    Name        string          `json:"name"`
    Description string          `json:"description"`
    Arguments   json.RawMessage `json:"arguments"`
    Result      string          `json:"result"`
    Status      ToolCallStatus  `json:"status"`
    // ServerOrigin identifies the MCP server this tool came from.
    // Empty for built-in tools. Used for auto-approval decisions.
    ServerOrigin string         `json:"server_origin,omitempty"`
}
```

#### Step 2.3: Populate ServerOrigin During Streaming

**File: `streaming/streaming.go`** - In the `EventTypeToolCalls` handler (around line 351), after the tool calls are received from the LLM, look up each tool's `ServerOrigin` from the tool store before storing them. This requires the tool store to be accessible during streaming.

The approach: The stream event already contains `ToolCall` objects. We need to enrich them with `ServerOrigin` before they reach the approval flow. The best place to do this is at the point where tool calls are received, by looking up the tool name in the tools store.

We need to pass the tool store (or a lookup function) into `StreamToPost`. Since `StreamToPost` doesn't currently have access to the tool store, we'll add a `ToolServerOriginLookup` function:

```go
// New type for looking up server origin
type ToolServerOriginLookup func(toolName string) string
```

This function will be passed as a parameter or set as a field on the streaming service context. The `BuildLLMContextUserRequest` already creates the tool store, so we can extract a lookup from it.

---

### Phase 3: Core Auto-Approval Logic

#### Step 3.1: Add Auto-Approval Check to HandleToolCall

**File: `conversations/tool_handling.go`** - This is the primary hook point. Modify `HandleToolCall` to auto-approve READ tools from approved servers.

The current flow in `HandleToolCall` (line 195-212):

```go
for i := range tools {
    if slices.Contains(acceptedToolIDs, tools[i].ID) {
        // Execute tool...
    } else {
        tools[i].Result = "Tool call rejected by user"
        tools[i].Status = llm.ToolCallStatusRejected
    }
}
```

The new flow adds a pre-check: before presenting tools for approval, auto-approve any tools that are READ-only on approved servers:

```go
// NEW: Determine which tools can be auto-approved
approvedServers := c.configProvider.ApprovedMCPServers()
autoApprovedIDs := make([]string, 0)
for _, tc := range tools {
    if approvedServers.IsToolAutoApproved(tc.ServerOrigin, tc.Name) {
        autoApprovedIDs = append(autoApprovedIDs, tc.ID)
    }
}

// Merge auto-approved IDs with user-accepted IDs
allAcceptedIDs := append(acceptedToolIDs, autoApprovedIDs...)

for i := range tools {
    if slices.Contains(allAcceptedIDs, tools[i].ID) {
        // Execute tool...
    } else {
        tools[i].Result = "Tool call rejected by user"
        tools[i].Status = llm.ToolCallStatusRejected
    }
}
```

However, this approach has a problem: `HandleToolCall` is only called **after** the user has already been prompted for approval and responded. The real hook point needs to be earlier.

#### Step 3.2: Add Auto-Approval in the Streaming Layer (Primary Hook Point)

**File: `streaming/streaming.go`** - The real decision point is in `StreamToPost`, around line 365-381, where the code decides whether to show the approval UI or not.

Currently, when tool calls arrive in a channel (non-DM), they are:
1. Stored in KV store with full args (private)
2. Redacted on the post (public)
3. The post gets the `pending_tool_call` prop, triggering the approval UI

For auto-approvable tools, we need a different path. The approach:

**If ALL tools in the batch are auto-approvable**: Skip the approval UI entirely, execute them immediately, then go to result approval stage.

**If SOME tools are auto-approvable**: Still show the approval UI, but mark the auto-approvable ones as pre-accepted. The user only needs to approve the non-auto-approvable ones.

**Recommended approach**: For simplicity and security, go with the "all-or-nothing" approach first. If ALL tools in a batch are auto-approvable, auto-execute them all and skip to result approval. If ANY tool is not auto-approvable, fall back to the full approval flow for the entire batch.

The implementation requires `StreamToPost` to:
1. Have access to the approved servers config
2. Have access to a way to resolve/execute tools
3. Be able to transition directly to the "result approval" stage

Since `StreamToPost` currently doesn't have tool execution capability (it only handles post streaming), the cleaner approach is:

#### Step 3.3: Implement Auto-Approval via a New Post Property

Instead of modifying `StreamToPost` to execute tools, add a new post property that signals "these tools are pre-approved for execution." Then modify `HandleToolCall` to recognize this signal.

**New approach:**

1. **In `StreamToPost`** (streaming.go, EventTypeToolCalls handler): After determining the tool calls, check if all tools are auto-approvable. If so, instead of setting `pending_tool_call` and waiting for user action, set a new property `auto_approved_tool_call` and immediately trigger the tool call handler via a callback/event.

2. **Add a callback mechanism**: The streaming service already has a `Client` interface. Add a new method or use the existing websocket event system to trigger auto-execution:

```go
// In the EventTypeToolCalls handler, after line 365
isDMWithBot = mmapi.IsDMWith(post.UserId, channel)

if !isDMWithBot {
    // NEW: Check if all tools are auto-approvable
    allAutoApprovable := true
    if toolAutoApprover != nil {
        for _, tc := range toolCalls {
            if !toolAutoApprover.IsToolAutoApproved(tc.ServerOrigin, tc.Name) {
                allAutoApprovable = false
                break
            }
        }
    }

    if allAutoApprovable {
        // Store full tool calls in KV (still private)
        kvKey := ToolCallPrivateKVKey(post.Id, requesterID)
        p.mmClient.KVSet(kvKey, toolCalls)

        // Mark as auto-approved (instead of pending approval)
        post.AddProp("auto_approved_tool_call", "true")
        toolCallJSON, _ := json.Marshal(RedactToolCalls(toolCalls))
        post.AddProp(ToolCallProp, string(toolCallJSON))
        post.AddProp(ToolCallRedactedProp, "true")
        p.mmClient.UpdatePost(post)

        // Trigger auto-execution via callback
        if autoExecuteCallback != nil {
            go autoExecuteCallback(post.Id, requesterID)
        }
        return
    }

    // ... existing non-auto-approvable flow ...
}
```

3. **The auto-execute callback** calls into `HandleToolCall` with all tool IDs pre-accepted:

```go
// In conversations or a new handler
func (c *Conversations) AutoExecuteApprovedToolCalls(postID string, requesterID string) error {
    post, err := c.mmClient.GetPost(postID)
    // ... get channel, extract tool IDs ...

    // Call HandleToolCall with all tools pre-accepted
    allToolIDs := extractAllToolIDs(tools)
    return c.HandleToolCall(requesterID, post, channel, allToolIDs)
}
```

This reuses the existing `HandleToolCall` flow, which already handles:
- Executing tools
- Storing results in KV store
- Setting `pending_tool_result` for the result approval stage
- Sending websocket events

The result: **tools auto-execute, and the user still sees the result approval UI** (step 2 of the channel flow).

---

### Phase 4: Frontend Changes

#### Step 4.1: Update Tool Approval UI for Auto-Approved Tools

**File: `webapp/src/components/tool_approval_set.tsx`**

When tools have been auto-approved, the frontend should:
1. Skip showing the "Approve / Reject" UI for tool execution (stage 'call')
2. Show the tools as "Auto-approved (READ)" with a distinct visual indicator
3. Still show the result approval UI (stage 'result') as normal

Detect the auto-approved state from the post properties:
```typescript
const isAutoApproved = post.props?.auto_approved_tool_call === 'true';
```

When auto-approved, skip the 'call' stage rendering and show an informational message like:
> "These READ-only tools were auto-executed by Mattermost-approved policy. Review the results before sharing."

#### Step 4.2: Update Tool Card for Auto-Approved Status

**File: `webapp/src/components/tool_card.tsx`**

Add a visual badge/indicator for auto-approved tools. When a tool was auto-approved, show a small "Auto-approved" tag or icon instead of the approve/reject buttons.

#### Step 4.3: Tool Call Status (No Change)

**Decision:** Do **not** add `AutoApproved` to the `ToolCallStatus` enum. The `ToolCallStatus` remains unchanged. Auto-approval is modeled as a post-level property (`auto_approved_tool_call`) only. The frontend detects auto-approved tools via `post.props?.auto_approved_tool_call === 'true'` (see Step 4.1).

---

### Phase 5: Admin Configuration UI

#### Step 5.1: Approved Servers Settings Panel

Add a new section to the plugin's system console settings (or admin panel) that shows:
1. The list of built-in approved MCP servers (read-only display)
2. User-defined approved servers (editable)
3. Ability to disable built-in servers
4. Ability to add custom approved server configurations

The JSON configuration format allows admins to:
```json
{
  "mcp": {
    "approvedServers": [
      {
        "name": "Internal API",
        "url_patterns": ["internal-mcp.company.com"],
        "auto_approve_tools": ["get_status", "list_services", "get_config"],
        "enabled": true
      }
    ]
  }
}
```

---

### Phase 6: Testing

#### Step 6.1: Unit Tests

**File: `mcp/approved_servers_test.go` (new)**

- Test `IsToolAutoApproved` with various combinations:
  - Tool from approved server, in auto-approve list → true
  - Tool from approved server, NOT in auto-approve list → false
  - Tool from non-approved server → false
  - Disabled approved server → false
  - Empty URL patterns → false
  - Multiple URL patterns matching → true

- Test `MergeApprovedServers`:
  - Built-in only → returns built-in
  - User override of built-in → user version wins
  - User adds new server → appended
  - User disables built-in → disabled

**File: `conversations/tool_handling_test.go`**

- Test auto-approval in HandleToolCall:
  - All tools auto-approvable → all execute without user approval
  - Some tools auto-approvable → full approval flow for all
  - No tools auto-approvable → standard approval flow

**File: `streaming/streaming_test.go`**

- Test auto-approval detection in streaming:
  - Auto-approvable tools trigger callback
  - Non-auto-approvable tools follow standard flow
  - Mixed batch follows standard flow

#### Step 6.2: Integration Tests

- Test full end-to-end flow with a mock MCP server:
  - Configure an approved server
  - Trigger a READ tool call in a channel
  - Verify tool executes without approval prompt
  - Verify result approval is still required
  - Verify the result approval flow works normally

#### Step 6.3: E2E Tests

- Add Playwright tests for the frontend:
  - Auto-approved tool shows auto-approved badge
  - Result approval UI still appears for auto-approved tools
  - Standard approval flow unchanged for non-approved tools

---

## Implementation Order

| # | Task | Priority | Depends On |
|---|------|----------|------------|
| 1 | Define `ApprovedMCPServer` data model | High | - |
| 2 | Create built-in approved server definitions | High | 1 |
| 3 | Extend plugin config with `ApprovedServers` | High | 1 |
| 4 | Add merge logic for built-in + user servers | High | 1, 3 |
| 5 | Add `ServerOrigin` to `Tool` and `ToolCall` | High | 1 |
| 6 | Propagate `ServerOrigin` during MCP tool registration | High | 5 |
| 7 | Add auto-approval check in streaming layer | High | 4, 6 |
| 8 | Add auto-execute callback mechanism | High | 7 |
| 9 | Modify `HandleToolCall` for auto-approval | High | 7, 8 |
| 10 | Frontend: Update tool approval UI | Medium | 9 |
| 11 | Frontend: Add auto-approved badge/indicator | Medium | 10 |
| 12 | Unit tests for approved servers config | High | 1-4 |
| 13 | Unit tests for auto-approval flow | High | 7-9 |
| 14 | Integration tests | Medium | 9 |
| 15 | E2E tests | Medium | 10, 11 |
| 16 | Admin configuration UI | Low | 3, 4 |

---

## JSON Schema for End-User Custom Approved MCP Servers

End users (system admins) can define their own approved MCP servers using this JSON structure in the plugin settings:

```json
{
  "approvedServers": [
    {
      "name": "string - Human-readable name for this approved server",
      "url_patterns": [
        "string - URL substring to match against MCP server BaseURL"
      ],
      "auto_approve_tools": [
        "string - Tool name that is READ-only and can be auto-executed"
      ],
      "enabled": true
    }
  ]
}
```

### Full Example Configuration

```json
{
  "approvedServers": [
    {
      "name": "GitHub",
      "url_patterns": ["api.githubcopilot.com"],
      "auto_approve_tools": [
        "get_me", "get_file_contents", "list_branches", "list_commits",
        "get_commit", "search_code", "search_repositories", "issue_read",
        "list_issues", "search_issues", "pull_request_read",
        "list_pull_requests", "search_pull_requests", "search_users",
        "get_label", "list_issue_types", "get_tag", "list_tags",
        "get_latest_release", "get_release_by_tag", "list_releases",
        "actions_get", "actions_list", "get_job_logs",
        "get_code_scanning_alert", "list_code_scanning_alerts",
        "get_dependabot_alert", "list_dependabot_alerts",
        "get_discussion", "get_discussion_comments",
        "list_discussion_categories", "list_discussions",
        "get_gist", "list_gists", "get_repository_tree", "list_label",
        "get_notification_details", "list_notifications", "search_orgs",
        "projects_get", "projects_list",
        "get_secret_scanning_alert", "list_secret_scanning_alerts",
        "get_global_security_advisory", "list_global_security_advisories",
        "list_org_repository_security_advisories",
        "list_repository_security_advisories",
        "list_starred_repositories",
        "get_copilot_job_status", "get_copilot_space", "list_copilot_spaces",
        "github_support_docs_search"
      ],
      "enabled": true
    },
    {
      "name": "Atlassian",
      "url_patterns": ["mcp.atlassian.com"],
      "auto_approve_tools": [
        "search", "fetch", "atlassianUserInfo",
        "getAccessibleAtlassianResources",
        "getConfluenceSpaces", "getConfluencePage",
        "getPagesInConfluenceSpace", "getConfluencePageAncestors",
        "getConfluencePageDescendants", "getConfluencePageFooterComments",
        "getConfluencePageInlineComments", "searchConfluenceUsingCql",
        "getJiraIssue", "getJiraIssueRemoteIssueLinks",
        "getTransitionsForJiraIssue", "getVisibleJiraProjects",
        "getJiraProjectIssueTypesMetadata", "getJiraIssueTypeMetaWithFields",
        "lookupJiraAccountId", "searchJiraIssuesUsingJql"
      ],
      "enabled": true
    },
    {
      "name": "Figma",
      "url_patterns": ["mcp.figma.com"],
      "auto_approve_tools": [
        "get_design_context", "get_metadata", "get_screenshot",
        "get_variable_defs", "get_figjam", "create_design_system_rules",
        "get_code_connect_map", "get_code_connect_suggestions", "whoami"
      ],
      "enabled": true
    }
  ]
}
```

---

## Security Considerations

1. **Result approval preserved in channels**: Even auto-approved READ tools still require result approval before data is shared in channels. This prevents data leakage.

2. **Server identity verification**: URL pattern matching is the primary mechanism. Admins should use specific enough patterns to avoid false matches. Consider adding server certificate or fingerprint verification in future iterations.

3. **Tool name spoofing**: A malicious MCP server could name its tools identically to an approved server's tools. The URL pattern check prevents this - the tool must come from a URL matching the approved server's pattern.

4. **DM behavior unchanged**: DMs already auto-execute all tools. This change only affects channel (multiplayer) scenarios.

5. **Admin control**: Only system admins can configure approved servers. Users cannot override this at the individual level.

6. **Audit trail**: All auto-approved tool executions are still logged and stored on posts, so they are auditable.

7. **Defense in depth**: The `all-or-nothing` approach for mixed batches (where some tools are auto-approvable and some aren't) defaults to requiring full approval, preventing partial auto-execution that could confuse users.

---

## Key Files Modified

| File | Change |
|------|--------|
| `mcp/approved_servers.go` | **NEW** - Data model, matching logic, merge function |
| `mcp/approved_servers_builtin.go` | **NEW** - Built-in Atlassian/GitHub/Figma definitions |
| `mcp/approved_servers_test.go` | **NEW** - Unit tests |
| `mcp/mcp.go` | **MODIFY** - Add `ApprovedServers` to `Config` |
| `mcp/user_clients.go` | **MODIFY** - Add `ServerOrigin` when building `llm.Tool` |
| `llm/tools.go` | **MODIFY** - Add `ServerOrigin` to `Tool` and `ToolCall` |
| `config/config.go` | **MODIFY** - Add `ApprovedMCPServers()` accessor |
| `streaming/streaming.go` | **MODIFY** - Add auto-approval detection in `StreamToPost` |
| `conversations/tool_handling.go` | **MODIFY** - Handle auto-approved tool execution |
| `webapp/src/components/tool_approval_set.tsx` | **MODIFY** - Skip call-stage UI for auto-approved |
| `webapp/src/components/tool_card.tsx` | **MODIFY** - Add auto-approved visual indicator |

---

## Companion Documents

- **[ATLASSIAN.md](./ATLASSIAN.md)** - Full tool classification for the Atlassian Remote MCP Server (29 tools, 20 READ)
- **[GITHUB.md](./GITHUB.md)** - Full tool classification for the GitHub Remote MCP Server (88 tools, 56 READ)
- **[FIGMA.md](./FIGMA.md)** - Full tool classification for the Figma Remote MCP Server (13 tools, 9 READ)
