// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelcontext"
	"github.com/mattermost/mattermost/server/public/model"
)

type channelContextRow struct {
	ChannelID          string          `db:"channelid"`
	CustomInstructions string          `db:"custominstructions"`
	FileIDs            json.RawMessage `db:"fileids"`
	CreateAt           int64           `db:"createat"`
	UpdateAt           int64           `db:"updateat"`
}

// GetChannelContext returns the context for channelID, or nil when none exists.
func (s *Store) GetChannelContext(channelID string) (*channelcontext.Record, error) {
	var row channelContextRow
	err := s.db.Get(&row, `
		SELECT ChannelID, CustomInstructions, FileIDs, CreateAt, UpdateAt
		FROM Agents_ChannelContexts
		WHERE ChannelID = $1`,
		channelID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get channel context %q: %w", channelID, err)
	}

	var fileIDs []string
	if err := json.Unmarshal(row.FileIDs, &fileIDs); err != nil {
		return nil, fmt.Errorf("failed to parse file IDs for channel context %q: %w", channelID, err)
	}
	if fileIDs == nil {
		fileIDs = []string{}
	}

	return &channelcontext.Record{
		ChannelID:          row.ChannelID,
		CustomInstructions: row.CustomInstructions,
		FileIDs:            fileIDs,
		CreateAt:           row.CreateAt,
		UpdateAt:           row.UpdateAt,
	}, nil
}

// SaveChannelContext inserts or replaces a channel context at the expected version.
func (s *Store) SaveChannelContext(record *channelcontext.Record, expectedUpdateAt int64) error {
	fileIDs := record.FileIDs
	if fileIDs == nil {
		fileIDs = []string{}
	}
	fileIDsJSON, err := json.Marshal(fileIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal channel context file IDs: %w", err)
	}

	now := model.GetMillis()
	if expectedUpdateAt == 0 {
		err = s.db.QueryRowx(`
			INSERT INTO Agents_ChannelContexts (
				ChannelID, CustomInstructions, FileIDs, CreateAt, UpdateAt
			) VALUES ($1, $2, $3::jsonb, $4, $4)
			ON CONFLICT (ChannelID) DO NOTHING
			RETURNING CreateAt, UpdateAt`,
			record.ChannelID,
			record.CustomInstructions,
			string(fileIDsJSON),
			now,
		).Scan(&record.CreateAt, &record.UpdateAt)
	} else {
		err = s.db.QueryRowx(`
			UPDATE Agents_ChannelContexts SET
				CustomInstructions = $1,
				FileIDs = $2::jsonb,
				UpdateAt = GREATEST(UpdateAt + 1, $3)
			WHERE ChannelID = $4 AND UpdateAt = $5
			RETURNING CreateAt, UpdateAt`,
			record.CustomInstructions,
			string(fileIDsJSON),
			now,
			record.ChannelID,
			expectedUpdateAt,
		).Scan(&record.CreateAt, &record.UpdateAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return channelcontext.ErrConflict
	}
	if err != nil {
		return fmt.Errorf("failed to save channel context %q: %w", record.ChannelID, err)
	}

	return nil
}

// DeleteChannelContext permanently deletes a channel context at the expected version.
func (s *Store) DeleteChannelContext(channelID string, expectedUpdateAt int64) error {
	var deletedChannelID string
	err := s.db.QueryRowx(
		`DELETE FROM Agents_ChannelContexts WHERE ChannelID = $1 AND UpdateAt = $2 RETURNING ChannelID`,
		channelID,
		expectedUpdateAt,
	).Scan(&deletedChannelID)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if existsErr := s.db.Get(&exists, `SELECT EXISTS(SELECT 1 FROM Agents_ChannelContexts WHERE ChannelID = $1)`, channelID); existsErr != nil {
			return fmt.Errorf("failed to check channel context %q after delete: %w", channelID, existsErr)
		}
		if exists {
			return channelcontext.ErrConflict
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete channel context %q: %w", channelID, err)
	}
	return nil
}

var _ channelcontext.Store = (*Store)(nil)
