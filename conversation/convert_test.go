// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlocksToPost(t *testing.T) {
	tests := []struct {
		name     string
		blocks   []ContentBlock
		role     string
		expected llm.Post
	}{
		{
			name:     "text blocks to message",
			blocks:   []ContentBlock{{Type: BlockTypeText, Text: "Hello"}, {Type: BlockTypeText, Text: "World"}},
			role:     "user",
			expected: llm.Post{Role: llm.PostRoleUser, Message: "Hello\nWorld"},
		},
		{
			name: "tool_use blocks to ToolUse",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", ServerOrigin: "https://mcp.example.com", Input: json.RawMessage(`{"q":"test"}`), Status: StatusSuccess, Shared: BoolPtr(true)},
			},
			role: "assistant",
			expected: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "search", ServerOrigin: "https://mcp.example.com", Arguments: json.RawMessage(`{"q":"test"}`), Status: llm.ToolCallStatusSuccess},
				},
			},
		},
		{
			name: "thinking block to reasoning",
			blocks: []ContentBlock{
				{Type: BlockTypeThinking, Text: "Let me think...", Signature: "sig123"},
			},
			role: "assistant",
			expected: llm.Post{
				Role:               llm.PostRoleBot,
				Reasoning:          "Let me think...",
				ReasoningSignature: "sig123",
			},
		},
		{
			name: "mixed block types in single turn",
			blocks: []ContentBlock{
				{Type: BlockTypeThinking, Text: "thinking...", Signature: "sig"},
				{Type: BlockTypeText, Text: "Here is the answer"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "weather", Input: json.RawMessage(`{}`), Status: StatusSuccess},
			},
			role: "assistant",
			expected: llm.Post{
				Role:               llm.PostRoleBot,
				Message:            "Here is the answer",
				Reasoning:          "thinking...",
				ReasoningSignature: "sig",
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "weather", Arguments: json.RawMessage(`{}`), Status: llm.ToolCallStatusSuccess},
				},
			},
		},
		{
			name:   "tool_result role",
			blocks: []ContentBlock{{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "result data", Status: StatusSuccess}},
			role:   "tool_result",
			expected: llm.Post{
				Role: llm.PostRoleUser,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Result: "result data", Status: llm.ToolCallStatusSuccess},
				},
			},
		},
		{
			name:     "empty blocks",
			blocks:   []ContentBlock{},
			role:     "user",
			expected: llm.Post{Role: llm.PostRoleUser},
		},
		{
			name: "multiple thinking blocks uses last one",
			blocks: []ContentBlock{
				{Type: BlockTypeThinking, Text: "first thought", Signature: "sig1"},
				{Type: BlockTypeThinking, Text: "second thought", Signature: "sig2"},
			},
			role: "assistant",
			expected: llm.Post{
				Role:               llm.PostRoleBot,
				Reasoning:          "second thought",
				ReasoningSignature: "sig2",
			},
		},
		{
			name: "file and image blocks are skipped",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "message"},
				{Type: BlockTypeFile, Filename: "f.txt", Content: "data"},
				{Type: BlockTypeImage, FileID: "img1", MimeType: "image/png"},
			},
			role: "user",
			expected: llm.Post{
				Role:    llm.PostRoleUser,
				Message: "message",
			},
		},
		{
			name: "annotations blocks are skipped",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "answer"},
				{Type: BlockTypeAnnotations, WebSearchContext: &WebSearchContext{Count: 1}},
			},
			role: "assistant",
			expected: llm.Post{
				Role:    llm.PostRoleBot,
				Message: "answer",
			},
		},
		{
			name: "tool_result merges into matching tool_use entry",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", ServerOrigin: "https://mcp.example.com", Input: json.RawMessage(`{"q":"test"}`), Status: StatusSuccess},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "found it", Status: StatusSuccess},
			},
			role: "assistant",
			expected: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "search", ServerOrigin: "https://mcp.example.com", Arguments: json.RawMessage(`{"q":"test"}`), Result: "found it", Status: llm.ToolCallStatusSuccess},
				},
			},
		},
		{
			name: "tool_result without matching tool_use creates standalone entry",
			blocks: []ContentBlock{
				{Type: BlockTypeToolResult, ToolUseID: "tc_orphan", Content: "orphan result", Status: StatusSuccess},
			},
			role: "assistant",
			expected: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc_orphan", Result: "orphan result", Status: llm.ToolCallStatusSuccess},
				},
			},
		},
		{
			name: "multiple tool_use and tool_result blocks merge correctly",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"a"}`), Status: StatusSuccess},
				{Type: BlockTypeToolUse, ID: "tc2", Name: "weather", Input: json.RawMessage(`{"city":"NYC"}`), Status: StatusSuccess},
				{Type: BlockTypeToolResult, ToolUseID: "tc2", Content: "72F sunny", Status: StatusSuccess},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "found it", Status: StatusSuccess},
			},
			role: "assistant",
			expected: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{"q":"a"}`), Result: "found it", Status: llm.ToolCallStatusSuccess},
					{ID: "tc2", Name: "weather", Arguments: json.RawMessage(`{"city":"NYC"}`), Result: "72F sunny", Status: llm.ToolCallStatusSuccess},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BlocksToPost(tt.blocks, tt.role, false)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBlocksToPost_RedactUnshared(t *testing.T) {
	blocks := []ContentBlock{
		{Type: BlockTypeToolUse, ID: "t-shared", Name: "search", Input: json.RawMessage(`{"q":"public"}`), Status: StatusSuccess, Shared: BoolPtr(true)},
		{Type: BlockTypeToolResult, ToolUseID: "t-shared", Content: "PUBLIC", Status: StatusSuccess, Shared: BoolPtr(true)},
		{Type: BlockTypeToolUse, ID: "t-private", Name: "read_dm", Input: json.RawMessage(`{"channel":"secret-dm"}`), Status: StatusSuccess, Shared: BoolPtr(false)},
		{Type: BlockTypeToolResult, ToolUseID: "t-private", Content: "SECRET", Status: StatusSuccess, Shared: BoolPtr(false)},
		{Type: BlockTypeToolUse, ID: "t-nilshared", Name: "foo", Input: json.RawMessage(`{"token":"xyz"}`), Status: StatusSuccess},
		{Type: BlockTypeToolResult, ToolUseID: "t-nilshared", Content: "ALSO SECRET", Status: StatusSuccess},
	}

	t.Run("redactUnshared=false", func(t *testing.T) {
		got := BlocksToPost(blocks, "assistant", false)
		require.Len(t, got.ToolUse, 3)
		results := map[string]string{}
		args := map[string]string{}
		for _, tc := range got.ToolUse {
			results[tc.ID] = tc.Result
			args[tc.ID] = string(tc.Arguments)
		}
		assert.Equal(t, "PUBLIC", results["t-shared"])
		assert.Equal(t, "SECRET", results["t-private"])
		assert.Equal(t, "ALSO SECRET", results["t-nilshared"])
		assert.JSONEq(t, `{"q":"public"}`, args["t-shared"])
		assert.JSONEq(t, `{"channel":"secret-dm"}`, args["t-private"])
		assert.JSONEq(t, `{"token":"xyz"}`, args["t-nilshared"])
	})

	t.Run("redactUnshared=true", func(t *testing.T) {
		got := BlocksToPost(blocks, "assistant", true)
		require.Len(t, got.ToolUse, 3)
		results := map[string]string{}
		args := map[string]string{}
		for _, tc := range got.ToolUse {
			results[tc.ID] = tc.Result
			args[tc.ID] = string(tc.Arguments)
		}
		assert.Equal(t, "PUBLIC", results["t-shared"])
		assert.Equal(t, UnsharedToolResultRedaction, results["t-private"])
		assert.Equal(t, UnsharedToolResultRedaction, results["t-nilshared"])
		// Shared tool_use arguments pass through; unshared and nil-shared
		// arguments are redacted to empty JSON so the LLM cannot paraphrase
		// private tool parameters into a channel-visible reply.
		assert.JSONEq(t, `{"q":"public"}`, args["t-shared"])
		assert.JSONEq(t, `{}`, args["t-private"])
		assert.JSONEq(t, `{}`, args["t-nilshared"])
	})
}

