// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDBBackedAgentInBotRegistry(t *testing.T) {
	// Verify that a Bot built from a DB-backed agent's BotConfig
	// is findable by all lookup methods.
	cfg := llm.BotConfig{
		ID:          "agent-id",
		Name:        "db-agent",
		DisplayName: "DB Agent",
		ServiceID:   "svc-1",
		BotUserID:   "bot-user-id-db-agent",
	}

	mmBot := &model.Bot{
		UserId:      "bot-user-id-db-agent",
		Username:    "db-agent",
		DisplayName: "DB Agent",
	}

	bot := NewBot(cfg, llm.ServiceConfig{ID: "svc-1", Type: "openai"}, mmBot, nil)
	bots := &MMBots{}
	bots.SetBotsForTesting([]*Bot{bot})

	found := bots.GetBotByUsername("db-agent")
	require.NotNil(t, found)
	assert.Equal(t, "db-agent", found.GetConfig().Name)

	found = bots.GetBotByID("bot-user-id-db-agent")
	require.NotNil(t, found)
	assert.Equal(t, "DB Agent", found.GetMMBot().DisplayName)

	assert.True(t, bots.IsAnyBot("bot-user-id-db-agent"))
	assert.False(t, bots.IsAnyBot("some-other-id"))

	all := bots.GetAllBots()
	require.Len(t, all, 1)
	assert.Equal(t, "db-agent", all[0].GetConfig().Name)

	mentioned := bots.GetBotMentioned("Hey @db-agent can you help?")
	require.NotNil(t, mentioned)
	assert.Equal(t, "db-agent", mentioned.GetMMBot().Username)
}
