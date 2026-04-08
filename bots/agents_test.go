// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/useragents"
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

	// Bot-level fields map from UserAgent when set
	assert.Equal(t, "", cfg.Model)
	assert.False(t, cfg.DisableTools)
	assert.False(t, cfg.EnableVision)
	assert.Nil(t, cfg.EnabledNativeTools)
	assert.False(t, cfg.ReasoningEnabled)
	assert.Equal(t, "", cfg.ReasoningEffort)
	assert.Equal(t, 0, cfg.ThinkingBudget)
	assert.False(t, cfg.StructuredOutputEnabled)
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

func TestConvertEnabledTools(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		result := convertEnabledTools(nil)
		assert.Nil(t, result)
	})

	t.Run("empty input returns empty non-nil", func(t *testing.T) {
		result := convertEnabledTools([]useragents.EnabledTool{})
		require.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("populated input converts correctly", func(t *testing.T) {
		input := []useragents.EnabledTool{
			{ServerOrigin: "https://server.com", ToolName: "tool_a"},
			{ServerOrigin: "https://other.com", ToolName: "tool_b"},
		}
		result := convertEnabledTools(input)
		require.Len(t, result, 2)
		assert.Equal(t, "https://server.com", result[0].ServerOrigin)
		assert.Equal(t, "tool_a", result[0].ToolName)
		assert.Equal(t, "https://other.com", result[1].ServerOrigin)
		assert.Equal(t, "tool_b", result[1].ToolName)
	})
}

func TestUserAgentToBotConfigEnabledTools(t *testing.T) {
	agent := &useragents.UserAgent{
		ID:          "agent-123",
		Username:    "test-agent",
		DisplayName: "Test Agent",
		ServiceID:   "svc-1",
		EnabledTools: []useragents.EnabledTool{
			{ServerOrigin: "https://mcp.example.com", ToolName: "search"},
		},
	}

	cfg := userAgentToBotConfig(agent)

	require.NotNil(t, cfg.EnabledMCPTools)
	require.Len(t, cfg.EnabledMCPTools, 1)
	assert.Equal(t, "https://mcp.example.com", cfg.EnabledMCPTools[0].ServerOrigin)
	assert.Equal(t, "search", cfg.EnabledMCPTools[0].ToolName)
}

func TestUserAgentToBotConfigNilEnabledTools(t *testing.T) {
	agent := &useragents.UserAgent{
		ID:           "agent-456",
		Username:     "no-tools-agent",
		DisplayName:  "No Tools",
		ServiceID:    "svc-1",
		EnabledTools: nil,
	}

	cfg := userAgentToBotConfig(agent)
	assert.Nil(t, cfg.EnabledMCPTools)
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
