// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyBotAsUserAgent(t *testing.T) {
	allowlist := []llm.EnabledMCPTool{{ServerOrigin: "embedded://mattermost", ToolName: "read_channel"}}
	base := llm.BotConfig{ID: "legacy", Name: "legacybot", DisplayName: "Legacy", ServiceID: "svc", CreatorID: "someone", CreateAt: 1}

	tests := []struct {
		name   string
		config func(*llm.BotConfig)
	}{
		{name: "plain config bot"},
		{name: "config bot with an allowlist", config: func(c *llm.BotConfig) { c.EnabledMCPTools = allowlist }},
		{
			name: "config bot in scoped channel mode",
			config: func(c *llm.BotConfig) {
				c.UseServiceAccountAuth = true
				c.ScopedChannelMode = true
				c.ScopedExtraChannelIDs = []string{model.NewId()}
				c.EnabledMCPTools = allowlist
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bc := base
			if tt.config != nil {
				tt.config(&bc)
			}
			botUserID := model.NewId()

			ua := legacyBotAsUserAgent(bc, botUserID)

			require.NoError(t, ua.Validate(), "a migrated agent must stay active")
			assert.Empty(t, ua.ID)
			assert.Empty(t, ua.CreatorID)
			assert.Equal(t, botUserID, ua.BotUserID)
			assert.True(t, ua.AutoEnableNewMCPTools)
			assert.False(t, ua.ScopedChannelMode)
			assert.Empty(t, ua.ScopedExtraChannelIDs)
			assert.Equal(t, bc.UseServiceAccountAuth, ua.UseServiceAccountAuth)
		})
	}
}
