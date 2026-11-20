// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimEndingResume(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		staleSeen bool
		wantErr   error
	}{
		{name: "an unchanged ending project is claimed", state: ProjectStateEnding},
		{name: "a project another end already touched is not", state: ProjectStateEnding, staleSeen: true, wantErr: ErrProjectStateChanged},
		{name: "an active project is not", state: ProjectStateActive, wantErr: ErrProjectStateChanged},
		{name: "an ended project is not", state: ProjectStateEnded, wantErr: ErrProjectStateChanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())
			require.NoError(t, s.ClaimProject(&Project{ChannelID: "P", TeamID: "T", LeadUserID: "lead", CreatedBy: "lead", CreateAt: 1000}))
			_, err := s.db.Exec(`UPDATE Agents_Projects SET State = $1 WHERE ChannelID = 'P'`, tt.state)
			require.NoError(t, err)

			seen, err := s.GetProject("P")
			require.NoError(t, err)
			updateAt := seen.UpdateAt
			if tt.staleSeen {
				updateAt--
			}

			err = s.ClaimEndingResume("P", updateAt)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			after, err := s.GetProject("P")
			require.NoError(t, err)
			assert.Greater(t, after.UpdateAt, seen.UpdateAt)
			assert.ErrorIs(t, s.ClaimEndingResume("P", seen.UpdateAt), ErrProjectStateChanged, "a second resume of the same view loses")
		})
	}
}

func TestProjectThreadExternalIDIsWriteOnce(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		root     string
		set      string
		wantErr  error
		wantID   string
	}{
		{name: "first agent is recorded", root: "run", set: "bc-1", wantID: "bc-1"},
		{name: "the same agent again is fine", existing: "bc-1", root: "run", set: "bc-1", wantID: "bc-1"},
		{name: "another agent is refused", existing: "bc-1", root: "run", set: "bc-2", wantErr: ErrProjectLinkSet, wantID: "bc-1"},
		{name: "a post that isn't a run thread", root: "chat", set: "bc-1", wantErr: ErrProjectThreadNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())
			require.NoError(t, s.ClaimProject(&Project{ChannelID: "P", TeamID: "T", LeadUserID: "lead", CreatedBy: "lead", CreateAt: 1000}))
			require.NoError(t, s.AddProjectThread(&ProjectThread{RootPostID: "run", ChannelID: "P", Kind: ProjectThreadKindCursor, ExternalID: tt.existing, StartedBy: "lead", CreateAt: 1}))

			err := s.SetProjectThreadExternalID("P", tt.root, tt.set)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tt.wantID != "" {
				thread, getErr := s.GetProjectThread("P", "run")
				require.NoError(t, getErr)
				assert.Equal(t, tt.wantID, thread.ExternalID)
			}
			_, err = s.GetProjectThread("P", "chat")
			assert.ErrorIs(t, err, ErrProjectThreadNotFound)
		})
	}
}
