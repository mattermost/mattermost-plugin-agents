// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/useragents"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserAgentToBotConfig(t *testing.T) {
	agent := &useragents.UserAgent{
		ID:                 "agent-id-123",
		Username:           "my-agent",
		DisplayName:        "My Agent",
		CustomInstructions: "Be helpful",
		ServiceID:          "svc-1",
		ChannelAccessLevel: 1, // Allow
		ChannelIDs:         []string{"ch-1", "ch-2"},
		UserAccessLevel:    0, // All
		UserIDs:            nil,
		TeamIDs:            []string{"team-1"},
	}

	cfg := userAgentToBotConfig(agent)

	assert.Equal(t, "agent-id-123", cfg.ID)
	assert.Equal(t, "my-agent", cfg.Name)
	assert.Equal(t, "My Agent", cfg.DisplayName)
	assert.Equal(t, "Be helpful", cfg.CustomInstructions)
	assert.Equal(t, "svc-1", cfg.ServiceID)
	assert.Equal(t, llm.ChannelAccessLevelAllow, cfg.ChannelAccessLevel)
	assert.Equal(t, []string{"ch-1", "ch-2"}, cfg.ChannelIDs)
	assert.Equal(t, llm.UserAccessLevelAll, cfg.UserAccessLevel)
	assert.Nil(t, cfg.UserIDs)
	assert.Equal(t, []string{"team-1"}, cfg.TeamIDs)

	// Verify defaults for fields not in UserAgent
	assert.Equal(t, "", cfg.Model)
	assert.False(t, cfg.DisableTools)
	assert.False(t, cfg.EnableVision)
	assert.Nil(t, cfg.EnabledNativeTools)
}

func TestUserAgentToBotConfigIsValid(t *testing.T) {
	agent := &useragents.UserAgent{
		ID:          "agent-id",
		Username:    "valid-agent",
		DisplayName: "Valid Agent",
		ServiceID:   "svc-1",
	}

	cfg := userAgentToBotConfig(agent)
	assert.True(t, cfg.IsValid(), "converted BotConfig should pass IsValid()")
}

func TestDBBackedAgentInBotRegistry(t *testing.T) {
	// Verify that a Bot built from a DB-backed agent's BotConfig
	// is findable by all lookup methods.
	agent := &useragents.UserAgent{
		ID:          "agent-id",
		Username:    "db-agent",
		DisplayName: "DB Agent",
		ServiceID:   "svc-1",
	}
	cfg := userAgentToBotConfig(agent)

	mmBot := &model.Bot{
		UserId:      "bot-user-id-db-agent",
		Username:    "db-agent",
		DisplayName: "DB Agent",
	}

	bot := NewBot(cfg, llm.ServiceConfig{ID: "svc-1", Type: "openai"}, mmBot, nil)

	// Simulate adding to the registry
	bots := &MMBots{}
	bots.SetBotsForTesting([]*Bot{bot})

	// Lookup by username
	found := bots.GetBotByUsername("db-agent")
	require.NotNil(t, found)
	assert.Equal(t, "db-agent", found.GetConfig().Name)

	// Lookup by user ID
	found = bots.GetBotByID("bot-user-id-db-agent")
	require.NotNil(t, found)
	assert.Equal(t, "DB Agent", found.GetMMBot().DisplayName)

	// IsAnyBot check
	assert.True(t, bots.IsAnyBot("bot-user-id-db-agent"))
	assert.False(t, bots.IsAnyBot("some-other-id"))

	// GetAllBots includes it
	all := bots.GetAllBots()
	require.Len(t, all, 1)
	assert.Equal(t, "db-agent", all[0].GetConfig().Name)

	// GetBotMentioned (text containing @db-agent)
	mentioned := bots.GetBotMentioned("Hey @db-agent can you help?")
	require.NotNil(t, mentioned)
	assert.Equal(t, "db-agent", mentioned.GetMMBot().Username)
}

func TestForceRefreshFlag(t *testing.T) {
	bots := &MMBots{}

	// Initially false
	assert.False(t, bots.forceRefresh)

	// Set via public method
	bots.ForceRefreshOnNextEnsure()
	assert.True(t, bots.forceRefresh)

	// Reset manually (simulating what EnsureBots does)
	bots.botsLock.Lock()
	bots.forceRefresh = false
	bots.botsLock.Unlock()
	assert.False(t, bots.forceRefresh)
}
