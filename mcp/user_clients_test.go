// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/stretchr/testify/require"
)

func TestPrepareToolCallMetadata_EmbeddedOnly(t *testing.T) {
	scope := &llm.MattermostAccessScope{
		TeamID:            "team-id",
		AllowedChannelIDs: []string{"allowed-channel"},
	}

	llmContext := llm.NewContext()
	llmContext.BotUserID = "bot-user-id"
	llmContext.SetMCPServerMetadata(EmbeddedClientKey, map[string]any{
		llm.MattermostAccessScopeMetadataKey: scope.ToMetadataValue(),
	})

	clients := &UserClients{}

	embeddedMetadata := clients.prepareToolCallMetadata(&Client{
		config: ServerConfig{Name: EmbeddedClientKey},
	}, llmContext, "search_posts")
	require.NotNil(t, embeddedMetadata)
	require.Equal(t, "bot-user-id", embeddedMetadata["bot_user_id"])
	embeddedScope := llm.MattermostAccessScopeFromMetadata(embeddedMetadata)
	require.NotNil(t, embeddedScope)
	require.Equal(t, scope.TeamID, embeddedScope.TeamID)
	require.Equal(t, scope.AllowedChannelIDs, embeddedScope.AllowedChannelIDs)

	remoteMetadata := clients.prepareToolCallMetadata(&Client{
		config: ServerConfig{Name: "remote-server"},
	}, llmContext, "search_posts")
	require.Nil(t, remoteMetadata)
}
