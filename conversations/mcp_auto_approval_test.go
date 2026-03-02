// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapStreamWithMCPAutoApproval(t *testing.T) {
	t.Run("nil stream returns nil", func(t *testing.T) {
		result := wrapStreamWithMCPAutoApproval(nil, &llm.Context{}, &mcp.ApprovedMCPServersConfig{})
		assert.Nil(t, result)
	})

	t.Run("nil context returns original stream", func(t *testing.T) {
		stream := llm.NewStreamFromString("test")
		result := wrapStreamWithMCPAutoApproval(stream, nil, &mcp.ApprovedMCPServersConfig{})
		assert.Equal(t, stream, result)
	})

	t.Run("nil approved servers returns original stream", func(t *testing.T) {
		stream := llm.NewStreamFromString("test")
		ctx := &llm.Context{Tools: llm.NewToolStore(nil, false)}
		result := wrapStreamWithMCPAutoApproval(stream, ctx, nil)
		assert.Equal(t, stream, result)
	})

	t.Run("text events pass through unchanged", func(t *testing.T) {
		input := make(chan llm.TextStreamEvent, 3)
		input <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "hello"}
		input <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(input)

		stream := &llm.TextStreamResult{Stream: input}
		ctx := &llm.Context{Tools: llm.NewToolStore(nil, false)}
		approvedServers := &mcp.ApprovedMCPServersConfig{}

		result := wrapStreamWithMCPAutoApproval(stream, ctx, approvedServers)
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
		approvedServers := &mcp.ApprovedMCPServersConfig{
			Servers: []mcp.ApprovedMCPServer{
				{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: true, AutoApproveTools: []string{"search"}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(stream(input), ctx, approvedServers)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)
		assert.Equal(t, llm.EventTypeToolCalls, events[0].Type)

		resultToolCalls := events[0].Value.([]llm.ToolCall)
		// Status should be unchanged (zero value = Pending)
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
		approvedServers := &mcp.ApprovedMCPServersConfig{
			Servers: []mcp.ApprovedMCPServer{
				{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: true, AutoApproveTools: []string{"search", "getJiraIssue"}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(stream(input), ctx, approvedServers)
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
		approvedServers := &mcp.ApprovedMCPServersConfig{
			Servers: []mcp.ApprovedMCPServer{
				{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: true, AutoApproveTools: []string{"search"}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(stream(input), ctx, approvedServers)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)

		resultToolCalls := events[0].Value.([]llm.ToolCall)
		// Both should still be at default status (not auto-approved)
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
		approvedServers := &mcp.ApprovedMCPServersConfig{
			Servers: []mcp.ApprovedMCPServer{
				{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: true, AutoApproveTools: []string{"search"}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(stream(input), ctx, approvedServers)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)

		resultToolCalls := events[0].Value.([]llm.ToolCall)
		assert.Equal(t, llm.ToolCallStatusError, resultToolCalls[0].Status)
		assert.Equal(t, assert.AnError.Error(), resultToolCalls[0].Result)
	})

	t.Run("disabled approved server does not auto-approve", func(t *testing.T) {
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
		approvedServers := &mcp.ApprovedMCPServersConfig{
			Servers: []mcp.ApprovedMCPServer{
				{Name: "Atlassian", URLPatterns: []string{"mcp.atlassian.com"}, Enabled: false, AutoApproveTools: []string{"search"}},
			},
		}

		result := wrapStreamWithMCPAutoApproval(stream(input), ctx, approvedServers)
		events := collectStreamEvents(result)
		require.Len(t, events, 1)

		resultToolCalls := events[0].Value.([]llm.ToolCall)
		assert.Equal(t, llm.ToolCallStatusPending, resultToolCalls[0].Status)
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
			assert.Equal(t, tt.expected, hasAutoApprovedToolCalls(tt.toolCalls))
		})
	}
}

// stream wraps a channel in a TextStreamResult
func stream(ch <-chan llm.TextStreamEvent) *llm.TextStreamResult {
	return &llm.TextStreamResult{Stream: ch}
}

// collectStreamEvents reads all events from a stream until the channel closes
func collectStreamEvents(result *llm.TextStreamResult) []llm.TextStreamEvent {
	var events []llm.TextStreamEvent
	for event := range result.Stream {
		events = append(events, event)
	}
	return events
}
