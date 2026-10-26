// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createAgentWithVersions creates an agent and updates it until it has n versions.
func createAgentWithVersions(t *testing.T, s *Store, username string, n int) string {
	t.Helper()
	agent := testAgent("creator-1", username, username)
	require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))
	for i := 2; i <= n; i++ {
		agent.CustomInstructions = fmt.Sprintf("edit %d", i)
		require.NoError(t, s.UpdateAgent(agent, SystemAgentVersionMeta()))
	}
	return agent.ID
}

func remainingVersions(t *testing.T, s *Store, agentID string) []int {
	t.Helper()
	versions, err := listAllAgentVersions(s, agentID)
	require.NoError(t, err)
	return versionNumbers(versions)
}

func TestDeleteAgentVersions(t *testing.T) {
	s := setupVersionTestStore(t)

	tests := []struct {
		name          string
		versions      []int
		previous      bool
		wantErr       error
		wantDeleted   int64
		wantRemaining []int
	}{
		{name: "selected versions", versions: []int{2, 3}, wantDeleted: 2, wantRemaining: []int{5, 4, 1}},
		{name: "a single version", versions: []int{1}, wantDeleted: 1, wantRemaining: []int{5, 4, 3, 2}},
		{name: "missing versions are skipped", versions: []int{2, 99}, wantDeleted: 1, wantRemaining: []int{5, 4, 3, 1}},
		{name: "the current version is refused", versions: []int{5}, wantErr: ErrCurrentAgentVersion, wantRemaining: []int{5, 4, 3, 2, 1}},
		{name: "a selection with the current version deletes nothing", versions: []int{1, 5}, wantErr: ErrCurrentAgentVersion, wantRemaining: []int{5, 4, 3, 2, 1}},
		{name: "every previous version", previous: true, wantDeleted: 4, wantRemaining: []int{5}},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentID := createAgentWithVersions(t, s, fmt.Sprintf("delete-%d", i), 5)
			otherID := createAgentWithVersions(t, s, fmt.Sprintf("bystander-%d", i), 3)

			var deleted int64
			var err error
			if tt.previous {
				deleted, err = s.DeletePreviousAgentVersions(agentID)
			} else {
				deleted, err = s.DeleteAgentVersions(agentID, tt.versions)
			}
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantDeleted, deleted)
			}
			assert.Equal(t, tt.wantRemaining, remainingVersions(t, s, agentID))
			assert.Equal(t, []int{3, 2, 1}, remainingVersions(t, s, otherID), "other agents keep their versions")
		})
	}

	t.Run("numbering continues after deleting previous versions", func(t *testing.T) {
		agentID := createAgentWithVersions(t, s, "renumber", 3)
		_, err := s.DeletePreviousAgentVersions(agentID)
		require.NoError(t, err)

		agent, err := s.GetAgent(agentID)
		require.NoError(t, err)
		agent.CustomInstructions = "after cleanup"
		require.NoError(t, s.UpdateAgent(agent, SystemAgentVersionMeta()))

		versions, err := listAllAgentVersions(s, agentID)
		require.NoError(t, err)
		require.Equal(t, []int{4, 3}, versionNumbers(versions))
		assert.Equal(t, []string{"customInstructions"}, versions[0].ChangedFields)
	})

	t.Run("a deleted agent is refused", func(t *testing.T) {
		agentID := createAgentWithVersions(t, s, "gone", 2)
		require.NoError(t, s.DeleteAgent(agentID))
		_, err := s.DeleteAgentVersions(agentID, []int{1})
		require.Error(t, err)
		_, err = s.DeletePreviousAgentVersions(agentID)
		require.Error(t, err)
	})
}

func TestAgentVersionRetentionOnWrite(t *testing.T) {
	tests := []struct {
		name          string
		keep          int
		wantRemaining []int
	}{
		{name: "zero keeps every version", keep: 0, wantRemaining: []int{5, 4, 3, 2, 1}},
		{name: "negative keeps every version", keep: -3, wantRemaining: []int{5, 4, 3, 2, 1}},
		{name: "keep one", keep: 1, wantRemaining: []int{5}},
		{name: "keep three", keep: 3, wantRemaining: []int{5, 4, 3}},
		{name: "keep more than exist", keep: 10, wantRemaining: []int{5, 4, 3, 2, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupVersionTestStore(t)
			s.SetAgentVersionsToKeep(tt.keep)
			agentID := createAgentWithVersions(t, s, "retained", 5)
			assert.Equal(t, tt.wantRemaining, remainingVersions(t, s, agentID))
		})
	}

	t.Run("backfill of a drifted row prunes too", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agentID := createAgentWithVersions(t, s, "drift-retained", 3)
		s.SetAgentVersionsToKeep(2)
		setAgentServiceIDDirectly(t, s, agentID, "svc-remapped")

		backfilled, err := s.BackfillAgentVersions()
		require.NoError(t, err)
		assert.Equal(t, 1, backfilled)
		assert.Equal(t, []int{4, 3}, remainingVersions(t, s, agentID))
	})
}

func TestPruneAgentVersions(t *testing.T) {
	s := setupVersionTestStore(t)
	longID := createAgentWithVersions(t, s, "long-history", 5)
	shortID := createAgentWithVersions(t, s, "short-history", 2)

	deleted, err := s.PruneAgentVersions()
	require.NoError(t, err)
	assert.Zero(t, deleted, "keeping every version prunes nothing")

	s.SetAgentVersionsToKeep(3)
	deleted, err = s.PruneAgentVersions()
	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)
	assert.Equal(t, []int{5, 4, 3}, remainingVersions(t, s, longID))
	assert.Equal(t, []int{2, 1}, remainingVersions(t, s, shortID))

	deleted, err = s.PruneAgentVersions()
	require.NoError(t, err)
	assert.Zero(t, deleted, "pruning again is a no-op")
}

func TestDeleteAgentDeletesItsVersions(t *testing.T) {
	s := setupVersionTestStore(t)
	agentID := createAgentWithVersions(t, s, "deleted-agent", 3)
	otherID := createAgentWithVersions(t, s, "kept-agent", 2)

	require.NoError(t, s.DeleteAgent(agentID))
	assert.Empty(t, remainingVersions(t, s, agentID))
	assert.Equal(t, []int{2, 1}, remainingVersions(t, s, otherID))

	_, err := s.BackfillAgentVersions()
	require.NoError(t, err)
	assert.Empty(t, remainingVersions(t, s, agentID), "the backfill never recreates versions of a deleted agent")

	require.Error(t, s.DeleteAgent(agentID), "deleting twice still reports the missing agent")
}
