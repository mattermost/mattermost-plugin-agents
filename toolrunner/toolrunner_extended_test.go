// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package toolrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

func TestToolRunner_ServerOriginPreserved(t *testing.T) {
	// Verify that ServerOrigin is preserved through tool execution and in the
	// resubmitted request posts.
	const serverOrigin = "https://mcp.example.com"

	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "mcp_tool", Arguments: json.RawMessage(`{}`), ServerOrigin: serverOrigin},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "done"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(testToolDef{name: "mcp_tool", serverOrigin: serverOrigin, result: "mcp_result"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "test"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)
	_, _ = result.Stream.ReadAll()

	// Verify the tool turn preserves server origin.
	require.Len(t, result.ToolTurns, 1)
	assert.Equal(t, serverOrigin, result.ToolTurns[0].AssistantToolCalls[0].ServerOrigin)

	// Verify the resubmitted request preserves server origin in bot post.
	secondReq := inner.capturedRequests[1]
	botPost := secondReq.Posts[len(secondReq.Posts)-1]
	require.Len(t, botPost.ToolUse, 1)
	assert.Equal(t, serverOrigin, botPost.ToolUse[0].ServerOrigin)
	assert.Equal(t, "mcp_result", botPost.ToolUse[0].Result)
	assert.Equal(t, llm.ToolCallStatusAutoApproved, botPost.ToolUse[0].Status)
}

func TestToolRunner_ApprovalAfterToolRound(t *testing.T) {
	// Round 1: auto-approved tool call executes.
	// Round 2: LLM returns a tool call that is NOT approved -> return unresolved.
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "safe_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 40, OutputTokens: 10}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Now I need approval"},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc2", Name: "dangerous_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(
		testToolDef{name: "safe_tool", result: "safe_result"},
		testToolDef{name: "dangerous_tool", result: "never_called"},
	)
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	// Only approve safe_tool.
	result, err := runner.Run(context.Background(), request, func(tc llm.ToolCall) bool {
		return tc.Name == "safe_tool"
	}, nil)
	require.NoError(t, err)

	// Consume stream first to ensure goroutine completes.
	var gotText bool
	var gotToolCalls bool
	for event := range result.Stream.Stream {
		switch event.Type {
		case llm.EventTypeText:
			gotText = true
		case llm.EventTypeToolCalls:
			gotToolCalls = true
		}
	}
	assert.True(t, gotText)
	assert.True(t, gotToolCalls)

	// One tool turn was executed (safe_tool).
	require.Len(t, result.ToolTurns, 1)
	assert.Equal(t, "safe_tool", result.ToolTurns[0].AssistantToolCalls[0].Name)
	assert.Equal(t, int64(40), result.ToolTurns[0].TokensIn)

	// LLM called twice.
	assert.Equal(t, 2, inner.callCount)
}

func TestToolRunner_OnToolTurnsCallback(t *testing.T) {
	// Verify that onToolTurns callback is called with accumulated tool turns.
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "tool_a", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Done"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(testToolDef{name: "tool_a", result: "result_a"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	var callbackTurns []ToolTurn
	var callbackCalled bool
	result, err := runner.Run(context.Background(), request, alwaysExecute, func(turns []ToolTurn) {
		callbackCalled = true
		callbackTurns = turns
	})
	require.NoError(t, err)

	_, _ = result.Stream.ReadAll()
	assert.True(t, callbackCalled)
	require.Len(t, callbackTurns, 1)
	assert.Equal(t, "tool_a", callbackTurns[0].AssistantToolCalls[0].Name)
	assert.Equal(t, "result_a", callbackTurns[0].ToolResults[0].Result)
}

func TestToolRunner_OnToolTurnsNotCalledWithoutToolUse(t *testing.T) {
	// Verify that onToolTurns callback is NOT called when there are no tool turns.
	inner := &testLLM{
		responses: []testResponse{{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Hello"},
				{Type: llm.EventTypeEnd},
			},
		}},
	}

	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: llm.NewNoTools()},
	}

	callbackCalled := false
	result, err := runner.Run(context.Background(), request, alwaysExecute, func(_ []ToolTurn) {
		callbackCalled = true
	})
	require.NoError(t, err)

	_, _ = result.Stream.ReadAll()
	assert.False(t, callbackCalled)
}

