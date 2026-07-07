// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testAgent returns a fully-populated BotConfig for testing.
// ID, CreateAt, UpdateAt, and DeleteAt are set by the store on create.
func testAgent(creatorID, username, displayName string) *llm.BotConfig {
	return &llm.BotConfig{
		BotUserID:          "bot-user-id-" + username,
		CreatorID:          creatorID,
		DisplayName:        displayName,
		Name:               username,
		ServiceID:          "svc-1",
		CustomInstructions: "Be helpful and concise",
		ChannelAccessLevel: llm.ChannelAccessLevelAllow,
		ChannelIDs:         []string{"ch-1", "ch-2"},
		UserAccessLevel:    llm.UserAccessLevelAll,
		UserIDs:            nil,
		TeamIDs:            []string{"team-1"},
		AdminUserIDs:       []string{"admin-1", "admin-2"},
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: "https://mcp.example.com", ToolName: "web_search"},
			{ServerOrigin: "https://mcp.example.com", ToolName: "file_search"},
		},
		AutoEnableNewMCPTools:   true,
		MCPDynamicToolLoading:   true,
		Model:                   "gpt-4",
		EnableVision:            true,
		DisableTools:            false,
		EnabledNativeTools:      []string{"web_search"},
		ReasoningEnabled:        true,
		ReasoningEffort:         "medium",
		ThinkingBudget:          10000,
		StructuredOutputEnabled: true,
		MaxToolTurns:            42,
	}
}

func TestAgentCreateAndGet(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "my-agent", "My Agent")
	err = s.CreateAgent(agent)
	require.NoError(t, err)

	// ID should be populated (26 chars)
	assert.Len(t, agent.ID, 26)
	assert.NotZero(t, agent.CreateAt)
	assert.NotZero(t, agent.UpdateAt)
	assert.Equal(t, agent.CreateAt, agent.UpdateAt)
	assert.Zero(t, agent.DeleteAt)

	// Get round-trip
	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)

	// Scalar fields
	assert.Equal(t, agent.ID, fetched.ID)
	assert.Equal(t, agent.BotUserID, fetched.BotUserID)
	assert.Equal(t, agent.CreatorID, fetched.CreatorID)
	assert.Equal(t, agent.DisplayName, fetched.DisplayName)
	assert.Equal(t, agent.Name, fetched.Name)
	assert.Equal(t, agent.ServiceID, fetched.ServiceID)
	assert.Equal(t, agent.CustomInstructions, fetched.CustomInstructions)
	assert.Equal(t, llm.ChannelAccessLevelAllow, fetched.ChannelAccessLevel)
	assert.Equal(t, llm.UserAccessLevelAll, fetched.UserAccessLevel)
	assert.Equal(t, agent.CreateAt, fetched.CreateAt)
	assert.Equal(t, agent.UpdateAt, fetched.UpdateAt)
	assert.Equal(t, agent.DeleteAt, fetched.DeleteAt)

	// JSON slice fields — the critical round-trip test
	assert.Equal(t, []string{"ch-1", "ch-2"}, fetched.ChannelIDs)
	assert.Nil(t, fetched.UserIDs) // nil slice round-trips as nil
	assert.Equal(t, []string{"team-1"}, fetched.TeamIDs)
	assert.Equal(t, []string{"admin-1", "admin-2"}, fetched.AdminUserIDs)
	require.Len(t, fetched.EnabledMCPTools, 2)
	assert.Equal(t, "web_search", fetched.EnabledMCPTools[0].ToolName)
	assert.Equal(t, "https://mcp.example.com", fetched.EnabledMCPTools[0].ServerOrigin)
	assert.Equal(t, "file_search", fetched.EnabledMCPTools[1].ToolName)
	assert.True(t, fetched.AutoEnableNewMCPTools)
	assert.True(t, fetched.MCPDynamicToolLoading)

	assert.Equal(t, "gpt-4", fetched.Model)
	assert.True(t, fetched.EnableVision)
	assert.False(t, fetched.DisableTools)
	assert.Equal(t, []string{"web_search"}, fetched.EnabledNativeTools)
	assert.True(t, fetched.ReasoningEnabled)
	assert.Equal(t, "medium", fetched.ReasoningEffort)
	assert.Equal(t, 10000, fetched.ThinkingBudget)
	assert.True(t, fetched.StructuredOutputEnabled)
	assert.Equal(t, 42, fetched.MaxToolTurns)
}

