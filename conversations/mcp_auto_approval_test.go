// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPolicyChecker implements streaming.ToolPolicyChecker for tests.
type testPolicyChecker struct {
	servers []testPolicyServer
}

type testPolicyServer struct {
	urlPatterns       []string
	enabled           bool
	autoRun           map[string]bool
	autoRunEverywhere map[string]bool
}

func (c *testPolicyChecker) GetToolPolicy(serverBaseURL string, toolName string) (string, bool) {
	if c == nil || serverBaseURL == "" || toolName == "" {
		return "ask", false
	}
	for _, s := range c.servers {
		if !s.enabled {
			continue
		}
		for _, p := range s.urlPatterns {
			if matchesTestURL(serverBaseURL, p) {
				if s.autoRunEverywhere != nil && s.autoRunEverywhere[toolName] {
					return mcp.ToolPolicyAutoRunEverywhere, true
				}
				if s.autoRun[toolName] {
					return mcp.ToolPolicyAutoRun, true
				}
				return "ask", true
			}
		}
	}
	return "ask", false
}

func matchesTestURL(baseURL, pattern string) bool {
	// Simple: check if baseURL contains the pattern host
	return len(baseURL) > 0 && len(pattern) > 0 &&
		(baseURL == pattern || contains(baseURL, pattern))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestWrapStreamWithMCPAutoApproval(t *testing.T) {
	t.Run("nil stream returns nil", func(t *testing.T) {
		result := wrapStreamWithMCPAutoApproval(nil, &llm.Context{}, &testPolicyChecker{}, false)
		assert.Nil(t, result)
	})

	t.Run("nil context returns original stream", func(t *testing.T) {
		stream := llm.NewStreamFromString("test")
		result := wrapStreamWithMCPAutoApproval(stream, nil, &testPolicyChecker{}, false)
		assert.Equal(t, stream, result)
	})

	t.Run("nil policy checker returns original stream", func(t *testing.T) {
		stream := llm.NewStreamFromString("test")
		ctx := &llm.Context{Tools: llm.NewToolStore(nil, false)}
		result := wrapStreamWithMCPAutoApproval(stream, ctx, nil, false)
		assert.Equal(t, stream, result)
	})

	t.Run("text events pass through unchanged", func(t *testing.T) {
		input := make(chan llm.TextStreamEvent, 3)
		input <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "hello"}
		input <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(input)

		stream := &llm.TextStreamResult{Stream: input}
		ctx := &llm.Context{Tools: llm.NewToolStore(nil, false)}
		checker := &testPolicyChecker{}

		result := wrapStreamWithMCPAutoApproval(stream, ctx, checker, false)
		require.NotNil(t, result)

		events := collectStreamEvents(result)
		require.Len(t, events, 2)
		assert.Equal(t, llm.EventTypeText, events[0].Type)
		assert.Equal(t, "hello", events[0].Value)
		assert.Equal(t, llm.EventTypeEnd, events[1].Type)
	})

	t.Run("non-approved tool calls pass through unchanged", func(t *testing.T) {
		toolCalls := []llm.ToolCall{
			{ID: "tc1", Name: "unknown_tool", Arguments: json.RawMessage(`{"key":"val"}`)},
		}

		input := make(chan llm.TextStreamEvent, 2)
		input <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		close(input)

		toolStore := llm.NewToolStore(nil, false)
		ctx := &llm.Context{Tools: toolStore}
		checker := &testPolicyChecker{
			servers: []testPolicyServer{
				{urlPatterns: []string{"mcp.atlassian.com"}, enabled: true, autoRun: map[string]bool{"search": true}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(streamHelper(input), ctx, checker, false)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)
		assert.Equal(t, llm.EventTypeToolCalls, events[0].Type)

		resultToolCalls := events[0].Value.([]llm.ToolCall)
		assert.Equal(t, llm.ToolCallStatusPending, resultToolCalls[0].Status)
	})

	t.Run("all approved tools are auto-executed", func(t *testing.T) {
		toolCalls := []llm.ToolCall{
			{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{}`)},
			{ID: "tc2", Name: "getJiraIssue", Arguments: json.RawMessage(`{}`)},
		}

		input := make(chan llm.TextStreamEvent, 2)
		input <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		close(input)

		toolStore := llm.NewToolStore(nil, false)
		toolStore.AddTools([]llm.Tool{
			{
				Name:         "search",
				ServerOrigin: "https://mcp.atlassian.com/v1/mcp",
				Resolver: func(ctx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
					return "search result", nil
				},
			},
			{
				Name:         "getJiraIssue",
				ServerOrigin: "https://mcp.atlassian.com/v1/mcp",
				Resolver: func(ctx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
					return "issue result", nil
				},
			},
		})

		ctx := &llm.Context{Tools: toolStore}
		checker := &testPolicyChecker{
			servers: []testPolicyServer{
				{urlPatterns: []string{"mcp.atlassian.com"}, enabled: true, autoRun: map[string]bool{"search": true, "getJiraIssue": true}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(streamHelper(input), ctx, checker, false)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)
		assert.Equal(t, llm.EventTypeToolCalls, events[0].Type)

		resultToolCalls := events[0].Value.([]llm.ToolCall)
		require.Len(t, resultToolCalls, 2)

		assert.Equal(t, llm.ToolCallStatusAutoApproved, resultToolCalls[0].Status)
		assert.Equal(t, "search result", resultToolCalls[0].Result)
		assert.Equal(t, "https://mcp.atlassian.com/v1/mcp", resultToolCalls[0].ServerOrigin)

		assert.Equal(t, llm.ToolCallStatusAutoApproved, resultToolCalls[1].Status)
		assert.Equal(t, "issue result", resultToolCalls[1].Result)
	})

	t.Run("mixed approved and non-approved tools pass through unchanged", func(t *testing.T) {
		toolCalls := []llm.ToolCall{
			{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{}`)},
			{ID: "tc2", Name: "createJiraIssue", Arguments: json.RawMessage(`{}`)},
		}

		input := make(chan llm.TextStreamEvent, 2)
		input <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		close(input)

		toolStore := llm.NewToolStore(nil, false)
		toolStore.AddTools([]llm.Tool{
			{Name: "search", ServerOrigin: "https://mcp.atlassian.com/v1/mcp"},
			{Name: "createJiraIssue", ServerOrigin: "https://mcp.atlassian.com/v1/mcp"},
		})

		ctx := &llm.Context{Tools: toolStore}
		checker := &testPolicyChecker{
			servers: []testPolicyServer{
				{urlPatterns: []string{"mcp.atlassian.com"}, enabled: true, autoRun: map[string]bool{"search": true}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(streamHelper(input), ctx, checker, false)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)

		resultToolCalls := events[0].Value.([]llm.ToolCall)
		assert.Equal(t, llm.ToolCallStatusPending, resultToolCalls[0].Status)
		assert.Equal(t, llm.ToolCallStatusPending, resultToolCalls[1].Status)
	})

	t.Run("tool execution error sets error status", func(t *testing.T) {
		toolCalls := []llm.ToolCall{
			{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{}`)},
		}

		input := make(chan llm.TextStreamEvent, 2)
		input <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		close(input)

		toolStore := llm.NewToolStore(nil, false)
		toolStore.AddTools([]llm.Tool{
			{
				Name:         "search",
				ServerOrigin: "https://mcp.atlassian.com/v1/mcp",
				Resolver: func(ctx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
					return "", assert.AnError
				},
			},
		})

		ctx := &llm.Context{Tools: toolStore}
		checker := &testPolicyChecker{
			servers: []testPolicyServer{
				{urlPatterns: []string{"mcp.atlassian.com"}, enabled: true, autoRun: map[string]bool{"search": true}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(streamHelper(input), ctx, checker, false)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)

		resultToolCalls := events[0].Value.([]llm.ToolCall)
		assert.Equal(t, llm.ToolCallStatusError, resultToolCalls[0].Status)
		assert.Equal(t, assert.AnError.Error(), resultToolCalls[0].Result)
	})

	t.Run("disabled server does not auto-approve", func(t *testing.T) {
		toolCalls := []llm.ToolCall{
			{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{}`)},
		}

		input := make(chan llm.TextStreamEvent, 2)
		input <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		close(input)

		toolStore := llm.NewToolStore(nil, false)
		toolStore.AddTools([]llm.Tool{
			{Name: "search", ServerOrigin: "https://mcp.atlassian.com/v1/mcp"},
		})

		ctx := &llm.Context{Tools: toolStore}
		checker := &testPolicyChecker{
			servers: []testPolicyServer{
				{urlPatterns: []string{"mcp.atlassian.com"}, enabled: false, autoRun: map[string]bool{"search": true}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(streamHelper(input), ctx, checker, false)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)

		resultToolCalls := events[0].Value.([]llm.ToolCall)
		assert.Equal(t, llm.ToolCallStatusPending, resultToolCalls[0].Status)
	})

	t.Run("strict auto_run_everywhere_only leaves auto_run policy as pending", func(t *testing.T) {
		toolCalls := []llm.ToolCall{
			{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{}`)},
		}

		input := make(chan llm.TextStreamEvent, 2)
		input <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		close(input)

		toolStore := llm.NewToolStore(nil, false)
		toolStore.AddTools([]llm.Tool{
			{
				Name:         "search",
				ServerOrigin: "https://mcp.atlassian.com/v1/mcp",
				Resolver: func(ctx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
					return "should not run", nil
				},
			},
		})

		ctx := &llm.Context{Tools: toolStore}
		checker := &testPolicyChecker{
			servers: []testPolicyServer{
				{urlPatterns: []string{"mcp.atlassian.com"}, enabled: true, autoRun: map[string]bool{"search": true}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(streamHelper(input), ctx, checker, true)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)
		resultToolCalls := events[0].Value.([]llm.ToolCall)
		assert.Equal(t, llm.ToolCallStatusPending, resultToolCalls[0].Status)
		assert.Empty(t, resultToolCalls[0].Result)
	})

	t.Run("strict auto_run_everywhere_only executes auto_run_everywhere policy", func(t *testing.T) {
		toolCalls := []llm.ToolCall{
			{ID: "tc1", Name: "read", Arguments: json.RawMessage(`{}`)},
		}

		input := make(chan llm.TextStreamEvent, 2)
		input <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		close(input)

		toolStore := llm.NewToolStore(nil, false)
		toolStore.AddTools([]llm.Tool{
			{
				Name:         "read",
				ServerOrigin: "https://mcp.atlassian.com/v1/mcp",
				Resolver: func(ctx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
					return "ok", nil
				},
			},
		})

		ctx := &llm.Context{Tools: toolStore}
		checker := &testPolicyChecker{
			servers: []testPolicyServer{
				{urlPatterns: []string{"mcp.atlassian.com"}, enabled: true, autoRunEverywhere: map[string]bool{"read": true}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(streamHelper(input), ctx, checker, true)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)
		resultToolCalls := events[0].Value.([]llm.ToolCall)
		assert.Equal(t, llm.ToolCallStatusAutoApproved, resultToolCalls[0].Status)
		assert.Equal(t, "ok", resultToolCalls[0].Result)
	})
}

func TestHasAutoApprovedToolCalls(t *testing.T) {
	tests := []struct {
		name      string
		toolCalls []llm.ToolCall
		expected  bool
	}{
		{
			name:      "empty tool calls",
			toolCalls: []llm.ToolCall{},
			expected:  false,
		},
		{
			name: "all pending",
			toolCalls: []llm.ToolCall{
				{Status: llm.ToolCallStatusPending},
				{Status: llm.ToolCallStatusPending},
			},
			expected: false,
		},
		{
			name: "one auto-approved",
			toolCalls: []llm.ToolCall{
				{Status: llm.ToolCallStatusAutoApproved},
				{Status: llm.ToolCallStatusError},
			},
			expected: true,
		},
		{
			name: "all auto-approved",
			toolCalls: []llm.ToolCall{
				{Status: llm.ToolCallStatusAutoApproved},
				{Status: llm.ToolCallStatusAutoApproved},
			},
			expected: true,
		},
		{
			name: "error status counts as pre-executed",
			toolCalls: []llm.ToolCall{
				{Status: llm.ToolCallStatusError},
			},
			expected: true,
		},
		{
			name: "success status is not auto-approved",
			toolCalls: []llm.ToolCall{
				{Status: llm.ToolCallStatusSuccess},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, llm.HasPreExecutedToolCalls(tt.toolCalls))
		})
	}
}

// streamHelper wraps a channel in a TextStreamResult
func streamHelper(ch <-chan llm.TextStreamEvent) *llm.TextStreamResult {
	return &llm.TextStreamResult{Stream: ch}
}

// Ensure testPolicyChecker implements the interface at compile time
var _ streaming.ToolPolicyChecker = (*testPolicyChecker)(nil)

// collectStreamEvents reads all events from a stream until the channel closes
func collectStreamEvents(result *llm.TextStreamResult) []llm.TextStreamEvent {
	var events []llm.TextStreamEvent
	for event := range result.Stream {
		events = append(events, event)
	}
	return events
}
