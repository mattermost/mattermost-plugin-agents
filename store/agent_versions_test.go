// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupVersionTestStore(t *testing.T) *Store {
	t.Helper()
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())
	return s
}

// insertUnversionedAgent writes an agent row directly, the way agents
// written before versioning existed look.
func insertUnversionedAgent(t *testing.T, s *Store, id, username, instructions string, deleteAt int64) {
	t.Helper()
	_, err := s.db.Exec(`
		INSERT INTO Agents_UserAgents (
			ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, CreateAt, UpdateAt, DeleteAt
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, "bot"+id[3:], "creator-1", "Legacy "+username, username, "svc-1",
		instructions, int64(100), int64(200), deleteAt,
	)
	require.NoError(t, err)
}

func versionNumbers(versions []AgentVersion) []int {
	out := make([]int, 0, len(versions))
	for _, v := range versions {
		out = append(out, v.Version)
	}
	return out
}

func TestAgentVersionLifecycle(t *testing.T) {
	s := setupVersionTestStore(t)

	agent := testAgent("creator-1", "versioned", "Versioned")
	require.NoError(t, s.CreateAgent(agent, AgentVersionMeta{ActorID: "creator-1", Source: AgentVersionSourceCreate}))

	agent.CustomInstructions = "Second instructions"
	require.NoError(t, s.UpdateAgent(agent, AgentVersionMeta{ActorID: "editor-1", Source: AgentVersionSourceUpdate}))

	v1, err := s.GetAgentVersion(agent.ID, 1)
	require.NoError(t, err)
	require.NotNil(t, v1)
	restored := v1.Config
	restored.ID = agent.ID
	restored.BotUserID = agent.BotUserID
	restored.CreatorID = agent.CreatorID
	restored.UpdateAt = agent.UpdateAt
	require.NoError(t, s.UpdateAgent(&restored, AgentVersionMeta{ActorID: "editor-2", Source: AgentVersionSourceRestore, RestoredFromVersion: 1}))

	versions, err := s.ListAgentVersions(agent.ID)
	require.NoError(t, err)
	require.Equal(t, []int{3, 2, 1}, versionNumbers(versions), "versions are listed newest first")

	tests := []struct {
		name                string
		version             AgentVersion
		createdBy           string
		source              AgentVersionSource
		restoredFromVersion int
		changedFields       []string
		instructions        string
	}{
		{name: "create", version: versions[2], createdBy: "creator-1", source: AgentVersionSourceCreate, changedFields: []string{}, instructions: "Be helpful and concise"},
		{name: "update", version: versions[1], createdBy: "editor-1", source: AgentVersionSourceUpdate, changedFields: []string{"customInstructions"}, instructions: "Second instructions"},
		{name: "restore", version: versions[0], createdBy: "editor-2", source: AgentVersionSourceRestore, restoredFromVersion: 1, changedFields: []string{"customInstructions"}, instructions: "Be helpful and concise"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.createdBy, tt.version.CreatedBy)
			assert.Equal(t, tt.source, tt.version.Source)
			assert.Equal(t, tt.restoredFromVersion, tt.version.RestoredFromVersion)
			assert.Equal(t, tt.changedFields, tt.version.ChangedFields)
			assert.NotZero(t, tt.version.CreateAt)

			detail, getErr := s.GetAgentVersion(agent.ID, tt.version.Version)
			require.NoError(t, getErr)
			require.NotNil(t, detail)
			assert.Equal(t, tt.version, detail.AgentVersion)
			assert.Equal(t, tt.instructions, detail.Config.CustomInstructions)
		})
	}

	t.Run("snapshot carries the full editable configuration", func(t *testing.T) {
		detail, getErr := s.GetAgentVersion(agent.ID, 1)
		require.NoError(t, getErr)
		want := testAgent("creator-1", "versioned", "Versioned")
		cfg := detail.Config
		assert.Equal(t, want.Name, cfg.Name)
		assert.Equal(t, want.DisplayName, cfg.DisplayName)
		assert.Equal(t, want.ServiceID, cfg.ServiceID)
		assert.Equal(t, want.ChannelIDs, cfg.ChannelIDs)
		assert.Equal(t, want.TeamIDs, cfg.TeamIDs)
		assert.Equal(t, want.AdminUserIDs, cfg.AdminUserIDs)
		assert.Equal(t, want.EnabledMCPTools, cfg.EnabledMCPTools)
		assert.Equal(t, want.EnabledNativeTools, cfg.EnabledNativeTools)
		assert.Equal(t, want.Model, cfg.Model)
		assert.Equal(t, want.MaxToolTurns, cfg.MaxToolTurns)
		assert.Equal(t, want.UseServiceAccountAuth, cfg.UseServiceAccountAuth)
		assert.Equal(t, want.ReasoningEffort, cfg.ReasoningEffort)
		assert.Equal(t, want.ThinkingBudget, cfg.ThinkingBudget)
	})

	t.Run("latest version and unknown version", func(t *testing.T) {
		latest, latestErr := s.GetLatestAgentVersion(agent.ID)
		require.NoError(t, latestErr)
		assert.Equal(t, 3, latest)

		missing, getErr := s.GetAgentVersion(agent.ID, 4)
		require.NoError(t, getErr)
		assert.Nil(t, missing)
	})

	t.Run("versions survive agent deletion", func(t *testing.T) {
		require.NoError(t, s.DeleteAgent(agent.ID))
		after, listErr := s.ListAgentVersions(agent.ID)
		require.NoError(t, listErr)
		assert.Len(t, after, 3)
	})
}

func TestAgentVersionChangedFields(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(cfg *llm.BotConfig)
		expected []string
	}{
		{
			name:     "save without changes records an empty diff",
			mutate:   func(cfg *llm.BotConfig) {},
			expected: []string{},
		},
		{
			name: "empty and nil collections are the same value",
			mutate: func(cfg *llm.BotConfig) {
				cfg.UserIDs = []string{}
				cfg.EnabledNativeTools = []string{}
			},
			expected: []string{},
		},
		{
			name: "multiple fields are reported sorted, by name only",
			mutate: func(cfg *llm.BotConfig) {
				cfg.DisplayName = "Renamed"
				cfg.ChannelIDs = []string{"ch-3"}
				cfg.MaxToolTurns = 7
			},
			expected: []string{"channelIDs", "displayName", "maxToolTurns"},
		},
		{
			name: "MCP tool list change",
			mutate: func(cfg *llm.BotConfig) {
				cfg.EnabledMCPTools = []llm.EnabledMCPTool{{ServerOrigin: "https://other.example.com", ToolName: "x"}}
			},
			expected: []string{"enabledMCPTools"},
		},
		{
			name: "deprecated structured output flag is not reported",
			mutate: func(cfg *llm.BotConfig) {
				cfg.StructuredOutputEnabled = !cfg.StructuredOutputEnabled //nolint:staticcheck // deprecated field still persisted
				cfg.Model = "other-model"
			},
			expected: []string{"model"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupVersionTestStore(t)
			agent := testAgent("creator-1", "diff-agent", "Diff Agent")
			agent.UserIDs = nil
			agent.EnabledNativeTools = nil
			require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

			fetched, err := s.GetAgent(agent.ID)
			require.NoError(t, err)
			tt.mutate(fetched)
			require.NoError(t, s.UpdateAgent(fetched, SystemAgentVersionMeta()))

			v2, err := s.GetAgentVersion(agent.ID, 2)
			require.NoError(t, err)
			require.NotNil(t, v2)
			assert.Equal(t, tt.expected, v2.ChangedFields)
		})
	}
}

func TestChangedVersionFieldsIgnoresUnversionedKeys(t *testing.T) {
	next := llm.BotConfig{Name: "agent", StructuredOutputEnabled: false} //nolint:staticcheck // deprecated field still persisted
	prevJSON, err := json.Marshal(next)
	require.NoError(t, err)
	var prev map[string]any
	require.NoError(t, json.Unmarshal(prevJSON, &prev))
	prev["service"] = map[string]any{"id": "svc-1", "type": "openai"}
	prev["structuredOutputEnabled"] = true
	prev["displayName"] = "Old Name"
	raw, err := json.Marshal(prev)
	require.NoError(t, err)

	assert.Equal(t, []string{"displayName"}, changedVersionFields(string(raw), next))
}

func TestAgentVersionWritesAreTransactional(t *testing.T) {
	tooLongActor := strings.Repeat("a", 40) // exceeds CreatedBy VARCHAR(26)
	tooLongUsername := strings.Repeat("u", 80)

	t.Run("create rolls back the agent when the version cannot be written", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "rb-create", "Rollback")
		err := s.CreateAgent(agent, AgentVersionMeta{ActorID: tooLongActor, Source: AgentVersionSourceCreate})
		require.Error(t, err)

		agents, listErr := s.ListAgents()
		require.NoError(t, listErr)
		assert.Empty(t, agents)
	})

	t.Run("update rolls back the agent when the version cannot be written", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "rb-update", "Rollback")
		require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

		changed := *agent
		changed.CustomInstructions = "must not persist"
		err := s.UpdateAgent(&changed, AgentVersionMeta{ActorID: tooLongActor, Source: AgentVersionSourceUpdate})
		require.Error(t, err)

		fetched, getErr := s.GetAgent(agent.ID)
		require.NoError(t, getErr)
		assert.Equal(t, "Be helpful and concise", fetched.CustomInstructions)
		versions, listErr := s.ListAgentVersions(agent.ID)
		require.NoError(t, listErr)
		assert.Equal(t, []int{1}, versionNumbers(versions))
	})

	t.Run("failed agent update writes no version", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "failed-update", "Failed")
		require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

		changed := *agent
		changed.Name = tooLongUsername
		require.Error(t, s.UpdateAgent(&changed, SystemAgentVersionMeta()))

		versions, listErr := s.ListAgentVersions(agent.ID)
		require.NoError(t, listErr)
		assert.Equal(t, []int{1}, versionNumbers(versions))
	})

	t.Run("update of a deleted agent writes no version", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "deleted-update", "Deleted")
		require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))
		require.NoError(t, s.DeleteAgent(agent.ID))

		require.Error(t, s.UpdateAgent(agent, SystemAgentVersionMeta()))
		versions, listErr := s.ListAgentVersions(agent.ID)
		require.NoError(t, listErr)
		assert.Equal(t, []int{1}, versionNumbers(versions))
	})
}

func TestAgentVersionMetaValidation(t *testing.T) {
	tests := []struct {
		name    string
		meta    AgentVersionMeta
		wantErr bool
	}{
		{name: "missing source", meta: AgentVersionMeta{ActorID: "u1"}, wantErr: true},
		{name: "unknown source", meta: AgentVersionMeta{Source: "bogus"}, wantErr: true},
		{name: "restore without restored version", meta: AgentVersionMeta{Source: AgentVersionSourceRestore}, wantErr: true},
		{name: "restored version on a non-restore source", meta: AgentVersionMeta{Source: AgentVersionSourceUpdate, RestoredFromVersion: 2}, wantErr: true},
		{name: "valid restore", meta: AgentVersionMeta{Source: AgentVersionSourceRestore, RestoredFromVersion: 1}},
		{name: "valid import", meta: AgentVersionMeta{ActorID: "u1", Source: AgentVersionSourceImport}},
	}

	s := setupVersionTestStore(t)
	agent := testAgent("creator-1", "meta-agent", "Meta")
	require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, err := s.GetLatestAgentVersion(agent.ID)
			require.NoError(t, err)

			err = s.UpdateAgent(agent, tt.meta)
			after, latestErr := s.GetLatestAgentVersion(agent.ID)
			require.NoError(t, latestErr)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, before, after)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, before+1, after)
		})
	}
}

func TestAgentVersionConcurrentUpdates(t *testing.T) {
	s := setupVersionTestStore(t)
	agent := testAgent("creator-1", "concurrent", "Concurrent")
	require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

	const writers = 12
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg := *agent
			cfg.CustomInstructions = fmt.Sprintf("writer %d", i)
			errCh <- s.UpdateAgent(&cfg, AgentVersionMeta{Source: AgentVersionSourceUpdate})
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	versions, err := s.ListAgentVersions(agent.ID)
	require.NoError(t, err)
	expected := make([]int, 0, writers+1)
	for v := writers + 1; v >= 1; v-- {
		expected = append(expected, v)
	}
	assert.Equal(t, expected, versionNumbers(versions), "concurrent saves get gapless, unique version numbers")
}

func TestAgentVersionUnversionedAgents(t *testing.T) {
	t.Run("backfill records version 1 once for active agents without versions", func(t *testing.T) {
		s := setupVersionTestStore(t)
		insertUnversionedAgent(t, s, "legacyagent000000000000001", "legacy-one", "legacy instructions", 0)
		insertUnversionedAgent(t, s, "legacyagent000000000000002", "legacy-two", "", 0)
		insertUnversionedAgent(t, s, "legacyagent000000000000003", "legacy-deleted", "", 500)
		versioned := testAgent("creator-1", "versioned", "Versioned")
		require.NoError(t, s.CreateAgent(versioned, SystemAgentVersionMeta()))

		backfilled, err := s.BackfillAgentVersions()
		require.NoError(t, err)
		assert.Equal(t, 2, backfilled)

		again, err := s.BackfillAgentVersions()
		require.NoError(t, err)
		assert.Zero(t, again, "a second run must not write anything")

		v1, err := s.GetAgentVersion("legacyagent000000000000001", 1)
		require.NoError(t, err)
		require.NotNil(t, v1)
		assert.Equal(t, AgentVersionSourceInitial, v1.Source)
		assert.Empty(t, v1.CreatedBy)
		assert.Empty(t, v1.ChangedFields)
		assert.Equal(t, int64(200), v1.CreateAt, "the initial version is dated by the agent's last update")
		assert.Equal(t, "legacy instructions", v1.Config.CustomInstructions)
		assert.Equal(t, "legacy-one", v1.Config.Name)

		deletedVersions, err := s.ListAgentVersions("legacyagent000000000000003")
		require.NoError(t, err)
		assert.Empty(t, deletedVersions, "soft-deleted agents are not backfilled")

		versionedVersions, err := s.ListAgentVersions(versioned.ID)
		require.NoError(t, err)
		assert.Equal(t, []int{1}, versionNumbers(versionedVersions))
	})

	t.Run("concurrent backfills write each initial version once", func(t *testing.T) {
		s := setupVersionTestStore(t)
		for i := range 5 {
			insertUnversionedAgent(t, s, fmt.Sprintf("legacyagent%015d", i), fmt.Sprintf("legacy-%d", i), "", 0)
		}

		const nodes = 4
		var wg sync.WaitGroup
		counts := make(chan int, nodes)
		errCh := make(chan error, nodes)
		for range nodes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				n, err := s.BackfillAgentVersions()
				counts <- n
				errCh <- err
			}()
		}
		wg.Wait()
		close(counts)
		close(errCh)
		for err := range errCh {
			require.NoError(t, err)
		}
		total := 0
		for n := range counts {
			total += n
		}
		assert.Equal(t, 5, total)

		var rows int
		require.NoError(t, s.db.Get(&rows, `SELECT COUNT(*) FROM Agents_AgentVersions`))
		assert.Equal(t, 5, rows)
	})

	t.Run("first update of an unversioned agent records its prior state as version 1", func(t *testing.T) {
		s := setupVersionTestStore(t)
		insertUnversionedAgent(t, s, "legacyagent000000000000001", "legacy-one", "before", 0)

		cfg, err := s.GetAgent("legacyagent000000000000001")
		require.NoError(t, err)
		cfg.CustomInstructions = "after"
		require.NoError(t, s.UpdateAgent(cfg, AgentVersionMeta{ActorID: "editor-1", Source: AgentVersionSourceUpdate}))

		versions, err := s.ListAgentVersions(cfg.ID)
		require.NoError(t, err)
		require.Equal(t, []int{2, 1}, versionNumbers(versions))
		assert.Equal(t, AgentVersionSourceInitial, versions[1].Source)
		assert.Equal(t, AgentVersionSourceUpdate, versions[0].Source)
		assert.Equal(t, []string{"customInstructions"}, versions[0].ChangedFields)

		v1, err := s.GetAgentVersion(cfg.ID, 1)
		require.NoError(t, err)
		assert.Equal(t, "before", v1.Config.CustomInstructions)
	})
}

// setAgentServiceIDDirectly rewrites an agent row without recording a
// version, the way the service ID migration does.
func setAgentServiceIDDirectly(t *testing.T, s *Store, agentID, serviceID string) {
	t.Helper()
	_, err := s.db.Exec(`UPDATE Agents_UserAgents SET ServiceID = $1 WHERE ID = $2`, serviceID, agentID)
	require.NoError(t, err)
}

func TestAgentVersionRowDrift(t *testing.T) {
	t.Run("update first records a row that changed without a version", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "drifted", "Drifted")
		require.NoError(t, s.CreateAgent(agent, AgentVersionMeta{ActorID: "creator-1", Source: AgentVersionSourceCreate}))
		setAgentServiceIDDirectly(t, s, agent.ID, "svc-remapped")

		cfg, err := s.GetAgent(agent.ID)
		require.NoError(t, err)
		cfg.CustomInstructions = "edited"
		require.NoError(t, s.UpdateAgent(cfg, AgentVersionMeta{ActorID: "editor-1", Source: AgentVersionSourceUpdate}))

		versions, err := s.ListAgentVersions(agent.ID)
		require.NoError(t, err)
		require.Equal(t, []int{3, 2, 1}, versionNumbers(versions))
		assert.Equal(t, AgentVersionSourceSystem, versions[1].Source)
		assert.Empty(t, versions[1].CreatedBy)
		assert.Equal(t, []string{"serviceID"}, versions[1].ChangedFields)
		assert.Equal(t, AgentVersionSourceUpdate, versions[0].Source)
		assert.Equal(t, "editor-1", versions[0].CreatedBy)
		assert.Equal(t, []string{"customInstructions"}, versions[0].ChangedFields, "the editor's version lists only the editor's change")

		v2, err := s.GetAgentVersion(agent.ID, 2)
		require.NoError(t, err)
		assert.Equal(t, "svc-remapped", v2.Config.ServiceID)
		assert.Equal(t, "Be helpful and concise", v2.Config.CustomInstructions)
	})

	t.Run("update of an unchanged row records only the new version", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "in-sync", "In Sync")
		require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))
		agent.CustomInstructions = "edited"
		require.NoError(t, s.UpdateAgent(agent, SystemAgentVersionMeta()))

		versions, err := s.ListAgentVersions(agent.ID)
		require.NoError(t, err)
		assert.Equal(t, []int{2, 1}, versionNumbers(versions))
	})

	t.Run("backfill records a row that changed without a version once", func(t *testing.T) {
		s := setupVersionTestStore(t)
		drifted := testAgent("creator-1", "drifted", "Drifted")
		require.NoError(t, s.CreateAgent(drifted, SystemAgentVersionMeta()))
		inSync := testAgent("creator-1", "in-sync", "In Sync")
		require.NoError(t, s.CreateAgent(inSync, SystemAgentVersionMeta()))
		setAgentServiceIDDirectly(t, s, drifted.ID, "svc-remapped")

		backfilled, err := s.BackfillAgentVersions()
		require.NoError(t, err)
		assert.Equal(t, 1, backfilled)
		again, err := s.BackfillAgentVersions()
		require.NoError(t, err)
		assert.Zero(t, again)

		versions, err := s.ListAgentVersions(drifted.ID)
		require.NoError(t, err)
		require.Equal(t, []int{2, 1}, versionNumbers(versions))
		assert.Equal(t, AgentVersionSourceSystem, versions[0].Source)
		assert.Equal(t, []string{"serviceID"}, versions[0].ChangedFields)

		inSyncVersions, err := s.ListAgentVersions(inSync.ID)
		require.NoError(t, err)
		assert.Equal(t, []int{1}, versionNumbers(inSyncVersions))
	})
}

func TestBackfillAgentVersionsContinuesPastFailures(t *testing.T) {
	s := setupVersionTestStore(t)
	const broken = "legacyagent000000000000001"
	insertUnversionedAgent(t, s, broken, "legacy-broken", "", 0)
	insertUnversionedAgent(t, s, "legacyagent000000000000002", "legacy-two", "", 0)
	insertUnversionedAgent(t, s, "legacyagent000000000000003", "legacy-three", "", 0)
	_, err := s.db.Exec(`UPDATE Agents_UserAgents SET ChannelIDs = 'not json' WHERE ID = $1`, broken)
	require.NoError(t, err)

	backfilled, err := s.BackfillAgentVersions()
	require.Error(t, err)
	assert.Contains(t, err.Error(), broken)
	assert.Equal(t, 2, backfilled, "agents after the failing one are still backfilled")

	for _, id := range []string{"legacyagent000000000000002", "legacyagent000000000000003"} {
		versions, listErr := s.ListAgentVersions(id)
		require.NoError(t, listErr)
		assert.Equal(t, []int{1}, versionNumbers(versions))
	}
	brokenVersions, err := s.ListAgentVersions(broken)
	require.NoError(t, err)
	assert.Empty(t, brokenVersions)
}
