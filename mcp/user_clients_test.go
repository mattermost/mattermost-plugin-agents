// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/stretchr/testify/require"
)

func TestPrepareToolCallMetadata_EmbeddedMergesCallMetadataAndBotUserID(t *testing.T) {
	llmContext := llm.NewContext()
	llmContext.BotUserID = "bot-user-id"
	llmContext.Tools = llm.NewToolStore()
	llmContext.Tools.AddTools([]llm.Tool{
		llm.Tool{Name: "search_posts"}.WithCallMetadata(map[string]any{
			"tool_hooks": map[string]any{
				"search_posts": map[string]any{
					"before_hook_key": "beforeHook:user-1:secret",
				},
			},
		}),
		{Name: "no_hooks"},
	})

	clients := &UserClients{}
	embeddedClient := &Client{config: ServerConfig{Name: EmbeddedClientKey}}
	remoteClient := &Client{config: ServerConfig{Name: "remote-server"}}

	embeddedMeta := clients.prepareToolCallMetadata(embeddedClient, "search_posts", llmContext)
	require.NotNil(t, embeddedMeta)
	require.Equal(t, "bot-user-id", embeddedMeta["bot_user_id"])
	hooks, ok := embeddedMeta["tool_hooks"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, hooks, "search_posts")

	noHookMeta := clients.prepareToolCallMetadata(embeddedClient, "no_hooks", llmContext)
	require.Equal(t, map[string]any{"bot_user_id": "bot-user-id"}, noHookMeta)

	remoteMeta := clients.prepareToolCallMetadata(remoteClient, "search_posts", llmContext)
	require.Nil(t, remoteMeta)
}
