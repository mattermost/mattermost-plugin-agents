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

// SaveChannelContext inserts or replaces a channel context.
func (s *Store) SaveChannelContext(record *channelcontext.Record) error {
	fileIDs := record.FileIDs
	if fileIDs == nil {
		fileIDs = []string{}
	}
	fileIDsJSON, err := json.Marshal(fileIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal channel context file IDs: %w", err)
	}

	now := model.GetMillis()
	err = s.db.QueryRowx(`
		INSERT INTO Agents_ChannelContexts (
			ChannelID, CustomInstructions, FileIDs, CreateAt, UpdateAt
		) VALUES ($1, $2, $3::jsonb, $4, $4)
		ON CONFLICT (ChannelID) DO UPDATE SET
			CustomInstructions = EXCLUDED.CustomInstructions,
			FileIDs = EXCLUDED.FileIDs,
			UpdateAt = GREATEST(Agents_ChannelContexts.UpdateAt + 1, EXCLUDED.UpdateAt)
		RETURNING CreateAt, UpdateAt`,
		record.ChannelID,
		record.CustomInstructions,
		string(fileIDsJSON),
		now,
	).Scan(&record.CreateAt, &record.UpdateAt)
	if err != nil {
		return fmt.Errorf("failed to save channel context %q: %w", record.ChannelID, err)
	}

	return nil
}

// DeleteChannelContext permanently deletes a channel context.
func (s *Store) DeleteChannelContext(channelID string) error {
	if _, err := s.db.Exec(`DELETE FROM Agents_ChannelContexts WHERE ChannelID = $1`, channelID); err != nil {
		return fmt.Errorf("failed to delete channel context %q: %w", channelID, err)
	}
	return nil
}

var _ channelcontext.Store = (*Store)(nil)
