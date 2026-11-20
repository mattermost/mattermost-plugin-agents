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
