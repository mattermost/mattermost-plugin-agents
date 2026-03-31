// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/assert"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BlocksToPost(tt.blocks, tt.role)
			assert.Equal(t, tt.expected, result)
		})
	}
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
