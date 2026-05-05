// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupLoadedMCPToolStore(t *testing.T) *Store {
	t.Helper()

	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())
	return s
}

func TestLoadedMCPToolUpsertAndList(t *testing.T) {
	s := setupLoadedMCPToolStore(t)

	require.NoError(t, s.UpsertLoadedMCPTool(LoadedMCPTool{
		ConversationID: "conv-1",
		BotID:          "bot-1",
		UserID:         "user-1",
		ToolName:       "jira__get_issue",
		ServerOrigin:   "https://jira.example.com",
		BareName:       "get_issue",
		CreatedAt:      100,
		UpdatedAt:      100,
	}))
	require.NoError(t, s.UpsertLoadedMCPTool(LoadedMCPTool{
		ConversationID: "conv-1",
		BotID:          "bot-1",
		UserID:         "user-1",
		ToolName:       "github__search",
		ServerOrigin:   "https://github.example.com",
		BareName:       "search",
		CreatedAt:      101,
		UpdatedAt:      101,
	}))

	tools, err := s.ListLoadedMCPTools("conv-1", "bot-1", "user-1")
	require.NoError(t, err)
	require.Len(t, tools, 2)
	assert.Equal(t, "github__search", tools[0].ToolName)
	assert.Equal(t, "jira__get_issue", tools[1].ToolName)
	assert.Equal(t, "https://jira.example.com", tools[1].ServerOrigin)
	assert.Equal(t, "get_issue", tools[1].BareName)
}

func TestLoadedMCPToolUpsertUpdatesMetadataPreservesCreatedAt(t *testing.T) {
	s := setupLoadedMCPToolStore(t)

	require.NoError(t, s.UpsertLoadedMCPTool(LoadedMCPTool{
		ConversationID: "conv-1",
		BotID:          "bot-1",
		UserID:         "user-1",
		ToolName:       "jira__get_issue",
		ServerOrigin:   "https://old.example.com",
		BareName:       "old_name",
		CreatedAt:      100,
		UpdatedAt:      100,
	}))
	require.NoError(t, s.UpsertLoadedMCPTool(LoadedMCPTool{
		ConversationID: "conv-1",
		BotID:          "bot-1",
		UserID:         "user-1",
		ToolName:       "jira__get_issue",
		ServerOrigin:   "https://jira.example.com",
		BareName:       "get_issue",
		CreatedAt:      200,
		UpdatedAt:      250,
	}))

	tools, err := s.ListLoadedMCPTools("conv-1", "bot-1", "user-1")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, int64(100), tools[0].CreatedAt)
	assert.Equal(t, int64(250), tools[0].UpdatedAt)
	assert.Equal(t, "https://jira.example.com", tools[0].ServerOrigin)
	assert.Equal(t, "get_issue", tools[0].BareName)
}

func TestLoadedMCPToolListScopesByConversationBotUser(t *testing.T) {
	s := setupLoadedMCPToolStore(t)

	rows := []LoadedMCPTool{
		{ConversationID: "conv-1", BotID: "bot-1", UserID: "user-1", ToolName: "jira__get_issue", CreatedAt: 1, UpdatedAt: 1},
		{ConversationID: "conv-2", BotID: "bot-1", UserID: "user-1", ToolName: "github__search", CreatedAt: 1, UpdatedAt: 1},
		{ConversationID: "conv-1", BotID: "bot-2", UserID: "user-1", ToolName: "slack__post", CreatedAt: 1, UpdatedAt: 1},
		{ConversationID: "conv-1", BotID: "bot-1", UserID: "user-2", ToolName: "mattermost__search", CreatedAt: 1, UpdatedAt: 1},
	}
	for _, row := range rows {
		require.NoError(t, s.UpsertLoadedMCPTool(row))
	}

	tools, err := s.ListLoadedMCPTools("conv-1", "bot-1", "user-1")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "jira__get_issue", tools[0].ToolName)
}

func TestLoadedMCPToolDeleteMissingNoop(t *testing.T) {
	s := setupLoadedMCPToolStore(t)

	require.NoError(t, s.DeleteLoadedMCPTool("conv-missing", "bot-1", "user-1", "jira__get_issue"))
}

func TestLoadedMCPToolDeleteForConversation(t *testing.T) {
	s := setupLoadedMCPToolStore(t)

	for _, row := range []LoadedMCPTool{
		{ConversationID: "conv-1", BotID: "bot-1", UserID: "user-1", ToolName: "jira__get_issue", CreatedAt: 1, UpdatedAt: 1},
		{ConversationID: "conv-1", BotID: "bot-1", UserID: "user-1", ToolName: "github__search", CreatedAt: 1, UpdatedAt: 1},
		{ConversationID: "conv-2", BotID: "bot-1", UserID: "user-1", ToolName: "slack__post", CreatedAt: 1, UpdatedAt: 1},
	} {
		require.NoError(t, s.UpsertLoadedMCPTool(row))
	}

	require.NoError(t, s.DeleteLoadedMCPToolsForConversation("conv-1"))

	tools, err := s.ListLoadedMCPTools("conv-1", "bot-1", "user-1")
	require.NoError(t, err)
	assert.Empty(t, tools)

	tools, err = s.ListLoadedMCPTools("conv-2", "bot-1", "user-1")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "slack__post", tools[0].ToolName)
}

func TestLoadedMCPToolUpsertValidatesRequiredFields(t *testing.T) {
	s := setupLoadedMCPToolStore(t)

	valid := LoadedMCPTool{
		ConversationID: "conv-1",
		BotID:          "bot-1",
		UserID:         "user-1",
		ToolName:       "jira__get_issue",
		CreatedAt:      1,
		UpdatedAt:      1,
	}

	for name, mutate := range map[string]func(*LoadedMCPTool){
		"conversation id": func(tool *LoadedMCPTool) { tool.ConversationID = "" },
		"bot id":          func(tool *LoadedMCPTool) { tool.BotID = "" },
		"user id":         func(tool *LoadedMCPTool) { tool.UserID = "" },
		"tool name":       func(tool *LoadedMCPTool) { tool.ToolName = "" },
	} {
		t.Run(name, func(t *testing.T) {
			tool := valid
			mutate(&tool)
			require.Error(t, s.UpsertLoadedMCPTool(tool))
		})
	}
}
