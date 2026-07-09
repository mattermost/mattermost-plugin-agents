// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelcontext"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelContextStoreLifecycle(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())

	channelID := model.NewId()
	record, err := s.GetChannelContext(channelID)
	require.NoError(t, err)
	assert.Nil(t, record)

	record = &channelcontext.Record{
		ChannelID:          channelID,
		CustomInstructions: "Use the release glossary.",
	}
	require.NoError(t, s.SaveChannelContext(record, 0))
	assert.Positive(t, record.CreateAt)
	assert.Equal(t, record.CreateAt, record.UpdateAt)

	loaded, err := s.GetChannelContext(channelID)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, record.CustomInstructions, loaded.CustomInstructions)
	assert.Empty(t, loaded.FileIDs)

	createAt := loaded.CreateAt
	updateAt := loaded.UpdateAt
	firstFileID := model.NewId()
	secondFileID := model.NewId()
	loaded.CustomInstructions = "Prefer the latest release notes."
	loaded.FileIDs = []string{secondFileID, firstFileID}
	require.NoError(t, s.SaveChannelContext(loaded, updateAt))

	assert.Equal(t, createAt, loaded.CreateAt)
	assert.Greater(t, loaded.UpdateAt, updateAt)

	updated, err := s.GetChannelContext(channelID)
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, []string{secondFileID, firstFileID}, updated.FileIDs)
	assert.Equal(t, "Prefer the latest release notes.", updated.CustomInstructions)

	var count int
	require.NoError(t, s.db.Get(&count, `SELECT COUNT(*) FROM Agents_ChannelContexts WHERE ChannelID = $1`, channelID))
	assert.Equal(t, 1, count)

	require.NoError(t, s.DeleteChannelContext(channelID, updated.UpdateAt))
	require.NoError(t, s.DeleteChannelContext(channelID, 0))
	deleted, err := s.GetChannelContext(channelID)
	require.NoError(t, err)
	assert.Nil(t, deleted)
}

func TestChannelContextStoreRejectsStaleVersion(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())

	record := &channelcontext.Record{ChannelID: model.NewId(), CustomInstructions: "initial"}
	require.NoError(t, s.SaveChannelContext(record, 0))

	staleVersion := record.UpdateAt
	firstUpdate := &channelcontext.Record{ChannelID: record.ChannelID, CustomInstructions: "first update"}
	require.NoError(t, s.SaveChannelContext(firstUpdate, staleVersion))

	staleUpdate := &channelcontext.Record{ChannelID: record.ChannelID, CustomInstructions: "stale update"}
	require.ErrorIs(t, s.SaveChannelContext(staleUpdate, staleVersion), channelcontext.ErrConflict)
	require.ErrorIs(t, s.DeleteChannelContext(record.ChannelID, staleVersion), channelcontext.ErrConflict)
}

func TestGetChannelContextRejectsNonArrayFileIDs(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())

	channelID := model.NewId()
	_, err := s.db.Exec(`
		INSERT INTO Agents_ChannelContexts (
			ChannelID, CustomInstructions, FileIDs, CreateAt, UpdateAt
		) VALUES ($1, '', '"not-an-array"'::jsonb, 1, 1)`,
		channelID,
	)
	require.NoError(t, err)

	record, err := s.GetChannelContext(channelID)
	require.Error(t, err)
	assert.Nil(t, record)
	assert.Contains(t, err.Error(), "failed to parse file IDs")
}