func TestToolRunner_UnloadedMCPToolReturnsLoadFirstError(t *testing.T) {
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "jira__get_issue", Arguments: json.RawMessage(`{"key":"MM-1"}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "I will load it first"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}
	store := llm.NewNoTools()
	store.SetUnloadedMCPTools([]llm.Tool{{Name: "jira__get_issue", Description: "Get issue", ServerOrigin: "https://jira.example.com"}})

	shouldExecuteCalls := 0
	result, err := New(inner).Run(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "get issue"}},
		Context: &llm.Context{Tools: store},
	}, func(llm.ToolCall) bool {
		shouldExecuteCalls++
		return true
	}, nil)
	require.NoError(t, err)

	text, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)
	assert.Equal(t, "I will load it first", text)
	assert.Zero(t, shouldExecuteCalls)
	require.Len(t, result.ToolTurns, 1)
	require.Len(t, result.ToolTurns[0].ToolResults, 1)
	assert.True(t, result.ToolTurns[0].ToolResults[0].IsError)
	assert.Contains(t, result.ToolTurns[0].ToolResults[0].Result, `load_tool`)
	assert.Contains(t, result.ToolTurns[0].ToolResults[0].Result, `"name":"jira__get_issue"`)
	assert.Contains(t, result.ToolTurns[0].ToolResults[0].Result, "before calling it")

	secondReq := inner.capturedRequests[1]
	require.Len(t, secondReq.Posts, 2)
	require.Len(t, secondReq.Posts[1].ToolUse, 1)
	assert.Equal(t, llm.ToolCallStatusError, secondReq.Posts[1].ToolUse[0].Status)
	assert.Contains(t, secondReq.Posts[1].ToolUse[0].Result, "available but not loaded")
}

func TestToolRunnerUnloadedToolErrorTelemetry(t *testing.T) {
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "jira__get_issue", Arguments: json.RawMessage(`{"key":"MM-1"}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "I will load it first"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}
	store := llm.NewNoTools()
	store.SetUnloadedMCPTools([]llm.Tool{{Name: "jira__get_issue", Description: "Get issue", ServerOrigin: "https://jira.example.com"}})
	telemetry := &fakeMCPDynamicTelemetry{}

	result, err := New(inner).Run(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "get issue"}},
		Context: &llm.Context{
			BotUsername: "matty",
			Tools:       store,
			ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: telemetry},
		},
	}, alwaysExecute, nil)
	require.NoError(t, err)

	_, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)
	require.Equal(t, []toolrunnerTelemetryEvent{{botName: "matty", event: "unloaded_tool_error", result: "error"}}, telemetry.events)
}

func TestToolRunnerSearchLoadCallSuccessTelemetry(t *testing.T) {
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "jira__get_issue", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Done"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}
	telemetry := &fakeMCPDynamicTelemetry{}
	llmCtx := &llm.Context{
		BotUsername: "matty",
		Tools:       newTestToolStore(testToolDef{name: "jira__get_issue", result: "issue"}),
		ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: telemetry},
	}
	llmCtx.MarkMCPDynamicToolSearch()
	llmCtx.MarkMCPDynamicToolLoaded("jira__get_issue")

	result, err := New(inner).Run(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "get issue"}},
		Context: llmCtx,
	}, alwaysExecute, nil)
	require.NoError(t, err)

	_, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)
	require.Equal(t, []toolrunnerTelemetryEvent{{botName: "matty", event: "search_load_call_success", result: "success"}}, telemetry.events)
}

