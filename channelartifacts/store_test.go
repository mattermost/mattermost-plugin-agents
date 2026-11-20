// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost/server/public/model"
)

func newVersion(t *testing.T, db *sqlx.DB, sha string) channelartifacts.NewVersion {
	t.Helper()
	v := channelartifacts.NewVersion{
		PostID: model.NewId(), FileID: model.NewId(), SHA256: sha, Size: 10,
		SourceBotUserID: model.NewId(), UserID: model.NewId(), At: model.GetMillis(),
	}
	seedSource(t, db, v.PostID, v.FileID)
	return v
}

func sha(n int) string { return fmt.Sprintf("%064d", n) }

func TestStoreCreateAndListReportAvailability(t *testing.T) {
	db := testDB(t)
	s := channelartifacts.NewStore(db)
	channelID := model.NewId()
	v := newVersion(t, db, sha(1))
	id := model.NewId()
	require.NoError(t, s.Create(id, channelID, "Dashboard", v, 5))

	entries, err := s.List(channelID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	got := entries[0]
	assert.Equal(t, id, got.ID)
	assert.Equal(t, 1, got.Version)
	assert.Equal(t, v.FileID, got.SourceFileID)
	assert.Equal(t, v.SourceBotUserID, got.SourceBotUserID)
	assert.True(t, got.Available)

	tests := []struct {
		name    string
		breakIt string
	}{
		{name: "file deleted", breakIt: `UPDATE FileInfo SET DeleteAt = 1 WHERE Id = $1`},
		{name: "post deleted", breakIt: `UPDATE Posts SET DeleteAt = 1 WHERE Id = (SELECT PostId FROM FileInfo WHERE Id = $1)`},
		{name: "file row gone", breakIt: `DELETE FROM FileInfo WHERE Id = $1`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			s := channelartifacts.NewStore(db)
			v := newVersion(t, db, sha(2))
			id := model.NewId()
			require.NoError(t, s.Create(id, model.NewId(), "T", v, 5))
			_, err := db.Exec(tt.breakIt, v.FileID)
			require.NoError(t, err)

			entry, err := s.Get(id)
			require.NoError(t, err, "an unavailable entry stays listed instead of being deleted")
			assert.False(t, entry.Available)
			versions, err := s.Versions(id)
			require.NoError(t, err)
			require.Len(t, versions, 1)
			assert.False(t, versions[0].Available)
		})
	}
}

func TestStoreCreateEnforcesCapAndDedupe(t *testing.T) {
	db := testDB(t)
	s := channelartifacts.NewStore(db)
	channelID := model.NewId()
	first := model.NewId()
	require.NoError(t, s.Create(first, channelID, "One", newVersion(t, db, sha(1)), 2))

	tests := []struct {
		name    string
		sha     string
		channel string
		wantErr error
		wantDup string
	}{
		{name: "same content in the channel", sha: sha(1), channel: channelID, wantErr: channelartifacts.ErrDuplicate, wantDup: first},
		{name: "same content in another channel", sha: sha(1), channel: model.NewId()},
		{name: "second entry fits", sha: sha(2), channel: channelID},
		{name: "third entry is over the cap", sha: sha(3), channel: channelID, wantErr: channelartifacts.ErrLimitReached},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Create(model.NewId(), tt.channel, "T", newVersion(t, db, tt.sha), 2)
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
			if tt.wantDup != "" {
				dup, ok := err.(*channelartifacts.DuplicateError)
				require.True(t, ok)
				assert.Equal(t, tt.wantDup, dup.ArtifactID)
			}
		})
	}
}

func TestStoreCreateHonoursTheCapUnderConcurrency(t *testing.T) {
	db := testDB(t)
	s := channelartifacts.NewStore(db)
	channelID := model.NewId()
	versions := make([]channelartifacts.NewVersion, 12)
	for i := range versions {
		versions[i] = newVersion(t, db, sha(100+i))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	for _, v := range versions {
		wg.Go(func() {
			err := s.Create(model.NewId(), channelID, "T", v, 3)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				created++
			} else {
				assert.ErrorIs(t, err, channelartifacts.ErrLimitReached)
			}
		})
	}
	wg.Wait()
	assert.Equal(t, 3, created)
}

