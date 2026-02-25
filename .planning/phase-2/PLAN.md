# Phase 2: Tool Metadata Propagation - Implementation Plan

## Overview

Phase 2 adds a `ServerOrigin` field to both the `Tool` and `ToolCall` structs, and ensures this metadata flows from MCP server configuration through tool registration to tool call creation at streaming time. This enables Phase 3 to look up whether a tool call is auto-approvable.

## Data Flow Summary

```
MCP ServerConfig.BaseURL
    |
    v
UserClients.GetTools() -- sets Tool.ServerOrigin = client.config.BaseURL
    |
    v
ToolStore.AddTools() -- stores Tool with ServerOrigin in map[string]Tool
    |
    v
LLM returns ToolCall (only has ID, Name, Arguments -- no ServerOrigin)
    |
    v
EnrichToolCallsWithServerOrigin wraps the stream, intercepts EventTypeToolCalls,
looks up each ToolCall.Name in the ToolStore, and sets ToolCall.ServerOrigin
    |
    v
StreamToPost receives EventTypeToolCalls with ServerOrigin already populated
    |
    v
ToolCall is stored in KV / post props with ServerOrigin for Phase 3 to use
```

The critical insight: the LLM only returns `id`, `name`, and `arguments` for tool calls. It does NOT return `ServerOrigin`. Therefore, we must look up `ServerOrigin` from the `ToolStore` by wrapping the stream before it reaches the streaming service. The `ToolStore` already has all registered tools including their `ServerOrigin` (set during MCP registration).

---

## Step 2.1: Add `ServerOrigin` Field to `Tool` Struct

### File: `llm/tools.go`

**Change 1: Add `ServerOrigin` to `Tool` struct (line 25-30)**

Current:
```go
type Tool struct {
	Name        string
	Description string
	Schema      any
	Resolver    ToolResolver
}
```

New:
```go
type Tool struct {
	Name        string
	Description string
	Schema      any
	Resolver    ToolResolver
	// ServerOrigin identifies the MCP server this tool came from (the BaseURL).
	// Empty for built-in (non-MCP) tools.
	ServerOrigin string
}
```

**Change 2: Propagate `ServerOrigin` in `WithBoundParams` (line 38-45)**

The `WithBoundParams` method creates a new `Tool` copy. It must propagate `ServerOrigin`.

Current:
```go
func (t Tool) WithBoundParams(params map[string]interface{}) Tool {
	return Tool{
		Name:        t.Name,
		Description: t.Description,
		Schema:      removeSchemaProperties(t.Schema, params),
		Resolver:    wrapResolverWithBoundParams(t.Resolver, params),
	}
}
```

New:
```go
func (t Tool) WithBoundParams(params map[string]interface{}) Tool {
	return Tool{
		Name:         t.Name,
		Description:  t.Description,
		Schema:       removeSchemaProperties(t.Schema, params),
		Resolver:     wrapResolverWithBoundParams(t.Resolver, params),
		ServerOrigin: t.ServerOrigin,
	}
}
```

**No JSON tags needed on `Tool.ServerOrigin`**: The `Tool` struct is never JSON-serialized. It is an in-memory registration struct with a `Resolver` function field (functions cannot be JSON-serialized).

---

## Step 2.2: Add `ServerOrigin` Field to `ToolCall` Struct

### File: `llm/tools.go`

**Change: Add `ServerOrigin` to `ToolCall` struct (line 197-204)**

Current:
```go
type ToolCall struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Arguments   json.RawMessage `json:"arguments"`
	Result      string          `json:"result"`
	Status      ToolCallStatus  `json:"status"`
}
```

New:
```go
type ToolCall struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Arguments   json.RawMessage `json:"arguments"`
	Result      string          `json:"result"`
	Status      ToolCallStatus  `json:"status"`
	// ServerOrigin identifies the MCP server this tool came from (the BaseURL).
	// Empty for built-in tools. Used for auto-approval decisions.
	ServerOrigin string `json:"server_origin,omitempty"`
}
```

**JSON tag `omitempty` rationale**:
- Existing serialized `ToolCall` data (KV store, post props) without `server_origin` will deserialize cleanly (defaults to `""`).
- Built-in tools (where `ServerOrigin` is `""`) do not bloat JSON with an empty field.
- MCP tools will include `server_origin` in JSON when serialized.

