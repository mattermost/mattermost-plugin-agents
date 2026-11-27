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

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
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

type wireTestPostClient struct {
	mmapi.Client
	posts map[string]*model.Post
}

func (c *wireTestPostClient) GetPost(postID string) (*model.Post, error) {
	if p, ok := c.posts[postID]; ok {
		return p.Clone(), nil
	}
	return nil, pluginapi.ErrNotFound
}

func TestReconciledAnchoredTurnsProduceValidProviderRequests(t *testing.T) {
	const (
		chanID = "chan-wire"
		u1ID   = "u1"
		b1ID   = "b1"
		u2ID   = "u2"
		b2ID   = "b2"
		u3ID   = "u3"
	)

	marshal := func(blocks []conversation.ContentBlock) json.RawMessage {
		raw, err := json.Marshal(blocks)
		require.NoError(t, err)
		return raw
	}

	ch := chanID
	root := u1ID
	conv := &store.Conversation{
		ID:           "conv-wire",
		UserID:       "user-1",
		BotID:        "bot-1",
		ChannelID:    &ch,
		RootPostID:   &root,
		SystemPrompt: "You are helpful.",
		Operation:    "conversation",
	}

	baseTurns := []store.Turn{
		{ID: "t1", ConversationID: conv.ID, PostID: new(u1ID), Role: "user", Sequence: 1, Content: marshal([]conversation.ContentBlock{
			{Type: conversation.BlockTypeText, Text: "initial question"},
		})},
		{ID: "t2", ConversationID: conv.ID, PostID: new(b1ID), Role: "assistant", Sequence: 2, Content: marshal([]conversation.ContentBlock{
			{Type: conversation.BlockTypeText, Text: "checking tools"},
			{Type: conversation.BlockTypeToolUse, ID: "call_1", Name: "search", Input: json.RawMessage(`{"q":"x"}`), Status: conversation.StatusSuccess, Shared: new(true)},
		})},
		{ID: "t3", ConversationID: conv.ID, Role: "tool_result", Sequence: 3, Content: marshal([]conversation.ContentBlock{
			{Type: conversation.BlockTypeToolResult, ToolUseID: "call_1", Content: "found", Status: conversation.StatusSuccess, Shared: new(true)},
		})},
		{ID: "t4", ConversationID: conv.ID, PostID: new(u2ID), Role: "user", Sequence: 4, Content: marshal([]conversation.ContentBlock{
			{Type: conversation.BlockTypeText, Text: "second question"},
		})},
		{ID: "t5", ConversationID: conv.ID, PostID: new(b2ID), Role: "assistant", Sequence: 5, Content: marshal([]conversation.ContentBlock{
			{Type: conversation.BlockTypeText, Text: "second answer"},
		})},
		{ID: "t6", ConversationID: conv.ID, PostID: new(u3ID), Role: "user", Sequence: 6, Content: marshal([]conversation.ContentBlock{
			{Type: conversation.BlockTypeText, Text: "third question"},
		})},
	}

	tests := []struct {
		name  string
		posts map[string]*model.Post
	}{
		{
			name: "leading user post deleted and tool-bearing assistant post deleted",
			posts: map[string]*model.Post{
				u1ID: {Id: u1ID, ChannelId: chanID, DeleteAt: 100, Message: "initial question"},
				b1ID: {Id: b1ID, ChannelId: chanID, DeleteAt: 200, Message: "checking tools"},
				u2ID: {Id: u2ID, ChannelId: chanID, Message: "second question"},
				b2ID: {Id: b2ID, ChannelId: chanID, Message: "second answer"},
				u3ID: {Id: u3ID, ChannelId: chanID, Message: "third question"},
			},
		},
		{
			name: "mid-thread user and assistant posts edited to empty",
			posts: map[string]*model.Post{
				u1ID: {Id: u1ID, ChannelId: chanID, Message: "initial question"},
				b1ID: {Id: b1ID, ChannelId: chanID, EditAt: 100, Message: ""},
				u2ID: {Id: u2ID, ChannelId: chanID, EditAt: 100, Message: "   "},
				b2ID: {Id: b2ID, ChannelId: chanID, EditAt: 100, Message: ""},
				u3ID: {Id: u3ID, ChannelId: chanID, Message: "third question"},
			},
		},
		{
			name: "trailing user post edited to empty and mid-thread assistant post deleted",
			posts: map[string]*model.Post{
				u1ID: {Id: u1ID, ChannelId: chanID, Message: "initial question"},
				b1ID: {Id: b1ID, ChannelId: chanID, Message: "checking tools"},
				u2ID: {Id: u2ID, ChannelId: chanID, Message: "second question"},
				b2ID: {Id: b2ID, ChannelId: chanID, DeleteAt: 500, Message: "second answer"},
				u3ID: {Id: u3ID, ChannelId: chanID, Message: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := conversation.AssembleRequest(conv, baseTurns, &llm.Context{}, &wireTestPostClient{posts: tt.posts}, false, 0)
			require.NoError(t, err)

			for path, body := range anthropicWireRequests(t, req.Posts) {
				assert.Empty(t, llmtest.AnthropicRequestViolations(body),
					"anthropic %s rejected reconciled request: %s", path, body)
				assert.Equal(t, []string{"call_1"}, wireToolUseIDs(t, body),
					"anthropic %s must keep tool_use paired", path)
			}

			for chain, body := range bedrockWireRequests(t, req.Posts) {
				var parsed map[string]any
				require.NoError(t, json.Unmarshal(body, &parsed))
				assert.Empty(t, adjacentSameRoleMessages(parsed),
					"bedrock %s must not repeat roles: %s", chain, body)
			}
		})
	}
}
