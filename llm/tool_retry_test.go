// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCountTrailingFailedToolCalls(t *testing.T) {
	tests := []struct {
		name     string
		posts    []Post
		expected int
	}{
		{
			name: "counts consecutive trailing failures across posts",
			posts: []Post{
				{Role: PostRoleUser, Message: "run the tool"},
				{Role: PostRoleBot, ToolUse: []ToolCall{
					{Status: ToolCallStatusError},
					{Status: ToolCallStatusError},
				}},
				{Role: PostRoleBot, ToolUse: []ToolCall{
					{Status: ToolCallStatusError},
				}},
			},
			expected: 3,
		},
		{
			name: "stops counting after a successful tool result",
			posts: []Post{
				{Role: PostRoleUser, Message: "run the tool"},
				{Role: PostRoleBot, ToolUse: []ToolCall{
					{Status: ToolCallStatusError},
				}},
				{Role: PostRoleBot, ToolUse: []ToolCall{
					{Status: ToolCallStatusSuccess},
				}},
				{Role: PostRoleBot, ToolUse: []ToolCall{
					{Status: ToolCallStatusError},
				}},
			},
			expected: 1,
		},
		{
			name: "ignores system posts but stops on non-executed tool batches",
			posts: []Post{
				{Role: PostRoleUser, Message: "run the tool"},
				{Role: PostRoleBot, ToolUse: []ToolCall{
					{Status: ToolCallStatusError},
				}},
				{Role: PostRoleSystem, Message: "system prompt"},
				{Role: PostRoleBot, ToolUse: []ToolCall{
					{Status: ToolCallStatusRejected},
				}},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, CountTrailingFailedToolCalls(tt.posts))
		})
	}
}

func TestEnsureToolRetryLimitSystemMessage(t *testing.T) {
	tests := []struct {
		name        string
		posts       []Post
		expected    []Post
		assertInput func(*testing.T, []Post)
	}{
		{
			name: "prepends a system post when none exists",
			posts: []Post{
				{Role: PostRoleUser, Message: "hello"},
			},
			expected: []Post{
				{Role: PostRoleSystem, Message: ToolRetryLimitSystemMessage},
				{Role: PostRoleUser, Message: "hello"},
			},
		},
		{
			name: "appends message to existing system prompt",
			posts: []Post{
				{Role: PostRoleSystem, Message: "base prompt"},
				{Role: PostRoleUser, Message: "hello"},
			},
			expected: []Post{
				{Role: PostRoleSystem, Message: "base prompt\n\n" + ToolRetryLimitSystemMessage},
				{Role: PostRoleUser, Message: "hello"},
			},
			assertInput: func(t *testing.T, posts []Post) {
				t.Helper()
				assert.Equal(t, "base prompt", posts[0].Message)
			},
		},
		{
			name: "returns posts unchanged when retry message already exists",
			posts: []Post{
				{Role: PostRoleSystem, Message: ToolRetryLimitSystemMessage},
				{Role: PostRoleUser, Message: "hello"},
			},
			expected: []Post{
				{Role: PostRoleSystem, Message: ToolRetryLimitSystemMessage},
				{Role: PostRoleUser, Message: "hello"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EnsureToolRetryLimitSystemMessage(tt.posts)
			assert.Equal(t, tt.expected, result)
			if tt.assertInput != nil {
				tt.assertInput(t, tt.posts)
			}
		})
	}
}