func TestToolRunnerSearchLoadCallSuccessTelemetryOnlyOnce(t *testing.T) {
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "jira__get_issue", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc2", Name: "jira__get_issue", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Done"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}
	telemetry := &fakeMCPDynamicTelemetry{}
	llmCtx := &llm.Context{
		BotUsername: "matty",
		Tools:       newTestToolStore(testToolDef{name: "jira__get_issue", result: "issue"}),
		ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: telemetry},
	}
	llmCtx.MarkMCPDynamicToolSearch()
	llmCtx.MarkMCPDynamicToolLoaded("jira__get_issue")

	result, err := New(inner).Run(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "get issue"}},
		Context: llmCtx,
	}, alwaysExecute, nil)
	require.NoError(t, err)

	_, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)
	require.Equal(t, []toolrunnerTelemetryEvent{{botName: "matty", event: "search_load_call_success", result: "success"}}, telemetry.events)
}

func TestToolRunner_MixedVisibleAndUnloadedDoesNotExecuteVisible(t *testing.T) {
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "safe_tool", Arguments: json.RawMessage(`{}`)},
					{ID: "tc2", Name: "jira__get_issue", Arguments: json.RawMessage(`{}`)},
					{ID: "tc3", Name: "ghost_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Recovered"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}
	resolverCalls := 0
	store := llm.NewNoTools()
	store.AddTools([]llm.Tool{{
		Name: "safe_tool",
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			resolverCalls++
			return "safe", nil
		},
	}})
	store.SetUnloadedMCPTools([]llm.Tool{{Name: "jira__get_issue", Description: "Get issue", ServerOrigin: "https://jira.example.com"}})

	result, err := New(inner).Run(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "run tools"}},
		Context: &llm.Context{Tools: store},
	}, func(llm.ToolCall) bool {
		t.Fatal("shouldExecute must not be called for batches with unavailable tools")
		return true
	}, nil)
	require.NoError(t, err)

	_, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)
	assert.Zero(t, resolverCalls)
	require.Len(t, result.ToolTurns, 1)
	require.Len(t, result.ToolTurns[0].ToolResults, 3)
	assert.Contains(t, result.ToolTurns[0].ToolResults[0].Result, "batch contained unavailable tool(s): jira__get_issue, ghost_tool")
	assert.Contains(t, result.ToolTurns[0].ToolResults[1].Result, `load_tool`)
	assert.Equal(t, "unknown tool ghost_tool", result.ToolTurns[0].ToolResults[2].Result)
}

func TestToolRunner_ApprovalToolCallsPersistSchemaMetadata(t *testing.T) {
	inner := &testLLM{
		responses: []testResponse{{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "jira__create_issue", Arguments: json.RawMessage(`{"summary":"bug"}`)},
				}},
				{Type: llm.EventTypeEnd},
			},
		}},
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary": map[string]any{"type": "string"},
		},
	}
	store := llm.NewNoTools()
	store.AddTools([]llm.Tool{{
		Name:         "jira__create_issue",
		Description:  "Create a Jira issue",
		ServerOrigin: "https://jira.example.com",
		Schema:       schema,
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			t.Fatal("approval-required tool must not execute")
			return "", nil
		},
	}})

	result, err := New(inner).Run(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "create issue"}},
		Context: &llm.Context{Tools: store},
	}, neverExecute, nil)
	require.NoError(t, err)

	var pendingCalls []llm.ToolCall
	for event := range result.Stream.Stream {
		if event.Type == llm.EventTypeToolCalls {
			pendingCalls = append(pendingCalls, event.Value.([]llm.ToolCall)...)
		}
	}
	require.Len(t, pendingCalls, 1)
	assert.Equal(t, "Create a Jira issue", pendingCalls[0].Description)
	assert.Equal(t, "https://jira.example.com", pendingCalls[0].ServerOrigin)
	assert.Equal(t, "create_issue", pendingCalls[0].MCPBareName)
	assert.Equal(t, schema, pendingCalls[0].Schema)
	assert.Empty(t, result.ToolTurns)
}

