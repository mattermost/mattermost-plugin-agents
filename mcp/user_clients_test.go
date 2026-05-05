// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/stretchr/testify/require"
)

func TestPrepareToolCallMetadata_EmbeddedMergesServerMetadataAndBotUserID(t *testing.T) {
	llmContext := llm.NewContext()
	llmContext.BotUserID = "bot-user-id"
	llmContext.SetMCPServerMetadata(EmbeddedClientKey, map[string]any{
		"tool_hooks": map[string]any{
			"search_posts": map[string]any{
				"before_hook_key": "beforeHook:user-1:secret",
			},
		},
	})

	clients := &UserClients{}

	embeddedMeta := clients.prepareToolCallMetadata(&Client{
		config: ServerConfig{Name: EmbeddedClientKey},
	}, llmContext)
	require.NotNil(t, embeddedMeta)
	require.Equal(t, "bot-user-id", embeddedMeta["bot_user_id"])
	hooks, ok := embeddedMeta["tool_hooks"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, hooks, "search_posts")
	require.NotContains(t, embeddedMeta, "hook_plugin_id")

	remoteMeta := clients.prepareToolCallMetadata(&Client{
		config: ServerConfig{Name: "remote-server"},
	}, llmContext)
	require.Nil(t, remoteMeta)
}