func TestStoreAddVersionPrunesRowsButKeepsTheHead(t *testing.T) {
	db := testDB(t)
	s := channelartifacts.NewStore(db)
	channelID := model.NewId()
	id := model.NewId()
	require.NoError(t, s.Create(id, channelID, "T", newVersion(t, db, sha(1)), 5))
	for i := 2; i <= 4; i++ {
		require.NoError(t, s.AddVersion(id, newVersion(t, db, sha(i)), 2))
	}

	versions, err := s.Versions(id)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, 4, versions[0].Version)
	assert.True(t, versions[0].Head)
	assert.Equal(t, 3, versions[1].Version)

	err = s.AddVersion(id, newVersion(t, db, sha(4)), 2)
	var dup *channelartifacts.DuplicateError
	require.ErrorAs(t, err, &dup)
	assert.Equal(t, id, dup.ArtifactID)
	require.ErrorIs(t, s.AddVersion(model.NewId(), newVersion(t, db, sha(9)), 2), channelartifacts.ErrNotFound)
}

func TestStoreHeadAndDeleteOperations(t *testing.T) {
	db := testDB(t)
	s := channelartifacts.NewStore(db)
	id := model.NewId()
	require.NoError(t, s.Create(id, model.NewId(), "T", newVersion(t, db, sha(1)), 5))
	require.NoError(t, s.AddVersion(id, newVersion(t, db, sha(2)), 5))
	require.NoError(t, s.AddVersion(id, newVersion(t, db, sha(3)), 5))
	user := model.NewId()

	require.NoError(t, s.SetHead(id, 1, user, 100))
	entry, err := s.Get(id)
	require.NoError(t, err)
	assert.Equal(t, 1, entry.Version)
	assert.Equal(t, user, entry.UpdatedBy)
	require.ErrorIs(t, s.SetHead(id, 9, user, 100), channelartifacts.ErrNotFound)

	require.NoError(t, s.DeleteVersion(id, 1, user, 200))
	entry, err = s.Get(id)
	require.NoError(t, err)
	assert.Equal(t, 3, entry.Version, "deleting the current version moves it to the newest remaining")

	require.NoError(t, s.DeleteVersion(id, 2, user, 300))
	require.ErrorIs(t, s.DeleteVersion(id, 3, user, 400), channelartifacts.ErrInvalidInput)
	versions, err := s.Versions(id)
	require.NoError(t, err)
	assert.Len(t, versions, 1, "the last version cannot be removed")

	require.NoError(t, s.Rename(id, "Renamed", user, 500))
	entry, err = s.Get(id)
	require.NoError(t, err)
	assert.Equal(t, "Renamed", entry.Title)

	require.NoError(t, s.Purge(id))
	_, err = s.Get(id)
	require.ErrorIs(t, err, channelartifacts.ErrNotFound)
	var leftover int
	require.NoError(t, db.Get(&leftover, `SELECT COUNT(*) FROM Agents_ChannelArtifactVersions WHERE ArtifactID = $1`, id))
	assert.Zero(t, leftover)
	require.ErrorIs(t, s.Purge(id), channelartifacts.ErrNotFound)
}

func TestStoreAddVersionNeverReusesADeletedVersionNumber(t *testing.T) {
	db := testDB(t)
	s := channelartifacts.NewStore(db)
	id := model.NewId()
	user := model.NewId()
	require.NoError(t, s.Create(id, model.NewId(), "T", newVersion(t, db, sha(1)), 5))
	require.NoError(t, s.AddVersion(id, newVersion(t, db, sha(2)), 5))
	require.NoError(t, s.AddVersion(id, newVersion(t, db, sha(3)), 5))
	require.NoError(t, s.DeleteVersion(id, 3, user, 100))

	require.NoError(t, s.AddVersion(id, newVersion(t, db, sha(4)), 5))
	entry, err := s.Get(id)
	require.NoError(t, err)
	assert.Equal(t, 4, entry.Version, "the number of the deleted version is not given to new content")

	_, err = db.Exec(`UPDATE Agents_ChannelArtifacts SET LastVersion = 0 WHERE ID = $1`, id)
	require.NoError(t, err)
	require.NoError(t, s.AddVersion(id, newVersion(t, db, sha(5)), 5))
	entry, err = s.Get(id)
	require.NoError(t, err)
	assert.Equal(t, 5, entry.Version, "an artifact saved before the counter existed still gets the next number")
}