// TestAgentMaxToolTurnsDefaultsToThirty verifies that the SQL DEFAULT 30 supplied
// by migration 000008 lets agents inserted before/around the migration come back
// with 30 even if the caller never set the column explicitly.
func TestAgentMaxToolTurnsDefaultsToThirty(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	// Insert directly without specifying MaxToolTurns to exercise the column default.
	now := int64(1_700_000_000_000)
	id := "abcdefghijklmnopqrstuvwxyz"
	_, err = s.db.Exec(`INSERT INTO Agents_UserAgents
		(ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
		 CustomInstructions, ChannelAccessLevel, ChannelIDs,
		 UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
		 EnabledTools, AutoEnableNewMCPTools,
		 CreateAt, UpdateAt, DeleteAt)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		id, "bot-user-id-default", "creator-1", "Default Agent", "default-agent", "svc-1",
		"", 0, "[]",
		0, "[]", "[]", "[]",
		"[]", false,
		now, now, 0,
	)
	require.NoError(t, err)

	fetched, err := s.GetAgent(id)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Equal(t, 30, fetched.MaxToolTurns, "column default should populate MaxToolTurns at 30")
}

func TestAgentGetNonexistent(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	fetched, err := s.GetAgent("nonexistent-id")
	require.NoError(t, err)
	assert.Nil(t, fetched)
}

func TestAgentListReturnsOnlyActive(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	// Create 3 agents
	a1 := testAgent("creator-1", "agent-1", "Agent 1")
	a2 := testAgent("creator-1", "agent-2", "Agent 2")
	a3 := testAgent("creator-2", "agent-3", "Agent 3")
	require.NoError(t, s.CreateAgent(a1))
	require.NoError(t, s.CreateAgent(a2))
	require.NoError(t, s.CreateAgent(a3))

	// Delete one
	require.NoError(t, s.DeleteAgent(a2.ID))

	// List should return only 2
	agents, err := s.ListAgents()
	require.NoError(t, err)
	assert.Len(t, agents, 2)

	// Ordered by CreateAt DESC (tiebreak: order is undefined if CreateAt matches)
	ids := map[string]struct{}{agents[0].ID: {}, agents[1].ID: {}}
	_, okA1 := ids[a1.ID]
	_, okA3 := ids[a3.ID]
	assert.True(t, okA1 && okA3, "list should contain the two active agents")
	assert.GreaterOrEqual(t, agents[0].CreateAt, agents[1].CreateAt)
}

func TestAgentListByCreator(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	a1 := testAgent("creator-1", "agent-1", "Agent 1")
	a2 := testAgent("creator-1", "agent-2", "Agent 2")
	a3 := testAgent("creator-2", "agent-3", "Agent 3")
	require.NoError(t, s.CreateAgent(a1))
	require.NoError(t, s.CreateAgent(a2))
	require.NoError(t, s.CreateAgent(a3))

	// List by creator-1
	agents, err := s.ListAgentsByCreator("creator-1")
	require.NoError(t, err)
	assert.Len(t, agents, 2)
	for _, a := range agents {
		assert.Equal(t, "creator-1", a.CreatorID)
	}

	// List by creator-2
	agents, err = s.ListAgentsByCreator("creator-2")
	require.NoError(t, err)
	assert.Len(t, agents, 1)
	assert.Equal(t, a3.ID, agents[0].ID)

	// List by nonexistent creator
	agents, err = s.ListAgentsByCreator("creator-999")
	require.NoError(t, err)
	assert.Empty(t, agents)
}

func TestAgentUpdate(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "agent-1", "Agent 1")
	require.NoError(t, s.CreateAgent(agent))
	originalUpdateAt := agent.UpdateAt

	// Modify fields
	agent.DisplayName = "Updated Agent"
	agent.CustomInstructions = "New instructions"
	agent.ChannelIDs = []string{"ch-3"}
	agent.EnabledMCPTools = nil
	agent.ServiceID = "svc-2"

	require.NoError(t, s.UpdateAgent(agent))

	// UpdateAt should be bumped
	assert.Greater(t, agent.UpdateAt, originalUpdateAt)

	// Verify round-trip
	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)

	assert.Equal(t, "Updated Agent", fetched.DisplayName)
	assert.Equal(t, "New instructions", fetched.CustomInstructions)
	assert.Equal(t, []string{"ch-3"}, fetched.ChannelIDs)
	assert.Nil(t, fetched.EnabledMCPTools)
	assert.Equal(t, "svc-2", fetched.ServiceID)

	// Immutable fields should not change
	assert.Equal(t, agent.CreatorID, fetched.CreatorID)
	assert.Equal(t, agent.BotUserID, fetched.BotUserID)
	assert.Equal(t, agent.CreateAt, fetched.CreateAt)
}

func TestAgentUpdateNonexistent(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := &llm.BotConfig{
		ID:          "nonexistent-id",
		DisplayName: "Ghost",
		Name:        "ghost",
		ServiceID:   "svc-1",
	}
	err = s.UpdateAgent(agent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already deleted")
}

func TestAgentSoftDelete(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "agent-1", "Agent 1")
	require.NoError(t, s.CreateAgent(agent))

	// Delete
	require.NoError(t, s.DeleteAgent(agent.ID))

	// Get should return nil
	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Nil(t, fetched)

	// Verify the row still exists with DeleteAt > 0 (soft delete)
	var deleteAt int64
	err = s.db.Get(&deleteAt, "SELECT DeleteAt FROM Agents_UserAgents WHERE ID = $1", agent.ID)
	require.NoError(t, err)
	assert.NotZero(t, deleteAt)
}

func TestAgentDeleteNonexistent(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	err = s.DeleteAgent("nonexistent-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already deleted")
}

func TestAgentDoubleDelete(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "agent-1", "Agent 1")
	require.NoError(t, s.CreateAgent(agent))

	// First delete succeeds
	require.NoError(t, s.DeleteAgent(agent.ID))

	// Second delete fails (already deleted)
	err = s.DeleteAgent(agent.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already deleted")
}

func TestAgentUpdateDeletedAgent(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "agent-1", "Agent 1")
	require.NoError(t, s.CreateAgent(agent))
	require.NoError(t, s.DeleteAgent(agent.ID))

	// Update should fail (WHERE DeleteAt = 0 clause)
	agent.DisplayName = "Should Fail"
	err = s.UpdateAgent(agent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already deleted")
}

func TestAgentEmptySliceFields(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	// Create agent with all slice fields nil/empty
	agent := &llm.BotConfig{
		BotUserID:   "bot-empty",
		CreatorID:   "creator-empty",
		DisplayName: "Empty Agent",
		Name:        "empty-agent",
		ServiceID:   "svc-1",
		// All slice fields intentionally left nil
	}
	require.NoError(t, s.CreateAgent(agent))

	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)

	// Nil slices round-trip as nil (not empty slices)
	assert.Nil(t, fetched.ChannelIDs)
	assert.Nil(t, fetched.UserIDs)
	assert.Nil(t, fetched.TeamIDs)
	assert.Nil(t, fetched.AdminUserIDs)
	assert.Nil(t, fetched.EnabledMCPTools)
}

func TestAgentAutoEnableNewMCPToolsRoundTrip(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "auto-mcp", "Auto MCP Agent")
	agent.AutoEnableNewMCPTools = true
	agent.EnabledMCPTools = nil // allowlist is ignored when auto-enable is on
	require.NoError(t, s.CreateAgent(agent))

	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.True(t, fetched.AutoEnableNewMCPTools)
	assert.Nil(t, fetched.EnabledMCPTools)

	// Flip it off and verify the flag updates cleanly.
	fetched.AutoEnableNewMCPTools = false
	require.NoError(t, s.UpdateAgent(fetched))

	again, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, again)
	assert.False(t, again.AutoEnableNewMCPTools)
}

func TestAgentMCPDynamicToolLoadingRoundTrip(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "dynamic-off", "Dynamic Off Agent")
	agent.MCPDynamicToolLoading = false
	require.NoError(t, s.CreateAgent(agent))

	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.False(t, fetched.MCPDynamicToolLoading)

	fetched.MCPDynamicToolLoading = true
	require.NoError(t, s.UpdateAgent(fetched))

	again, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, again)
	assert.True(t, again.MCPDynamicToolLoading)
}

func TestAgentEnabledMCPToolsBareAndNamespacedRoundTrip(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "mixed-mcp", "Mixed MCP Agent")
	agent.AutoEnableNewMCPTools = false
	agent.EnabledMCPTools = []llm.EnabledMCPTool{
		{ServerOrigin: "https://mcp.example.com", ToolName: "read_post"},
		{ServerOrigin: "embedded://mattermost", ToolName: "mattermost__search_users"},
	}
	require.NoError(t, s.CreateAgent(agent))

	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	require.Equal(t, agent.EnabledMCPTools, fetched.EnabledMCPTools)

	fetched.EnabledMCPTools = []llm.EnabledMCPTool{
		{ServerOrigin: "https://mcp.atlassian.com", ToolName: "get_issue"},
		{ServerOrigin: "https://api.githubcopilot.com", ToolName: "github__search"},
	}
	require.NoError(t, s.UpdateAgent(fetched))

	again, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, again)
	require.Equal(t, fetched.EnabledMCPTools, again.EnabledMCPTools)
}

func TestAgentConcurrentCreates(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	const count = 10
	errCh := make(chan error, count)

	for i := 0; i < count; i++ {
		go func(idx int) {
			a := testAgent("creator-1", fmt.Sprintf("agent-%d", idx), fmt.Sprintf("Agent %d", idx))
			errCh <- s.CreateAgent(a)
		}(i)
	}

	for i := 0; i < count; i++ {
		require.NoError(t, <-errCh)
	}

	agents, err := s.ListAgents()
	require.NoError(t, err)
	assert.Len(t, agents, count)
}

func TestAgentAdminLifecycleRoundTrip(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())

	cfg := &llm.BotConfig{
		BotUserID:    "bot-admin-test",
		CreatorID:    "creator-1",
		DisplayName:  "Admin Test",
		Name:         "admin-test",
		ServiceID:    "svc-1",
		AdminUserIDs: []string{"admin-a", "admin-b", "admin-c"},
	}
	require.NoError(t, s.CreateAgent(cfg))
	assert.NotZero(t, cfg.CreateAt)
	assert.Equal(t, cfg.CreateAt, cfg.UpdateAt)
	assert.Zero(t, cfg.DeleteAt)

	fetched, err := s.GetAgent(cfg.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Equal(t, []string{"admin-a", "admin-b", "admin-c"}, fetched.AdminUserIDs)
	assert.Equal(t, cfg.CreateAt, fetched.CreateAt)
	assert.Equal(t, cfg.UpdateAt, fetched.UpdateAt)
	assert.Zero(t, fetched.DeleteAt)

	originalCreateAt := cfg.CreateAt
	cfg.AdminUserIDs = []string{"admin-a"} // shrink
	require.NoError(t, s.UpdateAgent(cfg))
	fetched, err = s.GetAgent(cfg.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"admin-a"}, fetched.AdminUserIDs)
	assert.GreaterOrEqual(t, fetched.UpdateAt, originalCreateAt)
}