### File: `llm/auto_run_tools.go`

**Change: Propagate `ServerOrigin` in resolved tool call construction (line 103-109)**

Current:
```go
resolvedToolCalls[j] = ToolCall{
	ID:        toolCalls[j].ID,
	Name:      toolCalls[j].Name,
	Arguments: toolCalls[j].Arguments,
	Result:    r.Result,
	Status:    status,
}
```

New:
```go
resolvedToolCalls[j] = ToolCall{
	ID:           toolCalls[j].ID,
	Name:         toolCalls[j].Name,
	Arguments:    toolCalls[j].Arguments,
	Result:       r.Result,
	Status:       status,
	ServerOrigin: toolCalls[j].ServerOrigin,
}
```

### No changes needed to `streaming.RedactToolCalls`

`RedactToolCalls` (streaming/streaming.go line 55-63) copies tool calls by value (`redacted[i] = toolCall`), so `ServerOrigin` is automatically copied. `ServerOrigin` is not sensitive data (it's just a URL), so it does not need redaction.

---

## Step 2.3: Populate `ServerOrigin` During MCP Tool Registration and Streaming

### Part A: MCP Tool Registration

#### File: `mcp/user_clients.go`

**Change: Set `ServerOrigin` when building `llm.Tool` from MCP tools (line 169-174)**

Current:
```go
tools = append(tools, llm.Tool{
	Name:        toolName,
	Description: tool.Description,
	Schema:      tool.InputSchema,
	Resolver:    c.createToolResolver(client, toolName),
})
```

New:
```go
tools = append(tools, llm.Tool{
	Name:         toolName,
	Description:  tool.Description,
	Schema:       tool.InputSchema,
	Resolver:     c.createToolResolver(client, toolName),
	ServerOrigin: client.config.BaseURL,
})
```

**How `client.config.BaseURL` is available**: Each `Client` (mcp/client.go line 38-48) stores its `config ServerConfig`. The `ServerConfig` struct has `BaseURL string` (mcp/client.go line 54). When `UserClients.GetTools()` iterates over `c.clients` (line 154), each `client` is a `*Client` with `client.config.BaseURL`.

**Embedded server note**: For the embedded Mattermost server, the config is `ServerConfig{Name: EmbeddedClientKey}` (client.go line 104), so `BaseURL` is `""`. This is correct: the embedded server should never match URL patterns for auto-approval.

### Part B: ServerOrigin Lookup and Stream Enrichment

#### File: `llm/tools.go`

**Change 1: Add `GetServerOrigin` helper method to `ToolStore` (after line 404)**

```go
// GetServerOrigin returns the ServerOrigin for a tool by name.
// Returns empty string if the tool is not found or has no server origin (built-in tools).
func (s *ToolStore) GetServerOrigin(toolName string) string {
	if tool, ok := s.tools[toolName]; ok {
		return tool.ServerOrigin
	}
	return ""
}
```

**Change 2: Add `EnrichToolCallsWithServerOrigin` function (after `GetServerOrigin`)**

```go
// EnrichToolCallsWithServerOrigin returns a new TextStreamResult that intercepts
// EventTypeToolCalls events and populates each ToolCall's ServerOrigin field
// by looking up the tool name in the provided ToolStore.
func EnrichToolCallsWithServerOrigin(stream *TextStreamResult, store *ToolStore) *TextStreamResult {
	if store == nil {
		return stream
	}

	enriched := make(chan TextStreamEvent)
	go func() {
		defer close(enriched)
		for event := range stream.Stream {
			if event.Type == EventTypeToolCalls {
				if toolCalls, ok := event.Value.([]ToolCall); ok {
					for i := range toolCalls {
						toolCalls[i].ServerOrigin = store.GetServerOrigin(toolCalls[i].Name)
					}
					event.Value = toolCalls
				}
			}
			enriched <- event
		}
	}()

	return &TextStreamResult{Stream: enriched}
}
```

**Design rationale**: `StreamToPost` in `streaming/streaming.go` does NOT have access to the `ToolStore`. The `ToolStore` lives on `llm.Context` in the `conversations` package. Rather than threading a lookup function through the streaming service (which would require per-stream variation since different users have different tools), we wrap the stream **before** it reaches `StreamToPost`. This keeps `streaming/streaming.go` completely unchanged.

### Part C: Wire Up Enrichment at Call Sites

The enrichment must be applied after `ChatCompletion` returns a stream and before the stream reaches the streaming service. There are exactly **two** call sites:

#### File: `conversations/conversations.go` - line 153-156

This is `ProcessUserRequestWithContext`, the main entry point for all user requests (DM and channel mentions, also called by regeneration).

Current:
```go
result, err := bot.LLM().ChatCompletion(completionRequest, opts...)
if err != nil {
	return nil, err
}
```

New:
```go
result, err := bot.LLM().ChatCompletion(completionRequest, opts...)
if err != nil {
	return nil, err
}

// Enrich tool calls with server origin for auto-approval decisions
result = llm.EnrichToolCallsWithServerOrigin(result, context.Tools)
```

Insert the enrichment line immediately after the error check on line 156, before the web search decoration on line 158.

#### File: `conversations/tool_handling.go` - line 471-474

This is `completeAndStreamToolResponse`, called after tool results are approved/rejected to continue the LLM conversation.

Current:
```go
result, err := bot.LLM().ChatCompletion(completionRequest, opts...)
if err != nil {
	return fmt.Errorf("failed to get chat completion: %w", err)
}
```

New:
```go
result, err := bot.LLM().ChatCompletion(completionRequest, opts...)
if err != nil {
	return fmt.Errorf("failed to get chat completion: %w", err)
}

// Enrich tool calls with server origin for auto-approval decisions
result = llm.EnrichToolCallsWithServerOrigin(result, llmContext.Tools)
```

Insert the enrichment line immediately after the error check on line 474, before the web search decoration on line 476.

---

## Summary of All Changes

| File | Location | Change |
|------|----------|--------|
| `llm/tools.go` | `Tool` struct (line ~25) | Add `ServerOrigin string` field |
| `llm/tools.go` | `WithBoundParams` (line ~38) | Add `ServerOrigin: t.ServerOrigin` to new Tool |
| `llm/tools.go` | `ToolCall` struct (line ~197) | Add `ServerOrigin string` with `json:"server_origin,omitempty"` |
| `llm/tools.go` | After `GetTool` (line ~404) | Add `GetServerOrigin(toolName string) string` method |
| `llm/tools.go` | After `GetServerOrigin` | Add `EnrichToolCallsWithServerOrigin(stream, store)` function |
| `llm/auto_run_tools.go` | resolvedToolCalls (line ~103) | Add `ServerOrigin: toolCalls[j].ServerOrigin` |
| `mcp/user_clients.go` | GetTools() append (line ~169) | Add `ServerOrigin: client.config.BaseURL` |
| `conversations/conversations.go` | After ChatCompletion (line ~156) | Add `result = llm.EnrichToolCallsWithServerOrigin(result, context.Tools)` |
| `conversations/tool_handling.go` | After ChatCompletion (line ~474) | Add `result = llm.EnrichToolCallsWithServerOrigin(result, llmContext.Tools)` |

### Files NOT changed:

| File | Reason |
|------|--------|
| `streaming/streaming.go` | Receives already-enriched stream; no changes needed |
| `streaming/RedactToolCalls` | `ServerOrigin` auto-copied by value; not sensitive |
| `bifrost/bifrost.go` | LLM layer; correctly produces ToolCall without ServerOrigin |
| `mcp/mcp.go` | No structural changes needed |

---

## Imports Needed

None. All files already import the required packages:
- `llm/tools.go` - uses only types from same package
- `mcp/user_clients.go` - `client.config.BaseURL` already accessible
- `llm/auto_run_tools.go` - uses only types from same package
- `conversations/conversations.go` - already imports `github.com/mattermost/mattermost-plugin-ai/llm`
- `conversations/tool_handling.go` - already imports `github.com/mattermost/mattermost-plugin-ai/llm`

---

## Unit Tests

All tests follow: Go standard formatting, table-driven tests, no mocking, no new testing libraries, license headers, snake_case file names.

### Test File: `llm/tools_test.go` (append to existing file)

#### Test 1: `TestGetServerOrigin`

```go
func TestGetServerOrigin(t *testing.T) {
	tests := []struct {
		name        string
		tools       []Tool
		lookupName  string
		expectedURL string
	}{
		{
			name: "MCP tool returns server origin",
			tools: []Tool{
				{Name: "get_issue", ServerOrigin: "https://mcp.atlassian.com/v2"},
			},
			lookupName:  "get_issue",
			expectedURL: "https://mcp.atlassian.com/v2",
		},
		{
			name: "built-in tool returns empty",
			tools: []Tool{
				{Name: "builtin_tool", ServerOrigin: ""},
			},
			lookupName:  "builtin_tool",
			expectedURL: "",
		},
		{
			name: "unknown tool returns empty",
			tools: []Tool{
				{Name: "known_tool", ServerOrigin: "https://example.com"},
			},
			lookupName:  "unknown_tool",
			expectedURL: "",
		},
		{
			name:        "empty store returns empty",
			tools:       []Tool{},
			lookupName:  "any_tool",
			expectedURL: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewToolStore(nil, false)
			store.AddTools(tc.tools)
			result := store.GetServerOrigin(tc.lookupName)
			assert.Equal(t, tc.expectedURL, result)
		})
	}
}
```

#### Test 2: `TestEnrichToolCallsWithServerOrigin`

```go
func TestEnrichToolCallsWithServerOrigin(t *testing.T) {
	tests := []struct {
		name            string
		toolCalls       []ToolCall
		storeTools      []Tool
		nilStore        bool
		expectedOrigins []string
	}{
		{
			name: "enriches MCP tool calls",
			toolCalls: []ToolCall{
				{ID: "1", Name: "get_issue"},
				{ID: "2", Name: "list_repos"},
			},
			storeTools: []Tool{
				{Name: "get_issue", ServerOrigin: "https://mcp.atlassian.com"},
				{Name: "list_repos", ServerOrigin: "https://api.github.com"},
			},
			expectedOrigins: []string{"https://mcp.atlassian.com", "https://api.github.com"},
		},
		{
			name: "built-in tools remain empty",
			toolCalls: []ToolCall{
				{ID: "1", Name: "builtin_tool"},
			},
			storeTools: []Tool{
				{Name: "builtin_tool", ServerOrigin: ""},
			},
			expectedOrigins: []string{""},
		},
		{
			name: "unknown tool gets empty origin",
			toolCalls: []ToolCall{
				{ID: "1", Name: "unknown_tool"},
			},
			storeTools: []Tool{
				{Name: "known_tool", ServerOrigin: "https://example.com"},
			},
			expectedOrigins: []string{""},
		},
		{
			name: "mixed MCP and built-in tools",
			toolCalls: []ToolCall{
				{ID: "1", Name: "get_issue"},
				{ID: "2", Name: "builtin_summarize"},
			},
			storeTools: []Tool{
				{Name: "get_issue", ServerOrigin: "https://mcp.atlassian.com"},
				{Name: "builtin_summarize", ServerOrigin: ""},
			},
			expectedOrigins: []string{"https://mcp.atlassian.com", ""},
		},
		{
			name: "nil store returns stream unmodified",
			toolCalls: []ToolCall{
				{ID: "1", Name: "any_tool"},
			},
			nilStore:        true,
			expectedOrigins: []string{""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inputCh := make(chan TextStreamEvent, 2)
			inputCh <- TextStreamEvent{
				Type:  EventTypeToolCalls,
				Value: tc.toolCalls,
			}
			close(inputCh)
			input := &TextStreamResult{Stream: inputCh}

			var store *ToolStore
			if !tc.nilStore {
				store = NewToolStore(nil, false)
				store.AddTools(tc.storeTools)
			}

			enriched := EnrichToolCallsWithServerOrigin(input, store)

			var resultToolCalls []ToolCall
			for event := range enriched.Stream {
				if event.Type == EventTypeToolCalls {
					if calls, ok := event.Value.([]ToolCall); ok {
						resultToolCalls = calls
					}
				}
			}

			require.Len(t, resultToolCalls, len(tc.expectedOrigins))
			for i, expected := range tc.expectedOrigins {
				assert.Equal(t, expected, resultToolCalls[i].ServerOrigin)
			}
		})
	}
}
```

#### Test 3: `TestToolCallServerOriginJSON`

```go
func TestToolCallServerOriginJSON(t *testing.T) {
	tests := []struct {
		name     string
		toolCall ToolCall
		check    func(t *testing.T, jsonStr string)
	}{
		{
			name: "MCP tool includes server_origin in JSON",
			toolCall: ToolCall{
				ID:           "1",
				Name:         "get_issue",
				ServerOrigin: "https://mcp.atlassian.com",
			},
			check: func(t *testing.T, jsonStr string) {
				assert.Contains(t, jsonStr, `"server_origin"`)
				assert.Contains(t, jsonStr, "mcp.atlassian.com")
			},
		},
		{
			name: "built-in tool omits server_origin from JSON",
			toolCall: ToolCall{
				ID:           "1",
				Name:         "builtin_tool",
				ServerOrigin: "",
			},
			check: func(t *testing.T, jsonStr string) {
				assert.NotContains(t, jsonStr, "server_origin")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.toolCall)
			require.NoError(t, err)
			tc.check(t, string(data))

			// Round-trip test
			var roundTripped ToolCall
			err = json.Unmarshal(data, &roundTripped)
			require.NoError(t, err)
			assert.Equal(t, tc.toolCall.ServerOrigin, roundTripped.ServerOrigin)
		})
	}
}

func TestToolCallServerOriginBackwardCompatibility(t *testing.T) {
	// Old JSON without server_origin should deserialize cleanly
	oldJSON := `{"id":"1","name":"old_tool","description":"","arguments":null,"result":"","status":0}`
	var deserialized ToolCall
	err := json.Unmarshal([]byte(oldJSON), &deserialized)
	require.NoError(t, err)
	assert.Equal(t, "", deserialized.ServerOrigin)
}
```

#### Test 4: `TestWithBoundParamsPreservesServerOrigin`

```go
func TestWithBoundParamsPreservesServerOrigin(t *testing.T) {
	original := Tool{
		Name:         "test_tool",
		Description:  "A test tool",
		ServerOrigin: "https://mcp.example.com",
		Resolver: func(ctx *Context, argsGetter ToolArgumentGetter) (string, error) {
			return "result", nil
		},
	}

	bound := original.WithBoundParams(map[string]interface{}{"key": "value"})

	assert.Equal(t, original.ServerOrigin, bound.ServerOrigin)
	assert.Equal(t, original.Name, bound.Name)
}
```

#### Test 5: `TestEnrichToolCallsPassesThroughNonToolEvents`

```go
func TestEnrichToolCallsPassesThroughNonToolEvents(t *testing.T) {
	inputCh := make(chan TextStreamEvent, 4)
	inputCh <- TextStreamEvent{Type: EventTypeText, Value: "hello"}
	inputCh <- TextStreamEvent{Type: EventTypeToolCalls, Value: []ToolCall{
		{ID: "1", Name: "test_tool"},
	}}
	inputCh <- TextStreamEvent{Type: EventTypeEnd}
	close(inputCh)

	store := NewToolStore(nil, false)
	store.AddTools([]Tool{{Name: "test_tool", ServerOrigin: "https://example.com"}})

	enriched := EnrichToolCallsWithServerOrigin(&TextStreamResult{Stream: inputCh}, store)

	var events []TextStreamEvent
	for event := range enriched.Stream {
		events = append(events, event)
	}

	require.Len(t, events, 3)
	assert.Equal(t, EventTypeText, events[0].Type)
	assert.Equal(t, "hello", events[0].Value)
	assert.Equal(t, EventTypeToolCalls, events[1].Type)
	toolCalls := events[1].Value.([]ToolCall)
	assert.Equal(t, "https://example.com", toolCalls[0].ServerOrigin)
	assert.Equal(t, EventTypeEnd, events[2].Type)
}
```

---

## Edge Cases and Backward Compatibility

1. **Existing KV store data**: Old `ToolCall` data without `server_origin` deserializes cleanly due to `omitempty` and Go zero-value behavior. `ServerOrigin` defaults to `""`, correctly meaning "no server origin."

2. **Existing post props**: Same as above. Old serialized `ToolCall` arrays in post props will deserialize correctly.

3. **Built-in tools**: Built-in tools from `toolProvider.GetTools(bot)` have no `ServerOrigin` (zero value `""`). They correctly won't match any approved server.

4. **Embedded MCP server**: Has `BaseURL: ""` (client.go line 104). Its `ServerOrigin` will be `""`, correctly excluding it from URL pattern matching.

5. **Tool name conflicts**: `UserClients.GetTools()` handles conflicts (first server wins, line 158-166). `ServerOrigin` is set from the winning server's `BaseURL`.

6. **Stream enrichment ordering**: `EnrichToolCallsWithServerOrigin` wraps the channel. Events flow through in order. Only `EventTypeToolCalls` events are modified; all others pass through unchanged.

7. **Auto-run tools**: The `AutoRunToolsWrapper` re-submits resolved tool calls as `Post.ToolUse`. The `ServerOrigin` is preserved via the explicit field copy in step 2.2's change to `auto_run_tools.go`.

---

## Implementation Order

1. Add `ServerOrigin` to `Tool` struct and `WithBoundParams` in `llm/tools.go`
2. Add `ServerOrigin` to `ToolCall` struct in `llm/tools.go`
3. Add `GetServerOrigin` method to `ToolStore` in `llm/tools.go`
4. Add `EnrichToolCallsWithServerOrigin` function in `llm/tools.go`
5. Set `ServerOrigin` in `mcp/user_clients.go` `GetTools()`
6. Propagate `ServerOrigin` in `llm/auto_run_tools.go` resolved tool calls
7. Add enrichment call in `conversations/conversations.go` line ~156
8. Add enrichment call in `conversations/tool_handling.go` line ~474
9. Add all unit tests in `llm/tools_test.go`

---

## Code Style Reminders

- Go standard formatting (goimports)
- snake_case file names (already satisfied - all modified files already exist)
- License header on all files (already present in files being modified; no new files created)
- Table-driven tests (all tests above use this pattern)
- No mocking or new testing libraries (tests use real structs and channels)
- Document all public APIs (all new public functions/methods have doc comments)

---

## Implementation Summary (Completed)

All changes from this plan have been implemented and verified:

### Files Modified

1. **`llm/tools.go`**:
   - Added `ServerOrigin string` field to `Tool` struct
   - Updated `WithBoundParams` to propagate `ServerOrigin`
   - Added `ServerOrigin string` with `json:"server_origin,omitempty"` tag to `ToolCall` struct
   - Added `GetServerOrigin(toolName string) string` method on `ToolStore`
   - Added `EnrichToolCallsWithServerOrigin(stream, store)` function

2. **`llm/auto_run_tools.go`**:
   - Added `ServerOrigin: toolCalls[j].ServerOrigin` to resolved tool call construction

3. **`mcp/user_clients.go`**:
   - Added `ServerOrigin: client.config.BaseURL` when building `llm.Tool` from MCP tools

4. **`conversations/conversations.go`**:
   - Added `result = llm.EnrichToolCallsWithServerOrigin(result, context.Tools)` after `ChatCompletion` in `ProcessUserRequestWithContext`

5. **`conversations/tool_handling.go`**:
   - Added `result = llm.EnrichToolCallsWithServerOrigin(result, llmContext.Tools)` after `ChatCompletion` in `completeAndStreamToolResponse`

### Tests Added

6. **`llm/tools_test.go`** - 5 new test functions:
   - `TestGetServerOrigin` - table-driven test for `ToolStore.GetServerOrigin`
   - `TestEnrichToolCallsWithServerOrigin` - table-driven test for stream enrichment
   - `TestToolCallServerOriginJSON` - JSON serialization/deserialization/backward compatibility
   - `TestWithBoundParamsPreservesServerOrigin` - verifies `ServerOrigin` preserved through `WithBoundParams`
   - `TestEnrichToolCallsPassesThroughNonToolEvents` - verifies non-tool events pass through unmodified

7. **`llm/auto_run_tools_test.go`** - 1 new test function:
   - `TestAutoRunToolsPreservesServerOrigin` - verifies `ServerOrigin` preserved through auto-run tool loop

### Test Results

All tests in `./llm/...` and `./mcp/...` pass successfully.