func TestStoreSweepDeletedChannelsOnlyRemovesRows(t *testing.T) {
	db := testDB(t)
	s := channelartifacts.NewStore(db)
	live, gone := model.NewId(), model.NewId()
	_, err := db.Exec(`INSERT INTO Channels (Id) VALUES ($1)`, live)
	require.NoError(t, err)
	keep, drop := model.NewId(), model.NewId()
	require.NoError(t, s.Create(keep, live, "Keep", newVersion(t, db, sha(1)), 5))
	dropVersion := newVersion(t, db, sha(2))
	require.NoError(t, s.Create(drop, gone, "Drop", dropVersion, 5))
	require.NoError(t, s.AddVersion(drop, newVersion(t, db, sha(3)), 5))

	removed, err := s.SweepDeletedChannels()
	require.NoError(t, err)
	assert.EqualValues(t, 1, removed)
	_, err = s.Get(keep)
	require.NoError(t, err)
	_, err = s.Get(drop)
	require.ErrorIs(t, err, channelartifacts.ErrNotFound)
	var versionRows int
	require.NoError(t, db.Get(&versionRows, `SELECT COUNT(*) FROM Agents_ChannelArtifactVersions WHERE ArtifactID = $1`, drop))
	assert.Zero(t, versionRows)

	var files int
	require.NoError(t, db.Get(&files, `SELECT COUNT(*) FROM FileInfo WHERE Id = $1`, dropVersion.FileID))
	assert.Equal(t, 1, files, "the library never touches source files")
}

func TestStoreAddVersionRenamesInTheSameTransaction(t *testing.T) {
	db := testDB(t)
	s := channelartifacts.NewStore(db)
	id := model.NewId()
	require.NoError(t, s.Create(id, model.NewId(), "Old", newVersion(t, db, sha(1)), 5))

	renamed := newVersion(t, db, sha(2))
	renamed.Title = "New"
	require.NoError(t, s.AddVersion(id, renamed, 5))
	entry, err := s.Get(id)
	require.NoError(t, err)
	assert.Equal(t, "New", entry.Title)
	assert.Equal(t, 2, entry.Version)

	untitled := newVersion(t, db, sha(3))
	require.NoError(t, s.AddVersion(id, untitled, 5))
	entry, err = s.Get(id)
	require.NoError(t, err)
	assert.Equal(t, "New", entry.Title, "a version without a title keeps the current one")

	duplicate := newVersion(t, db, sha(3))
	duplicate.Title = "Never applied"
	require.Error(t, s.AddVersion(id, duplicate, 5))
	entry, err = s.Get(id)
	require.NoError(t, err)
	assert.Equal(t, "New", entry.Title, "a refused version does not rename")
}

func TestStoreAddVersionRacingPurgeLeavesNoOrphanRows(t *testing.T) {
	db := testDB(t)
	s := channelartifacts.NewStore(db)

	for i := range 20 {
		id := model.NewId()
		require.NoError(t, s.Create(id, model.NewId(), "T", newVersion(t, db, sha(1000+i*2)), 5))
		next := newVersion(t, db, sha(1001+i*2))

		var wg sync.WaitGroup
		wg.Go(func() {
			if err := s.AddVersion(id, next, 5); err != nil {
				assert.ErrorIs(t, err, channelartifacts.ErrNotFound)
			}
		})
		wg.Go(func() {
			if err := s.Purge(id); err != nil {
				assert.ErrorIs(t, err, channelartifacts.ErrNotFound)
			}
		})
		wg.Wait()
	}

	var orphans int
	require.NoError(t, db.Get(&orphans, `
		SELECT COUNT(*) FROM Agents_ChannelArtifactVersions v
		WHERE NOT EXISTS (SELECT 1 FROM Agents_ChannelArtifacts a WHERE a.ID = v.ArtifactID)`))
	assert.Zero(t, orphans)
}