func TestEnrichToolCallsForApprovalUsesScopedCatalogMetadata(t *testing.T) {
	store := llm.NewNoTools()
	store.AddTools([]llm.Tool{
		{Name: "jira__create_issue", Description: "Create a Jira issue", ServerOrigin: "https://jira.example.com", Schema: map[string]any{"type": "object"}},
		{Name: "github__create_issue", Description: "Create a GitHub issue", ServerOrigin: "https://github.example.com"},
	})

	enriched := enrichToolCallsForApproval([]llm.ToolCall{{
		ID:           "tc1",
		Name:         "create_issue",
		ServerOrigin: "https://jira.example.com",
		Arguments:    json.RawMessage(`{"summary":"bug"}`),
	}}, store)

	require.Len(t, enriched, 1)
	assert.Equal(t, "Create a Jira issue", enriched[0].Description)
	assert.Equal(t, "https://jira.example.com", enriched[0].ServerOrigin)
	assert.Equal(t, "create_issue", enriched[0].MCPBareName)
	assert.Equal(t, map[string]any{"type": "object"}, enriched[0].Schema)
}

func TestToolRunner_AutoExecutesScopedBareToolCall(t *testing.T) {
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "create_issue", ServerOrigin: "https://jira.example.com", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "done"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}
	store := llm.NewNoTools()
	store.AddTools([]llm.Tool{
		{
			Name:         "jira__create_issue",
			ServerOrigin: "https://jira.example.com",
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "jira-result", nil
			},
		},
		{
			Name:         "github__create_issue",
			ServerOrigin: "https://github.example.com",
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "github-result", nil
			},
		},
	})

	result, err := New(inner).Run(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "create"}},
		Context: &llm.Context{Tools: store},
	}, alwaysExecute, nil)
	require.NoError(t, err)
	_, _ = result.Stream.ReadAll()

	require.Len(t, result.ToolTurns, 1)
	require.Len(t, result.ToolTurns[0].ToolResults, 1)
	assert.Equal(t, "jira-result", result.ToolTurns[0].ToolResults[0].Result)
	assert.False(t, result.ToolTurns[0].ToolResults[0].IsError)
}

func TestToolRunner_MixedBatchSkippedDoesNotDisableToolsAfterRetryLimit(t *testing.T) {
	responses := make([]testResponse, 2)
	for i := range responses {
		responses[i] = testResponse{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: fmt.Sprintf("tc-available-%d", i), Name: "safe_tool", Arguments: json.RawMessage(`{}`)},
					{ID: fmt.Sprintf("tc-unknown-%d", i), Name: "ghost_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			},
		}
	}
	responses = append(responses, testResponse{
		events: []llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "Done"},
			{Type: llm.EventTypeEnd},
		},
	})

	inner := &testLLM{responses: responses}
	store := llm.NewNoTools()
	store.AddTools([]llm.Tool{{
		Name: "safe_tool",
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			t.Fatal("available tools must not execute in mixed unavailable batches")
			return "", nil
		},
	}})

	var capturedOpts [][]llm.LanguageModelOption
	runner := New(&optCapturingLLM{inner: inner, capturedOpts: &capturedOpts})
	result, err := runner.Run(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}, alwaysExecute, nil)
	require.NoError(t, err)

	_, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)

	require.Len(t, capturedOpts, 3)
	for i := range capturedOpts {
		assert.Empty(t, capturedOpts[i], "batch-skipped tools must not exhaust retry limit early")
	}
	require.Equal(t, 2, llm.CountTrailingFailedToolCalls(inner.capturedRequests[len(inner.capturedRequests)-1].Posts))
}

func TestExecuteToolsDefensiveUnloadedGuard(t *testing.T) {
	store := llm.NewNoTools()
	store.SetUnloadedMCPTools([]llm.Tool{{Name: "jira__get_issue", Description: "Get issue", ServerOrigin: "https://jira.example.com"}})

	results := New(nil).executeTools(context.Background(), []llm.ToolCall{{
		ID:        "tc1",
		Name:      "jira__get_issue",
		Arguments: json.RawMessage(`{}`),
	}}, llm.CompletionRequest{Context: &llm.Context{Tools: store}})

	require.Len(t, results, 1)
	assert.True(t, results[0].IsError)
	assert.Contains(t, results[0].Result, "available but not loaded")
	assert.Contains(t, results[0].Result, `load_tool`)
}
