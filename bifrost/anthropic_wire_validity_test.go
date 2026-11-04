// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
)

// anthropicWireRequests converts posts through both bifrost request paths and
// Bifrost's Anthropic provider converters, returning the JSON body each path
// would send to Anthropic keyed by path name.
func anthropicWireRequests(t *testing.T, posts []llm.Post) map[string][]byte {
	t.Helper()

	b := &LLM{provider: schemas.Anthropic}
	cfg := llm.LanguageModelConfig{Model: "claude-sonnet-4-6", MaxGeneratedTokens: 1024}
	request := llm.CompletionRequest{Posts: posts}

	ctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()

	wireChat, err := anthropic.ToAnthropicChatRequest(ctx, b.convertToBifrostRequest(request, cfg))
	require.NoError(t, err)
	chatBody, err := json.Marshal(wireChat)
	require.NoError(t, err)

	respReq, err := b.convertToBifrostResponsesRequest(request, cfg)
	require.NoError(t, err)
	wireResponses, err := anthropic.ToAnthropicResponsesRequest(ctx, respReq)
	require.NoError(t, err)
	responsesBody, err := json.Marshal(wireResponses)
	require.NoError(t, err)

	return map[string][]byte{"chat": chatBody, "responses": responsesBody}
}

// wireToolUseIDs returns the IDs of every tool_use block in an Anthropic
// request body, in order.
func wireToolUseIDs(t *testing.T, body []byte) []string {
	t.Helper()

	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &req))

	var ids []string
	for _, msg := range req.Messages {
		var blocks []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(msg.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type == "tool_use" {
				ids = append(ids, block.ID)
			}
		}
	}
	return ids
}

func TestAnthropicRequestsNeverCarryEmptyContent(t *testing.T) {
	toolUse := func(id, result string) llm.ToolCall {
		return llm.ToolCall{ID: id, Name: "search", Arguments: json.RawMessage(`{"q":"x"}`), Result: result}
	}

	tests := []struct {
		name           string
		posts          []llm.Post
		wantToolUseIDs []string
	}{
		{
			name: "tool_use with empty result",
			posts: []llm.Post{
				{Role: llm.PostRoleBot, ToolUse: []llm.ToolCall{toolUse("call1", "")}},
			},
			wantToolUseIDs: []string{"call1"},
		},
		{
			name: "tool_use with whitespace-only result",
			posts: []llm.Post{
				{Role: llm.PostRoleBot, Message: "checking", ToolUse: []llm.ToolCall{toolUse("call1", " \n")}},
			},
			wantToolUseIDs: []string{"call1"},
		},
		{
			name: "tool_use with empty arguments",
			posts: []llm.Post{
				{Role: llm.PostRoleBot, ToolUse: []llm.ToolCall{{ID: "call1", Name: "search", Result: "found"}}},
			},
			wantToolUseIDs: []string{"call1"},
		},
		{
			name: "nameless orphan tool result next to a real tool_use",
			posts: []llm.Post{
				{Role: llm.PostRoleBot, ToolUse: []llm.ToolCall{
					toolUse("call1", "found"),
					{ID: "orphan", Result: "result for a tool_use that lives elsewhere"},
				}},
			},
			wantToolUseIDs: []string{"call1"},
		},
		{
			name: "user-role post carrying only tool results",
			posts: []llm.Post{
				{Role: llm.PostRoleBot, Message: "earlier answer"},
				{Role: llm.PostRoleUser, ToolUse: []llm.ToolCall{{ID: "orphan", Result: "stray"}}},
			},
		},
		{
			name: "empty assistant post left by a failed stream",
			posts: []llm.Post{
				{Role: llm.PostRoleBot},
			},
		},
		{
			name: "whitespace-only assistant post",
			posts: []llm.Post{
				{Role: llm.PostRoleBot, Message: "  \n"},
			},
		},
		{
			name: "empty user post",
			posts: []llm.Post{
				{Role: llm.PostRoleBot, Message: "earlier answer"},
				{Role: llm.PostRoleUser},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, systemPrompt := range []string{"You are helpful.", ""} {
				posts := append([]llm.Post{
					{Role: llm.PostRoleSystem, Message: systemPrompt},
					{Role: llm.PostRoleUser, Message: "first question"},
				}, tt.posts...)
				posts = append(posts, llm.Post{Role: llm.PostRoleUser, Message: "next question"})

				for path, body := range anthropicWireRequests(t, posts) {
					assert.Empty(t, llmtest.AnthropicRequestViolations(body),
						"%s path (system prompt %q) would be rejected by Anthropic: %s", path, systemPrompt, body)
					assert.Equal(t, tt.wantToolUseIDs, wireToolUseIDs(t, body),
						"%s path must replay every named tool_use", path)
				}
			}
		})
	}
}
