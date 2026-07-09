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
	require.NoError(t, s.SaveChannelContext(record))
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
	require.NoError(t, s.SaveChannelContext(loaded))

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

	require.NoError(t, s.DeleteChannelContext(channelID))
	require.NoError(t, s.DeleteChannelContext(channelID))
	deleted, err := s.GetChannelContext(channelID)
	require.NoError(t, err)
	assert.Nil(t, deleted)
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
