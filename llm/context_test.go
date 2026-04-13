// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContext_SetBotFields(t *testing.T) {
	c := NewContext()
	c.SetBotFields("BotDisplay", "botuser", "user-id-123", "gpt-4", "openai", "Be helpful and concise")

	assert.Equal(t, "BotDisplay", c.BotName)
	assert.Equal(t, "botuser", c.BotUsername)
	assert.Equal(t, "user-id-123", c.BotUserID)
	assert.Equal(t, "gpt-4", c.BotModel)
	assert.Equal(t, "openai", c.BotServiceType)
	assert.Equal(t, "Be helpful and concise", c.CustomInstructions)
}

func TestContext_MCPServerMetadata(t *testing.T) {
	c := NewContext()
	metadata := map[string]any{
		"mattermost_access_scope": map[string]any{
			"team_id": "team-id",
		},
	}

	c.SetMCPServerMetadata("embedded://mattermost", metadata)

	got := c.GetMCPServerMetadata("embedded://mattermost")
	require.NotNil(t, got)
	require.Equal(t, metadata, got)

	got["new_key"] = "new-value"
	require.Nil(t, c.GetMCPServerMetadata("embedded://mattermost")["new_key"])

	c.SetMCPServerMetadata("embedded://mattermost", nil)
	assert.Nil(t, c.GetMCPServerMetadata("embedded://mattermost"))
}
