// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/providers/bedrock"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
)

const bedrockClaudeModel = "anthropic.claude-sonnet-4-6"

// bedrockWireRequests builds posts on the chat path for chains that reach a
// Bedrock hop and converts the Bedrock hop's request with Bifrost's Bedrock
// converter, returning the Converse body that hop would receive keyed by chain
// name. A Bedrock fallback's request is the one the hook rebuilds for it.
func bedrockWireRequests(t *testing.T, posts []llm.Post) map[string][]byte {
	t.Helper()

	chains := []struct {
		name         string
		b            *LLM
		primaryModel string
		// bedrockFallback is whether Bedrock is reached as a fallback.
		bedrockFallback bool
	}{
		{name: "Bedrock primary", b: &LLM{provider: schemas.Bedrock}, primaryModel: bedrockClaudeModel},
		{
			name: "OpenAI-compatible chat primary with a Bedrock fallback",
			b: &LLM{
				provider:  schemas.OpenAI,
				fallbacks: testHops([]schemas.Fallback{{Provider: schemas.Bedrock, Model: bedrockClaudeModel}}, nil),
			},
			primaryModel:    "gpt-4o",
			bedrockFallback: true,
		},
	}

	ctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()

	request := llm.CompletionRequest{Posts: posts}
	bodies := make(map[string][]byte, len(chains))
	for _, chain := range chains {
		cfg := llm.LanguageModelConfig{Model: chain.primaryModel, MaxGeneratedTokens: 1024}
		bedrockReq := chain.b.convertToBifrostRequest(request, cfg)
		if chain.bedrockFallback {
			hop := *bedrockReq
			hop.Provider, hop.Model = schemas.Bedrock, bedrockClaudeModel
			hopReq := &schemas.BifrostRequest{ChatRequest: &hop}
			require.NoError(t, chain.b.applyHopRequest(hopReq, request, cfg))
			bedrockReq = hopReq.ChatRequest
		}

		wire, err := bedrock.ToBedrockChatCompletionRequest(ctx, bedrockReq)
		require.NoError(t, err)
		body, err := json.Marshal(wire)
		require.NoError(t, err)
		bodies[chain.name] = body
	}
	return bodies
}

func TestBedrockChatRequestsAreValidConverse(t *testing.T) {
	toolRound := llm.Post{Role: llm.PostRoleBot, Message: "checking", ToolUse: []llm.ToolCall{
		{ID: "call1", Name: "search", Arguments: json.RawMessage(`{"q":"x"}`), Result: "found"},
	}}

	tests := []struct {
		name  string
		posts []llm.Post
	}{
		{
			name:  "normal history",
			posts: []llm.Post{{Role: llm.PostRoleBot, Message: "earlier answer"}},
		},
		{
			name: "unsigned reasoning on a bot post",
			posts: []llm.Post{
				{Role: llm.PostRoleBot, Message: "earlier answer", Reasoning: "partial thinking captured before the stream failed"},
			},
		},
		{
			name:  "tool round followed by a user post",
			posts: []llm.Post{toolRound},
		},
		{
			name:  "tool round without text followed by a user post",
			posts: []llm.Post{{Role: llm.PostRoleBot, ToolUse: toolRound.ToolUse}},
		},
		{
			name:  "blank final answer after a tool round",
			posts: []llm.Post{toolRound, {Role: llm.PostRoleBot, Message: " \n"}},
		},
		{
			name:  "tool round answered before the next user post",
			posts: []llm.Post{toolRound, {Role: llm.PostRoleBot, Message: "Here is what I found."}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			posts := append([]llm.Post{
				{Role: llm.PostRoleSystem, Message: "You are helpful."},
				{Role: llm.PostRoleUser, Message: "first question"},
			}, tt.posts...)
			posts = append(posts, llm.Post{Role: llm.PostRoleUser, Message: "next question"})

			for chain, body := range bedrockWireRequests(t, posts) {
				assert.Empty(t, llmtest.BedrockConverseViolations(body),
					"%s would be rejected by Bedrock Converse: %s", chain, body)
			}
		})
	}
}

func TestBedrockChatRequestKeepsTheLiveToolRound(t *testing.T) {
	posts := []llm.Post{
		{Role: llm.PostRoleUser, Message: "first question"},
		{Role: llm.PostRoleBot, ToolUse: []llm.ToolCall{
			{ID: "call1", Name: "search", Arguments: json.RawMessage(`{"q":"x"}`), Result: "found"},
		}},
	}

	for chain, body := range bedrockWireRequests(t, posts) {
		var req struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(body, &req))
		require.NotEmpty(t, req.Messages, chain)
		assert.Equal(t, "user", req.Messages[len(req.Messages)-1].Role,
			"%s must end on the tool results the model continues from: %s", chain, body)
		assert.NotContains(t, string(body), missingAnswerPlaceholder, chain)
	}
}