func TestPostToBlocks(t *testing.T) {
	tests := []struct {
		name     string
		post     llm.Post
		shared   bool
		expected []ContentBlock
	}{
		{
			name:     "message only",
			post:     llm.Post{Role: llm.PostRoleUser, Message: "Hello"},
			shared:   true,
			expected: []ContentBlock{{Type: BlockTypeText, Text: "Hello"}},
		},
		{
			name:   "reasoning produces thinking block",
			post:   llm.Post{Role: llm.PostRoleBot, Reasoning: "thinking...", ReasoningSignature: "sig"},
			shared: true,
			expected: []ContentBlock{
				{Type: BlockTypeThinking, Text: "thinking...", Signature: "sig"},
			},
		},
		{
			name: "tool use produces tool_use blocks",
			post: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{"q":"test"}`), Status: llm.ToolCallStatusSuccess, ServerOrigin: "https://mcp.example.com"},
				},
			},
			shared: false,
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", ServerOrigin: "https://mcp.example.com", Input: json.RawMessage(`{"q":"test"}`), Status: StatusSuccess, Shared: BoolPtr(false)},
			},
		},
		{
			name: "resolved tool use produces both tool_use and tool_result",
			post: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{}`), Result: "found it", Status: llm.ToolCallStatusSuccess},
				},
			},
			shared: true,
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "found it", Status: StatusSuccess, Shared: BoolPtr(true)},
			},
		},
		{
			name: "full assistant post with reasoning text and tools",
			post: llm.Post{
				Role:               llm.PostRoleBot,
				Message:            "Here is the answer",
				Reasoning:          "Let me think",
				ReasoningSignature: "sig",
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "tool", Arguments: json.RawMessage(`{}`), Status: llm.ToolCallStatusPending},
				},
			},
			shared: false,
			expected: []ContentBlock{
				{Type: BlockTypeThinking, Text: "Let me think", Signature: "sig"},
				{Type: BlockTypeText, Text: "Here is the answer"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "tool", Input: json.RawMessage(`{}`), Status: StatusPending, Shared: BoolPtr(false)},
			},
		},
		{
			name:     "empty post produces no blocks",
			post:     llm.Post{Role: llm.PostRoleUser},
			shared:   true,
			expected: nil,
		},
		{
			name: "multiple tool calls with results interleaved",
			post: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "tool1", Arguments: json.RawMessage(`{}`), Result: "r1", Status: llm.ToolCallStatusSuccess},
					{ID: "tc2", Name: "tool2", Arguments: json.RawMessage(`{}`), Status: llm.ToolCallStatusPending},
				},
			},
			shared: true,
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "tool1", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "r1", Status: StatusSuccess, Shared: BoolPtr(true)},
				{Type: BlockTypeToolUse, ID: "tc2", Name: "tool2", Input: json.RawMessage(`{}`), Status: StatusPending, Shared: BoolPtr(true)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PostToBlocks(tt.post, tt.shared)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoleMapping(t *testing.T) {
	tests := []struct {
		roleStr  string
		expected llm.PostRole
	}{
		{"user", llm.PostRoleUser},
		{"assistant", llm.PostRoleBot},
		{"tool_result", llm.PostRoleUser},
		{"system", llm.PostRoleSystem},
		{"unknown", llm.PostRoleUser},
	}

	for _, tt := range tests {
		t.Run(tt.roleStr, func(t *testing.T) {
			assert.Equal(t, tt.expected, RoleFromString(tt.roleStr))
		})
	}
}

func TestStatusConversion(t *testing.T) {
	tests := []struct {
		str    string
		status llm.ToolCallStatus
	}{
		{StatusPending, llm.ToolCallStatusPending},
		{StatusAccepted, llm.ToolCallStatusAccepted},
		{StatusRejected, llm.ToolCallStatusRejected},
		{StatusError, llm.ToolCallStatusError},
		{StatusSuccess, llm.ToolCallStatusSuccess},
		{StatusAutoApproved, llm.ToolCallStatusAutoApproved},
	}

	for _, tt := range tests {
		t.Run(tt.str, func(t *testing.T) {
			assert.Equal(t, tt.status, StatusFromString(tt.str))
			assert.Equal(t, tt.str, StatusToString(tt.status))
		})
	}
}

func TestStatusFromStringDefault(t *testing.T) {
	assert.Equal(t, llm.ToolCallStatusPending, StatusFromString("bogus_status"))
}

func TestStatusToStringDefault(t *testing.T) {
	assert.Equal(t, StatusPending, StatusToString(llm.ToolCallStatus(999)))
}

func TestRoleToString(t *testing.T) {
	tests := []struct {
		role     llm.PostRole
		expected string
	}{
		{llm.PostRoleUser, "user"},
		{llm.PostRoleBot, "assistant"},
		{llm.PostRoleSystem, "system"},
		{llm.PostRole(999), "user"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, RoleToString(tt.role))
		})
	}
}

func TestPostToBlocksToPostRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		post   llm.Post
		shared bool
	}{
		{
			name:   "message only",
			post:   llm.Post{Role: llm.PostRoleBot, Message: "Hello world"},
			shared: true,
		},
		{
			name: "reasoning and message",
			post: llm.Post{
				Role:               llm.PostRoleBot,
				Message:            "The answer is 42",
				Reasoning:          "Let me think about this",
				ReasoningSignature: "sig_abc",
			},
			shared: true,
		},
		{
			name: "tool use with result",
			post: llm.Post{
				Role:    llm.PostRoleBot,
				Message: "Here are the results",
				ToolUse: []llm.ToolCall{
					{
						ID:           "tc1",
						Name:         "search",
						ServerOrigin: "https://mcp.example.com",
						Arguments:    json.RawMessage(`{"q":"test"}`),
						Result:       "found it",
						Status:       llm.ToolCallStatusSuccess,
					},
				},
			},
			shared: false,
		},
		{
			name: "multiple tools mixed resolved and unresolved",
			post: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "tool1", Arguments: json.RawMessage(`{}`), Result: "r1", Status: llm.ToolCallStatusSuccess},
					{ID: "tc2", Name: "tool2", Arguments: json.RawMessage(`{"x":1}`), Status: llm.ToolCallStatusPending},
				},
			},
			shared: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := PostToBlocks(tt.post, tt.shared)
			role := RoleToString(tt.post.Role)
			roundTripped := BlocksToPost(blocks, role, false)

			assert.Equal(t, tt.post.Role, roundTripped.Role)
			assert.Equal(t, tt.post.Message, roundTripped.Message)
			assert.Equal(t, tt.post.Reasoning, roundTripped.Reasoning)
			assert.Equal(t, tt.post.ReasoningSignature, roundTripped.ReasoningSignature)
			assert.Equal(t, len(tt.post.ToolUse), len(roundTripped.ToolUse))
			for i := range tt.post.ToolUse {
				assert.Equal(t, tt.post.ToolUse[i].ID, roundTripped.ToolUse[i].ID)
				assert.Equal(t, tt.post.ToolUse[i].Name, roundTripped.ToolUse[i].Name)
				assert.Equal(t, tt.post.ToolUse[i].ServerOrigin, roundTripped.ToolUse[i].ServerOrigin)
				assert.JSONEq(t, string(tt.post.ToolUse[i].Arguments), string(roundTripped.ToolUse[i].Arguments))
				assert.Equal(t, tt.post.ToolUse[i].Result, roundTripped.ToolUse[i].Result)
				assert.Equal(t, tt.post.ToolUse[i].Status, roundTripped.ToolUse[i].Status)
			}
		})
	}
}
